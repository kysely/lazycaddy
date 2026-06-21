package ui

import "testing"

func TestThemeModeFromArgsEnv(t *testing.T) {
	env := func(key string) string {
		if key == "LAZYCADDY_THEME" {
			return "dark"
		}
		return ""
	}
	if got := ThemeModeFromArgsEnv([]string{"lazycaddy"}, env); got != ThemeDark {
		t.Fatalf("env theme = %s, want dark", got)
	}
	if got := ThemeModeFromArgsEnv([]string{"lazycaddy", "--theme", "light"}, env); got != ThemeLight {
		t.Fatalf("cli theme = %s, want light", got)
	}
	if got := ThemeModeFromArgsEnv([]string{"lazycaddy", "--theme=dark"}, nil); got != ThemeDark {
		t.Fatalf("cli equals theme = %s, want dark", got)
	}
	if got := ThemeModeFromArgsEnv([]string{"lazycaddy", "--theme=banana"}, nil); got != ThemeAuto {
		t.Fatalf("invalid theme = %s, want auto", got)
	}
}

func TestThemeModeFromArgsEnvConfig(t *testing.T) {
	env := func(key string) string {
		if key == "LAZYCADDY_THEME" {
			return "dark"
		}
		return ""
	}
	if got := ThemeModeFromArgsEnvConfig([]string{"lazycaddy"}, nil, "light"); got != ThemeLight {
		t.Fatalf("config theme = %s, want light", got)
	}
	if got := ThemeModeFromArgsEnvConfig([]string{"lazycaddy"}, env, "light"); got != ThemeDark {
		t.Fatalf("env should beat config, got %s", got)
	}
	if got := ThemeModeFromArgsEnvConfig([]string{"lazycaddy", "--theme", "auto"}, env, "light"); got != ThemeAuto {
		t.Fatalf("cli should beat env/config, got %s", got)
	}
}

func TestDarkBackgroundFromColorFGBG(t *testing.T) {
	tests := []struct {
		value string
		dark  bool
		ok    bool
	}{
		{value: "15;0", dark: true, ok: true},
		{value: "0;15", dark: false, ok: true},
		{value: "0;7", dark: false, ok: true},
		{value: "0;8", dark: true, ok: true},
		{value: "", ok: false},
		{value: "bad", ok: false},
	}
	for _, tt := range tests {
		dark, ok := DarkBackgroundFromColorFGBG(tt.value)
		if dark != tt.dark || ok != tt.ok {
			t.Fatalf("DarkBackgroundFromColorFGBG(%q) = %v, %v; want %v, %v", tt.value, dark, ok, tt.dark, tt.ok)
		}
	}
}
