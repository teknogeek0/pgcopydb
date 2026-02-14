package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/theme"
)

type Column struct {
	Title string
	Width int
	Align int // 0=left, 1=right
}

func RenderTable(th *theme.Theme, columns []Column, rows [][]string, cursor int, visibleRows int, sortCol int, width int) string {
	var b strings.Builder

	header := renderRow(th, columns, nil, true, -1, sortCol)
	b.WriteString(header + "\n")

	if len(rows) == 0 {
		b.WriteString(th.DimStyle.Render("  No data"))
		return b.String()
	}

	start := 0
	if cursor >= visibleRows {
		start = cursor - visibleRows + 1
	}
	end := start + visibleRows
	if end > len(rows) {
		end = len(rows)
	}

	for i := start; i < end; i++ {
		isSelected := i == cursor
		row := renderRow(th, columns, rows[i], false, i, -1)
		if isSelected {
			b.WriteString(th.SelectedRowStyle.Width(width).Render(row) + "\n")
		} else if i%2 == 0 {
			b.WriteString(th.TableRowStyle.Render(row) + "\n")
		} else {
			b.WriteString(th.TableRowAltStyle.Render(row) + "\n")
		}
	}

	if len(rows) > visibleRows {
		b.WriteString(th.DimStyle.Render(fmt.Sprintf("  %d/%d rows", cursor+1, len(rows))))
	}

	return b.String()
}

func renderRow(th *theme.Theme, columns []Column, values []string, isHeader bool, rowIdx int, sortCol int) string {
	var parts []string
	for i, col := range columns {
		var val string
		if isHeader {
			val = col.Title
			if i == sortCol {
				val += " ▼"
			}
		} else if i < len(values) {
			val = values[i]
		}

		if len(val) > col.Width {
			val = val[:col.Width-1] + "…"
		}

		var styled string
		if isHeader {
			styled = lipgloss.NewStyle().
				Width(col.Width).
				Bold(true).
				Foreground(th.FgPrimary).
				Render(val)
		} else if col.Align == 1 {
			styled = lipgloss.NewStyle().
				Width(col.Width).
				Align(lipgloss.Right).
				Render(val)
		} else {
			styled = lipgloss.NewStyle().
				Width(col.Width).
				Render(val)
		}
		parts = append(parts, styled)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}
