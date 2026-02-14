package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/theme"
)

func RenderGauge(th *theme.Theme, label string, percent float64, width int) string {
	labelStr := lipgloss.NewStyle().
		Foreground(th.GaugeLabelColor).
		Width(6).
		Render(label)

	pctStr := fmt.Sprintf("%5.1f%%", percent)
	pctWidth := len(pctStr) + 1

	barWidth := width - 6 - pctWidth - 4
	if barWidth < 10 {
		barWidth = 10
	}

	filled := int(float64(barWidth) * percent / 100)
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}
	empty := barWidth - filled

	barColor := th.GaugeColor(percent)

	bar := lipgloss.NewStyle().Foreground(barColor).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(th.GaugeEmptyColor).Render(strings.Repeat("░", empty))

	pctStyled := lipgloss.NewStyle().Foreground(barColor).Render(pctStr)

	return fmt.Sprintf("%s [%s] %s", labelStr, bar, pctStyled)
}

// RenderProgressBar renders a simple progress bar with a label.
func RenderProgressBar(th *theme.Theme, label string, current, total int64, width int) string {
	var percent float64
	if total > 0 {
		percent = float64(current) / float64(total) * 100
	}
	return RenderGauge(th, label, percent, width)
}
