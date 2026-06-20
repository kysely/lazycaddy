package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kysely/lazycaddy/internal/ui"
)

func main() {
	program := tea.NewProgram(ui.New(os.Args, os.Getenv), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "lazycaddy: %v\n", err)
		os.Exit(1)
	}
}
