package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The RCON password lives in .env under our own name, never as RCON_PASSWORD.
// A foreign .env may already define that one, and the last definition wins.
const rconPasswordVar = "MCWOD_RCON_PASSWORD"

const composeBackupPrefix = "docker-compose.yml.mcwod-bak-"

// Pinned, so the same compose file keeps producing the same server. Bump these
// together with server/docker-compose.yml, a test holds them to it.
const (
	// No Java suffix: the image picks the runtime the configured Minecraft
	// version needs, and 26.2 already needs a newer one than 21.
	minecraftImage = "itzg/minecraft-server:2026.8.0"
	backupImage    = "itzg/mc-backup:2026.8.0"
)

// What itzg/minecraft-server understands as TYPE. Vanilla first, it is what
// somebody who does not know the difference wants.
var serverTypes = []string{"VANILLA", "PAPER", "PURPUR", "FABRIC", "FORGE", "NEOFORGE", "QUILT", "SPIGOT"}

// The four worth offering, in the order somebody would work through them.
var serverTypeChoices = []struct{ name, what string }{
	{"VANILLA", "normal Minecraft, nothing added"},
	{"PAPER", "same game, runs smoother, can use plugins"},
	{"FABRIC", "for mods, every player needs the same mods"},
	{"FORGE", "for mods, every player needs the same mods"},
}

// What the two services are built from, asked once and then just data.
type ComposeSpec struct {
	ServiceName    string
	BackupName     string
	ServerType     string
	MCVersion      string
	Memory         string
	MCPort         int
	DataDir        string
	Admin          string
	Whitelist      []string
	BackupInterval string
	KeepBackupDays int
	Backups        bool
}

func defaultComposeSpec(containerName string, mcPort int) ComposeSpec {
	return ComposeSpec{
		ServiceName:    containerName,
		BackupName:     containerName + "-backup",
		ServerType:     "VANILLA",
		MCVersion:      "1.21.4",
		Memory:         "4G",
		MCPort:         mcPort,
		DataDir:        "./data",
		BackupInterval: "24h",
		KeepBackupDays: 7,
		Backups:        true,
	}
}

// Only the game port is published, RCON stays on the internal docker network.
func minecraftService(spec ComposeSpec) *yaml.Node {
	env := []string{
		"EULA", "TRUE",
		"ENABLE_RCON", "TRUE",
		"RCON_PASSWORD", "${" + rconPasswordVar + ":?set " + rconPasswordVar + " in .env}",
		"TYPE", spec.ServerType,
		"VERSION", spec.MCVersion,
		"MEMORY", spec.Memory,
		"AUTOPAUSE", "TRUE",
		"AUTOPAUSE_TIMEOUT_EST", "3600",
		"AUTOPAUSE_TIMEOUT_INIT", "600",
		"ONLINE_MODE", "TRUE",
		// Unsigned chat, so nothing a player types is held back or reported.
		// Accounts are still verified, ONLINE_MODE above does that.
		"ENFORCE_SECURE_PROFILE", "FALSE",
		// Set even when the watcher proxies today, so switching to transfer
		// mode later never means editing anything on the server PC again.
		"ACCEPTS_TRANSFERS", "TRUE",
	}
	if spec.Admin != "" {
		env = append(env, "OPS", spec.Admin)
	}
	// An enforced but empty whitelist locks everyone out, the owner included.
	if len(spec.Whitelist) > 0 {
		env = append(env,
			"WHITELIST", strings.Join(spec.Whitelist, ","),
			"ENFORCE_WHITELIST", "TRUE")
	}

	service := mapping()
	addScalar(service, "image", minecraftImage)
	addScalar(service, "container_name", spec.ServiceName)
	// The watcher starts it, so docker must not race it on boot.
	addScalar(service, "restart", "no")
	addBool(service, "tty", true)
	addBool(service, "stdin_open", true)
	addSequence(service, "ports", []string{strconv.Itoa(spec.MCPort) + ":25565"})
	addMapping(service, "environment", env)
	addSequence(service, "volumes", []string{spec.DataDir + ":/data"})
	return service
}

