package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
	"gopkg.in/yaml.v3"
)

// The compose file is the source of truth, so the list survives the container
// being rebuilt, which it would not if it only lived in the world folder.
func readPlayerList(compose, service string) (playerList, error) {
	env, err := serviceEnvironment(compose, service)
	if err != nil {
		return playerList{}, err
	}
	list := playerList{
		admins:    splitNames(env["OPS"]),
		whitelist: splitNames(env["WHITELIST"]),
		enforced:  strings.EqualFold(strings.TrimSpace(env["ENFORCE_WHITELIST"]), "true"),
	}
	// A list nobody enforces is not a list, it just sits there.
	if !list.enforced {
		list.whitelist = nil
	}
	return list, nil
}

func writePlayerList(compose, service string, list playerList) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(compose), &doc); err != nil {
		return "", fmt.Errorf("the server's settings are not valid YAML: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return "", fmt.Errorf("the server's settings are empty")
	}
	env, err := environmentNode(doc.Content[0], service)
	if err != nil {
		return "", err
	}

	setOrRemove(env, "OPS", strings.Join(list.admins, ","))
	if list.enforced && len(list.whitelist) > 0 {
		setOrRemove(env, "WHITELIST", strings.Join(list.whitelist, ","))
		setOrRemove(env, "ENFORCE_WHITELIST", "TRUE")
	} else {
		setOrRemove(env, "WHITELIST", "")
		setOrRemove(env, "ENFORCE_WHITELIST", "")
	}

	out, err := yaml.Marshal(doc.Content[0])
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func serviceEnvironment(compose, service string) (map[string]string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(compose), &doc); err != nil {
		return nil, fmt.Errorf("the server's settings are not valid YAML: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("the server's settings are empty")
	}
	env, err := environmentNode(doc.Content[0], service)
	if err != nil {
		return nil, err
	}

	values := map[string]string{}
	for i := 0; i+1 < len(env.Content); i += 2 {
		values[env.Content[i].Value] = env.Content[i+1].Value
	}
	return values, nil
}

func environmentNode(root *yaml.Node, service string) (*yaml.Node, error) {
	services := findMapping(root, "services")
	if services == nil {
		return nil, fmt.Errorf("the server's settings list no services")
	}
	node := findMapping(services, service)
	if node == nil {
		return nil, fmt.Errorf("there is no service called %q in the settings", service)
	}
	env := findMapping(node, "environment")
	if env == nil {
		env = mapping()
		addNode(node, "environment", env)
	}
	return env, nil
}

// An empty value means the key should not be there at all, because compose
// treats an empty string as a value rather than as absent.
func setOrRemove(env *yaml.Node, key, value string) {
	for i := 0; i+1 < len(env.Content); i += 2 {
		if env.Content[i].Value != key {
			continue
		}
		if value == "" {
			env.Content = append(env.Content[:i], env.Content[i+2:]...)
			return
		}
		env.Content[i+1] = quotedScalar(value)
		return
	}
	if value != "" {
		addNode(env, key, quotedScalar(value))
	}
}

func quotedScalar(value string) *yaml.Node {
	node := scalar(value)
	node.Style = yaml.DoubleQuotedStyle
	return node
}

func splitNames(value string) []string {
	names := []string{}
	for _, name := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names
}

// Ten seconds and a way out, because somebody may be mid game and the person at
// the keyboard is not necessarily the one playing.
func countdownBeforeRestart(p *ui.Prompter) bool {
	fmt.Println("")
	ui.PrintWarning("Server will shut down in 10s for the changes to take effect.")
	ui.PrintHint("Press Ctrl+C to abort.")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	defer signal.Stop(stop)

	for remaining := 10; remaining > 0; remaining-- {
		fmt.Printf(" %d", remaining)
		select {
		case <-stop:
			fmt.Println("\nAborted.")
			return false
		case <-time.After(time.Second):
		}
	}
	fmt.Println("")
	return true
}

// Version and kind live in the same environment block the players do.
func setWorldEnvironment(compose, service, serverType, version string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(compose), &doc); err != nil {
		return "", fmt.Errorf("the server's settings are not valid YAML: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return "", fmt.Errorf("the server's settings are empty")
	}
	env, err := environmentNode(doc.Content[0], service)
	if err != nil {
		return "", err
	}

	setOrRemove(env, "TYPE", strings.ToUpper(strings.TrimSpace(serverType)))
	setOrRemove(env, "VERSION", strings.TrimSpace(version))

	out, err := yaml.Marshal(doc.Content[0])
	if err != nil {
		return "", err
	}
	return string(out), nil
}
