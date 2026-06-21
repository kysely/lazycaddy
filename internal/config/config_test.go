package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lazycaddy", "config.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("admin_url: localhost:2020\ntheme: dark\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Load([]string{"lazycaddy"}, env(map[string]string{"XDG_CONFIG_HOME": dir}))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Found || result.Path != path {
		t.Fatalf("result=%+v, want found path %s", result, path)
	}
	if result.Config.AdminURL != "localhost:2020" || result.Config.Theme != "dark" {
		t.Fatalf("config=%+v", result.Config)
	}
}

func TestLoadMissingDefaultConfigIsIgnored(t *testing.T) {
	result, err := Load([]string{"lazycaddy"}, env(map[string]string{"XDG_CONFIG_HOME": t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Found {
		t.Fatalf("missing default config should not be found: %+v", result)
	}
}

func TestLoadMissingExplicitConfigIsError(t *testing.T) {
	_, err := Load([]string{"lazycaddy", "--config", filepath.Join(t.TempDir(), "missing.yml")}, env(nil))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadRejectsInvalidTheme(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte("theme: banana\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load([]string{"lazycaddy", "--config", path}, env(nil))
	if err == nil {
		t.Fatal("expected invalid theme error")
	}
}

func env(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
