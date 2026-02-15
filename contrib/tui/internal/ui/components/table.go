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

func RenderTable(th *theme.Theme, columns []Column, rows [][]string, cursor int, visibleRows int, sortCol int, width int, footerInfo string) string {
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
		// Pad row to full width to ensure background styles fill the line
		rowWidth := lipgloss.Width(row)
		if rowWidth < width {
			row += strings.Repeat(" ", width-rowWidth)
		}
		if isSelected {
			b.WriteString(th.SelectedRowStyle.Render(row) + "\n")
		} else if i%2 == 0 {
			b.WriteString(th.TableRowStyle.Render(row) + "\n")
		} else {
			b.WriteString(th.TableRowAltStyle.Render(row) + "\n")
		}
	}

	if len(rows) > visibleRows {
		info := fmt.Sprintf("  %d/%d rows", cursor+1, len(rows))
		if footerInfo != "" {
			info += "  " + footerInfo
		}
		b.WriteString(th.DimStyle.Render(info))
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

		cellWidth := col.Width
		padding := 1
		if i == len(columns)-1 {
			padding = 0
		}

		// Truncate or pad val to exactly cellWidth visual characters.
		// We do this manually instead of using lipgloss Width to avoid
		// its word-wrapping behaviour, which breaks cells containing
		// multi-byte characters followed by spaces (e.g. "└ name").
		visWidth := lipgloss.Width(val)
		if visWidth > cellWidth {
			plain := stripAnsi(val)
			runes := []rune(plain)
			for len(runes) > 0 && lipgloss.Width(string(runes)) >= cellWidth {
				runes = runes[:len(runes)-1]
			}
			val = string(runes) + "…"
			visWidth = lipgloss.Width(val)
		}

		var styled string
		if isHeader {
			pad := ""
			if visWidth < cellWidth {
				pad = strings.Repeat(" ", cellWidth-visWidth)
			}
			styled = lipgloss.NewStyle().
				Bold(true).
				Foreground(th.FgPrimary).
				Render(val+pad) + strings.Repeat(" ", padding)
		} else if col.Align == 1 {
			pad := ""
			if visWidth < cellWidth {
				pad = strings.Repeat(" ", cellWidth-visWidth)
			}
			styled = pad + val + strings.Repeat(" ", padding)
		} else {
			pad := ""
			if visWidth < cellWidth {
				pad = strings.Repeat(" ", cellWidth-visWidth)
			}
			styled = val + pad + strings.Repeat(" ", padding)
		}
		parts = append(parts, styled)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func stripAnsi(s string) string {
	var result []byte
	inEscape := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
				inEscape = false
			}
			continue
		}
		result = append(result, s[i])
	}
	return string(result)
}
