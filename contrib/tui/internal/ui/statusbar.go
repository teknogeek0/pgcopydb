package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/theme"
)

func RenderStatusBar(th *theme.Theme, width int, connected []string, lastErr error) string {
	left := th.DimStyle.Render(" tab/1-5:switch  j/k:scroll  s:sort  /:filter  ?:help  q:quit")

	var right string
	if lastErr != nil {
		right = th.ErrorStyle.Render(fmt.Sprintf("ERR: %v ", lastErr))
	} else if len(connected) > 0 {
		right = th.ConnectedStyle.Render(fmt.Sprintf("● %s ", strings.Join(connected, ", ")))
	}

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := width - leftW - rightW
	if gap < 0 {
		gap = 0
	}

	bar := left + strings.Repeat(" ", gap) + right
	return th.StatusBarStyle.Width(width).Render(bar)
}
