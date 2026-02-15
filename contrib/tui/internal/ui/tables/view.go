package tables

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/metrics"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/theme"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/ui/components"
)

type Data struct {
	Tables    []metrics.CatalogTable
	Summaries []metrics.CatalogSummaryEntry
	Processes []metrics.CatalogProcess
	Cursor    int
	SortCol   int
}

type tableRow struct {
	name      string
	totalSize int64
	copied    int64
	pct       float64
	speed     float64
	eta       string
	status    string
}

func Render(th *theme.Theme, width, height int, data Data) string {
	var b strings.Builder
	contentWidth := width - 4
	if contentWidth < 40 {
		contentWidth = 40
	}

	b.WriteString(th.SectionTitleStyle.Render("  Per-Table Progress") + "\n")

	// Build summary lookup
	summaryByOID := buildSummaryMap(data.Summaries)
	activeOIDs := buildActiveOIDs(data.Processes)

	var rows []tableRow
	for _, t := range data.Tables {
		if t.ExcludeData {
			continue
		}
		row := tableRow{
			name:      t.QName,
			totalSize: t.Bytes,
		}

		if s, ok := summaryByOID[t.OID]; ok {
			row.copied = s.Bytes
			// duration is in milliseconds
			durationSec := float64(s.Duration) / 1000.0
			if durationSec > 0 {
				row.speed = float64(s.Bytes) / durationSec
			}

			if s.DoneTimeEpoch > 0 {
				row.status = "done"
				row.pct = 100.0
				row.eta = formatDuration(time.Duration(s.Duration) * time.Millisecond)
			} else if s.StartTimeEpoch > 0 {
				row.status = "copying"
				if t.Bytes > 0 {
					row.pct = float64(s.Bytes) / float64(t.Bytes) * 100
					if row.pct > 100 {
						row.pct = 99.9
					}
				}
				remaining := t.Bytes - s.Bytes
				if row.speed > 0 && remaining > 0 {
					eta := time.Duration(float64(remaining)/row.speed) * time.Second
					row.eta = formatDuration(eta)
				}
			}
		}

		if _, active := activeOIDs[t.OID]; active && row.status == "" {
			row.status = "copying"
		}
		if row.status == "" {
			row.status = "pending"
		}

		rows = append(rows, row)
	}

	// Sort
	sortRows(rows, data.SortCol)

	// Render table
	cols := []components.Column{
		{Title: "Table", Width: 30},
		{Title: "Size", Width: 10, Align: 1},
		{Title: "Copied", Width: 10, Align: 1},
		{Title: "%", Width: 7, Align: 1},
		{Title: "Speed", Width: 12, Align: 1},
		{Title: "ETA", Width: 10, Align: 1},
		{Title: "Status", Width: 10},
	}

	var tableRows [][]string
	for _, r := range rows {
		statusStyled := r.status
		switch r.status {
		case "done":
			statusStyled = th.GreenStyle.Render(r.status)
		case "copying":
			statusStyled = th.CyanStyle.Render(r.status)
		case "pending":
			statusStyled = th.DimStyle.Render(r.status)
		}

		tableRows = append(tableRows, []string{
			r.name,
			metrics.FormatBytes(uint64(r.totalSize)),
			metrics.FormatBytes(uint64(r.copied)),
			fmt.Sprintf("%.1f", r.pct),
			formatSpeed(r.speed),
			r.eta,
			statusStyled,
		})
	}

	visibleRows := height - 4
	if visibleRows < 5 {
		visibleRows = 5
	}

	b.WriteString(components.RenderTable(th, cols, tableRows, data.Cursor, visibleRows, data.SortCol, contentWidth, ""))

	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}

func buildSummaryMap(summaries []metrics.CatalogSummaryEntry) map[int64]metrics.CatalogSummaryEntry {
	m := make(map[int64]metrics.CatalogSummaryEntry)
	for _, s := range summaries {
		if s.TableOID > 0 && strings.HasPrefix(s.Command, "COPY") {
			existing, ok := m[s.TableOID]
			if !ok || s.Bytes > existing.Bytes {
				m[s.TableOID] = s
			}
		}
	}
	return m
}

func buildActiveOIDs(processes []metrics.CatalogProcess) map[int64]bool {
	m := make(map[int64]bool)
	for _, p := range processes {
		if p.TableOID > 0 {
			m[p.TableOID] = true
		}
	}
	return m
}

func sortRows(rows []tableRow, sortCol int) {
	sort.Slice(rows, func(i, j int) bool {
		switch sortCol {
		case 1: // Size
			return rows[i].totalSize > rows[j].totalSize
		case 2: // Copied
			return rows[i].copied > rows[j].copied
		case 3: // Percent
			return rows[i].pct > rows[j].pct
		case 4: // Speed
			return rows[i].speed > rows[j].speed
		case 5: // Status
			return statusOrder(rows[i].status) < statusOrder(rows[j].status)
		default: // Name
			return rows[i].name < rows[j].name
		}
	})
}

func statusOrder(s string) int {
	switch s {
	case "copying":
		return 0
	case "pending":
		return 1
	case "done":
		return 2
	default:
		return 3
	}
}

func formatSpeed(bytesPerSec float64) string {
	if bytesPerSec <= 0 {
		return "-"
	}
	return metrics.FormatBytesRate(bytesPerSec)
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
