package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kysely/lazycaddy/internal/ui"
)

var version = "dev"

func main() {
	if handleMetaCommand(os.Args) {
		return
	}

	ui.ApplyTheme(os.Args, os.Getenv)
	program := tea.NewProgram(ui.New(os.Args, os.Getenv), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "lazycaddy: %v\n", err)
		os.Exit(1)
	}
}

func handleMetaCommand(argv []string) bool {
	for _, arg := range argv[1:] {
		switch arg {
		case "--version", "version", "-v":
			fmt.Printf("lazycaddy %s\n", version)
			return true
		case "--help", "help", "-h":
			fmt.Print(helpText())
			return true
		}
	}
	return false
}

func helpText() string {
	return `lazycaddy - inspect and troubleshoot the local Caddy instance

Usage:
  lazycaddy [flags]

Flags:
  --admin-url, --admin <url>  Override Caddy Admin API URL
  --theme <auto|light|dark>   Override terminal theme detection
  --version, -v               Print version
  --help, -h                  Show help

Keys inside the TUI:
  ? help  S system  r refresh  q quit
`
}
