package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/theme"
)

func RenderHeader(th *theme.Theme, width int, runtime string) string {
	clock := time.Now().Format("15:04:05")

	// Build header text without per-segment padding to avoid width overflow
	titleStyle := lipgloss.NewStyle().
		Foreground(th.HeaderStyle.GetForeground()).
		Bold(true)
	dimStyle := lipgloss.NewStyle().
		Foreground(th.HeaderDimStyle.GetForeground())

	left := " " + titleStyle.Render("pgcopydb Migration Monitor") +
		dimStyle.Render(" — "+clock)

	right := dimStyle.Render(fmt.Sprintf("Runtime: %s ", runtime))

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := width - leftW - rightW
	if gap < 1 {
		gap = 1
	}

	header := left + strings.Repeat(" ", gap) + right

	return lipgloss.NewStyle().
		Background(th.BgSecondary).
		Width(width).
		Render(header)
}
