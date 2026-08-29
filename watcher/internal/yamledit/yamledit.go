package yamledit

import (
	"fmt"
	"os"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Editing the parsed document keeps the comments people wrote in their config,
// re-marshalling the Config would silently drop every one of them.
type Document struct {
	path string
	root *yaml.Node
}

func Load(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s is not valid YAML: %w", path, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	return &Document{path: path, root: doc.Content[0]}, nil
}

// Creates the intermediate mappings when a section is missing, so a config
// written before a feature existed can still be edited.
func (d *Document) Set(path []string, value any) error {
	node := d.root
	for _, key := range path[:len(path)-1] {
		child, err := mappingChild(node, key)
		if err != nil {
			return err
		}
		node = child
	}
	return setMappingValue(node, path[len(path)-1], value)
}

func mappingChild(node *yaml.Node, key string) (*yaml.Node, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%q is not a section", key)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			value := node.Content[i+1]
			if value.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("%q is not a section", key)
			}
			return value, nil
		}
	}

	created := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, created)
	return created, nil
}

func setMappingValue(node *yaml.Node, key string, value any) error {
	encoded := &yaml.Node{}
	if err := encoded.Encode(value); err != nil {
		return err
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != key {
			continue
		}
		existing := node.Content[i+1]
		// A quoted string stays quoted, otherwise every edit reflows the file.
		if existing.Kind == encoded.Kind {
			encoded.Style = existing.Style
		}
		encoded.HeadComment = existing.HeadComment
		encoded.LineComment = existing.LineComment
		encoded.FootComment = existing.FootComment
		node.Content[i+1] = encoded
		return nil
	}

	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, encoded)
	return nil
}

// The file holds the DuckDNS token. WriteFile leaves an existing file's mode
// alone, so it is tightened explicitly rather than assumed.
func (d *Document) Save() error {
	data, err := yaml.Marshal(d.root)
	if err != nil {
		return err
	}
	if err := os.WriteFile(d.path, data, 0o600); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chmod(d.path, 0o600)
}
