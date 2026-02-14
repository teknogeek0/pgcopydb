package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/theme"
)

func RenderHeader(th *theme.Theme, width int, version, sourceHost, targetHost, runtime string) string {
	clock := time.Now().Format("15:04:05")

	left := th.HeaderStyle.Render("pgcopydb-tui") +
		th.HeaderDimStyle.Render(" │ ") +
		th.HeaderDimStyle.Render(fmt.Sprintf("v%s", version))

	mid := th.HeaderDimStyle.Render("src: ") +
		th.HeaderStyle.Render(truncate(sourceHost, 25)) +
		th.HeaderDimStyle.Render(" → tgt: ") +
		th.HeaderStyle.Render(truncate(targetHost, 25)) +
		th.HeaderDimStyle.Render(" │ ") +
		th.HeaderDimStyle.Render(runtime)

	right := th.HeaderDimStyle.Render(clock)

	leftW := lipgloss.Width(left)
	midW := lipgloss.Width(mid)
	rightW := lipgloss.Width(right)
	totalContent := leftW + midW + rightW

	var header string
	if totalContent+4 > width {
		header = left + "  " + right
	} else {
		gap1 := (width - totalContent) / 2
		gap2 := width - leftW - midW - rightW - gap1
		if gap1 < 2 {
			gap1 = 2
		}
		if gap2 < 2 {
			gap2 = 2
		}
		header = left + strings.Repeat(" ", gap1) + mid + strings.Repeat(" ", gap2) + right
	}

	return lipgloss.NewStyle().
		Background(th.BgSecondary).
		Width(width).
		Render(header)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
