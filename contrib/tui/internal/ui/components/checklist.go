package components

import (
	"fmt"
	"strings"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/theme"
)

type CheckItem struct {
	Label    string
	Status   CheckStatus
	Detail   string
	Duration string
}

type CheckStatus int

const (
	StatusPending CheckStatus = iota
	StatusActive
	StatusDone
)

func RenderChecklist(th *theme.Theme, items []CheckItem) string {
	var b strings.Builder

	for _, item := range items {
		var icon, line string
		switch item.Status {
		case StatusDone:
			icon = th.CheckDoneStyle.Render("✓")
			line = th.CheckDoneStyle.Render(item.Label)
		case StatusActive:
			icon = th.CheckActiveStyle.Render("●")
			line = th.CheckActiveStyle.Render(item.Label)
		case StatusPending:
			icon = th.CheckPendingStyle.Render("○")
			line = th.CheckPendingStyle.Render(item.Label)
		}

		row := fmt.Sprintf("  %s %s", icon, line)
		if item.Duration != "" {
			row += th.DimStyle.Render(fmt.Sprintf(" (%s)", item.Duration))
		}
		if item.Detail != "" {
			row += th.DimStyle.Render(fmt.Sprintf(" — %s", item.Detail))
		}
		b.WriteString(row + "\n")
	}

	return b.String()
}
