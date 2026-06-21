package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type ThemeMode string

const (
	ThemeAuto  ThemeMode = "auto"
	ThemeLight ThemeMode = "light"
	ThemeDark  ThemeMode = "dark"
)

// ApplyTheme configures Lip Gloss background detection. By default it leaves
// Lip Gloss in terminal auto-detection mode, but COLORFGBG is used when present
// because it is often the only reliable signal available through tmux.
func ApplyTheme(argv []string, lookupEnv func(string) string, configTheme ...string) {
	mode := ThemeModeFromArgsEnvConfig(argv, lookupEnv, firstConfigTheme(configTheme))
	switch mode {
	case ThemeLight:
		lipgloss.SetHasDarkBackground(false)
	case ThemeDark:
		lipgloss.SetHasDarkBackground(true)
	case ThemeAuto:
		if dark, ok := DarkBackgroundFromColorFGBG(lookupEnv("COLORFGBG")); ok {
			lipgloss.SetHasDarkBackground(dark)
		}
	}
}

func ThemeModeFromArgsEnv(argv []string, lookupEnv func(string) string) ThemeMode {
	return ThemeModeFromArgsEnvConfig(argv, lookupEnv, "")
}

func ThemeModeFromArgsEnvConfig(argv []string, lookupEnv func(string) string, configTheme string) ThemeMode {
	if lookupEnv == nil {
		lookupEnv = func(string) string { return "" }
	}
	if theme := readCLITheme(argv); theme != "" {
		return normalizeThemeMode(theme)
	}
	if theme := lookupEnv("LAZYCADDY_THEME"); theme != "" {
		return normalizeThemeMode(theme)
	}
	if configTheme != "" {
		return normalizeThemeMode(configTheme)
	}
	return ThemeAuto
}

func firstConfigTheme(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func readCLITheme(argv []string) string {
	for index := 1; index < len(argv); index++ {
		arg := argv[index]
		switch {
		case arg == "--theme":
			if index+1 < len(argv) {
				return argv[index+1]
			}
		case strings.HasPrefix(arg, "--theme="):
			return strings.TrimPrefix(arg, "--theme=")
		}
	}
	return ""
}

func normalizeThemeMode(value string) ThemeMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "light":
		return ThemeLight
	case "dark":
		return ThemeDark
	default:
		return ThemeAuto
	}
}

func DarkBackgroundFromColorFGBG(value string) (bool, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, false
	}
	parts := strings.Split(value, ";")
	backgroundText := strings.TrimSpace(parts[len(parts)-1])
	background, err := strconv.Atoi(backgroundText)
	if err != nil {
		return false, false
	}
	background %= 16

	// Conventional ANSI palette: 0-6 and 8 are dark backgrounds; 7 and 9-15
	// are light backgrounds. This is a terminal signal, not an OS theme signal.
	if background == 7 || background >= 9 {
		return false, true
	}
	return true, true
}
