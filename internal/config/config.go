package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is lazycaddy's user preferences file. Caddy's running Admin API config
// remains the source of truth for services, routes, logs, and upstreams.
type Config struct {
	AdminURL string `yaml:"admin_url"`
	Theme    string `yaml:"theme"`
}

// LoadResult describes the config file that was used, if any.
type LoadResult struct {
	Config Config
	Path   string
	Found  bool
}

// Load reads lazycaddy config using this precedence for config file location:
// --config, LAZYCADDY_CONFIG, $XDG_CONFIG_HOME/lazycaddy/config.yml,
// ~/.config/lazycaddy/config.yml. Missing default files are ignored; missing
// explicit files are errors.
func Load(argv []string, lookupEnv func(string) string) (LoadResult, error) {
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}

	path, explicit := configPath(argv, lookupEnv)
	if path == "" {
		return LoadResult{}, nil
	}
	path = expandHome(path, lookupEnv)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !explicit {
			return LoadResult{Path: path}, nil
		}
		return LoadResult{}, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return LoadResult{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.AdminURL = strings.TrimSpace(cfg.AdminURL)
	cfg.Theme = normalizeTheme(cfg.Theme)
	if cfg.Theme != "" && cfg.Theme != "auto" && cfg.Theme != "light" && cfg.Theme != "dark" {
		return LoadResult{}, fmt.Errorf("parse config %s: theme must be auto, light, or dark", path)
	}

	return LoadResult{Config: cfg, Path: path, Found: true}, nil
}

func configPath(argv []string, lookupEnv func(string) string) (string, bool) {
	if path := readCLIConfigPath(argv); path != "" {
		return path, true
	}
	if path := strings.TrimSpace(lookupEnv("LAZYCADDY_CONFIG")); path != "" {
		return path, true
	}
	if xdg := strings.TrimSpace(lookupEnv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "lazycaddy", "config.yml"), false
	}
	if home := strings.TrimSpace(lookupEnv("HOME")); home != "" {
		return filepath.Join(home, ".config", "lazycaddy", "config.yml"), false
	}
	return "", false
}

func readCLIConfigPath(argv []string) string {
	for index := 1; index < len(argv); index++ {
		arg := argv[index]
		switch {
		case arg == "--config":
			if index+1 < len(argv) {
				return strings.TrimSpace(argv[index+1])
			}
		case strings.HasPrefix(arg, "--config="):
			return strings.TrimSpace(strings.TrimPrefix(arg, "--config="))
		}
	}
	return ""
}

func expandHome(path string, lookupEnv func(string) string) string {
	if path == "~" {
		if home := strings.TrimSpace(lookupEnv("HOME")); home != "" {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home := strings.TrimSpace(lookupEnv("HOME")); home != "" {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func normalizeTheme(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
