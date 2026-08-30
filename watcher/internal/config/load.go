package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"gopkg.in/yaml.v3"
)

func configSearchPaths() []string {
	paths := []string{}
	if env := RenamedEnv("MCWOD_CONFIG"); env != "" {
		paths = append(paths, env)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		paths = append(paths,
			filepath.Join(filepath.Dir(dir), "config.yml"),
			filepath.Join(dir, "config.yml"),
		)
	}
	if cwd, err := os.Getwd(); err == nil {
		paths = append(paths,
			filepath.Join(filepath.Dir(cwd), "config.yml"),
			filepath.Join(cwd, "config.yml"),
		)
	}
	return dedupe(paths)
}

func Load() (*Config, error) {
	searched := configSearchPaths()
	for _, p := range searched {
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("cannot read %s: %w", p, err)
		}
		cfg := Default()
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("%s is not valid YAML: %w", p, err)
		}
		cfg.Path = p
		applyEnvOverrides(&cfg)
		logging.Infof("Loading config from %s", p)
		if err := cfg.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		return &cfg, nil
	}
	return nil, fmt.Errorf(
		"no config.yml found, searched: %s\ncreate one with: cp config.example.yml config.yml",
		strings.Join(searched, ", "),
	)
}

func dedupe(list []string) []string {
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, v := range list {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