func backupService(spec ComposeSpec) *yaml.Node {
	env := []string{
		"RCON_HOST", spec.ServiceName,
		"RCON_PASSWORD", "${" + rconPasswordVar + ":?set " + rconPasswordVar + " in .env}",
		"BACKUP_INTERVAL", spec.BackupInterval,
		"PRUNE_BACKUPS_DAYS", strconv.Itoa(spec.KeepBackupDays),
		"INITIAL_DELAY", "120",
	}

	service := mapping()
	addScalar(service, "image", backupImage)
	addScalar(service, "container_name", spec.BackupName)
	addScalar(service, "restart", "no")
	addSequence(service, "depends_on", []string{spec.ServiceName})
	addMapping(service, "environment", env)
	addSequence(service, "volumes", []string{spec.DataDir + ":/data:ro", "./backups:/backups"})
	return service
}

// A whole file for a server that has none yet.
func newComposeFile(spec ComposeSpec) (string, error) {
	services := mapping()
	addNode(services, spec.ServiceName, minecraftService(spec))
	if spec.Backups {
		addNode(services, spec.BackupName, backupService(spec))
	}

	root := mapping()
	addNode(root, "services", services)
	root.Content[0].HeadComment = "Written by mcwod. " +
		"The RCON password lives in .env next to this file."

	data, err := yaml.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Adds the services to a file that already has some, leaving every other
// service and every comment exactly where it was.
func addServicesToCompose(existing string, spec ComposeSpec) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(existing), &doc); err != nil {
		return "", fmt.Errorf("the existing docker-compose.yml is not valid YAML: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return "", fmt.Errorf("the existing docker-compose.yml is empty")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", fmt.Errorf("the existing docker-compose.yml is not a mapping")
	}

	services := findMapping(root, "services")
	if services == nil {
		services = mapping()
		addNode(root, "services", services)
	}

	wanted := []string{spec.ServiceName}
	if spec.Backups {
		wanted = append(wanted, spec.BackupName)
	}
	for _, name := range wanted {
		if findMapping(services, name) != nil {
			return "", fmt.Errorf("the file already has a service called %q, refusing to touch it", name)
		}
	}

	addNode(services, spec.ServiceName, minecraftService(spec))
	if spec.Backups {
		addNode(services, spec.BackupName, backupService(spec))
	}

	data, err := yaml.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Three lines onto whatever is already there, so a foreign .env keeps working.
func appendRCONPassword(existing, password string) string {
	var b strings.Builder
	if trimmed := strings.TrimRight(existing, "\n"); trimmed != "" {
		b.WriteString(trimmed)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString("# RCON password, added by Minecraft Wake-on-Demand\n")
	b.WriteString(rconPasswordVar + "=" + password + "\n")
	return b.String()
}

func hasRCONPasswordVar(env string) bool {
	for _, line := range strings.Split(env, "\n") {
		key, _, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && strings.TrimSpace(key) == rconPasswordVar {
			return true
		}
	}
	return false
}

// URL safe so it cannot need quoting in .env or in a shell.
func generateRCONPassword() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func mapping() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func scalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func addNode(parent *yaml.Node, key string, value *yaml.Node) {
	parent.Content = append(parent.Content, scalar(key), value)
}

func addScalar(parent *yaml.Node, key, value string) {
	node := scalar(value)
	node.Style = yaml.DoubleQuotedStyle
	addNode(parent, key, node)
}

func addBool(parent *yaml.Node, key string, value bool) {
	addNode(parent, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(value)})
}

func addSequence(parent *yaml.Node, key string, values []string) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range values {
		node := scalar(value)
		node.Style = yaml.DoubleQuotedStyle
		seq.Content = append(seq.Content, node)
	}
	addNode(parent, key, seq)
}

// Flat key, value pairs, which is what compose wants for environment.
func addMapping(parent *yaml.Node, key string, pairs []string) {
	node := mapping()
	for i := 0; i+1 < len(pairs); i += 2 {
		addScalar(node, pairs[i], pairs[i+1])
	}
	addNode(parent, key, node)
}

func findMapping(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key && parent.Content[i+1].Kind == yaml.MappingNode {
			return parent.Content[i+1]
		}
	}
	return nil
}
