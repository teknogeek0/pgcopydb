package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/theme"
)

func RenderTabs(th *theme.Theme, width int, tabs []string, active int) string {
	var rendered []string
	for i, t := range tabs {
		if i == active {
			rendered = append(rendered, th.ActiveTabStyle.Render(t))
		} else {
			rendered = append(rendered, th.InactiveTabStyle.Render(t))
		}
	}

	row := lipgloss.JoinHorizontal(lipgloss.Top, rendered...)

	rowWidth := lipgloss.Width(row)
	if rowWidth < width {
		fill := th.TabBarStyle.Width(width - rowWidth).Render(strings.Repeat(" ", width-rowWidth))
		row = lipgloss.JoinHorizontal(lipgloss.Top, row, fill)
	}

	return row
}
