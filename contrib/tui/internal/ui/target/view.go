package target

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/metrics"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/theme"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/ui/components"
)

type Data struct {
	Version       string
	Uptime        string
	Databases     []metrics.PGDatabaseStat
	Conns         *metrics.PGConnectionSummary
	Activity      []metrics.PGActivityRow
	TpsDelta      *metrics.DeltaCalculator
	CatalogTables []metrics.CatalogTable
	Summaries     []metrics.CatalogSummaryEntry
}

func Render(th *theme.Theme, width, height int, data Data) string {
	var b strings.Builder
	contentWidth := width - 4
	if contentWidth < 40 {
		contentWidth = 40
	}

	// Server info
	b.WriteString(th.SectionTitleStyle.Render("  Target Database") + "\n")
	if data.Version != "" {
		b.WriteString(fmt.Sprintf("  %s %s\n", th.DimStyle.Render("Version:"), truncateVersion(data.Version)))
	}
	if data.Uptime != "" {
		b.WriteString(fmt.Sprintf("  %s %s\n", th.DimStyle.Render("Uptime:"), formatUptime(data.Uptime)))
	}

	// Tables populated vs total
	totalTables := len(data.CatalogTables)
	populatedTables := countPopulatedTables(data.Summaries)
	b.WriteString(fmt.Sprintf("  %s %d / %d\n",
		th.DimStyle.Render("Tables populated:"),
		populatedTables, totalTables,
	))
	if totalTables > 0 {
		pct := float64(populatedTables) / float64(totalTables) * 100
		b.WriteString(components.RenderGauge(th, "Tbl", pct, contentWidth) + "\n")
	}

	// Database sizes + stats
	if len(data.Databases) > 0 {
		b.WriteString("\n")
		cols := []components.Column{
			{Title: "Database", Width: 20},
			{Title: "Size", Width: 12, Align: 1},
			{Title: "Active", Width: 8, Align: 1},
			{Title: "Total", Width: 8, Align: 1},
			{Title: "TPS", Width: 10, Align: 1},
			{Title: "Cache%", Width: 8, Align: 1},
		}
		var rows [][]string
		for _, db := range data.Databases {
			tps := float64(0)
			if data.TpsDelta != nil {
				tps = data.TpsDelta.Rate("tgt_tps_"+db.DatName, db.TotalXacts)
			}
			rows = append(rows, []string{
				db.DatName,
				metrics.FormatBytes(uint64(db.SizeBytes)),
				fmt.Sprintf("%d", db.ActiveConns),
				fmt.Sprintf("%d", db.TotalConns),
				fmt.Sprintf("%.1f", tps),
				fmt.Sprintf("%.1f", db.CacheHitRatio),
			})
		}
		b.WriteString(components.RenderTable(th, cols, rows, -1, len(rows), -1, contentWidth))
		b.WriteString("\n")
	}

	// Connections summary
	if data.Conns != nil {
		b.WriteString("\n" + th.SectionTitleStyle.Render("  Connections") + "\n")
		b.WriteString(fmt.Sprintf("  Active: %s  Idle: %s  IdleTx: %s  Waiting: %s  Total: %s\n",
			th.GreenStyle.Render(fmt.Sprintf("%d", data.Conns.Active)),
			th.DimStyle.Render(fmt.Sprintf("%d", data.Conns.Idle)),
			th.YellowStyle.Render(fmt.Sprintf("%d", data.Conns.IdleInTransaction)),
			th.RedStyle.Render(fmt.Sprintf("%d", data.Conns.Waiting)),
			fmt.Sprintf("%d", data.Conns.Total),
		))
	}

	// Cache hit ratio summary
	if len(data.Databases) > 0 {
		var totalHitRatio float64
		for _, db := range data.Databases {
			totalHitRatio += db.CacheHitRatio
		}
		avgHitRatio := totalHitRatio / float64(len(data.Databases))
		b.WriteString(fmt.Sprintf("\n  %s %s\n",
			th.DimStyle.Render("Avg Cache Hit Ratio:"),
			colorizeHitRatio(th, avgHitRatio),
		))
	}

	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}

func countPopulatedTables(summaries []metrics.CatalogSummaryEntry) int {
	done := make(map[int64]bool)
	for _, s := range summaries {
		if s.TableOID > 0 && s.DoneTimeEpoch > 0 && s.Command == "COPY" {
			done[s.TableOID] = true
		}
	}
	return len(done)
}

func colorizeHitRatio(th *theme.Theme, ratio float64) string {
	s := fmt.Sprintf("%.1f%%", ratio)
	switch {
	case ratio >= 99:
		return th.GreenStyle.Render(s)
	case ratio >= 90:
		return th.YellowStyle.Render(s)
	default:
		return th.RedStyle.Render(s)
	}
}

func truncateVersion(v string) string {
	if len(v) > 60 {
		return v[:57] + "..."
	}
	return v
}

func formatUptime(raw string) string {
	if raw == "" {
		return "..."
	}
	if idx := strings.Index(raw, "."); idx != -1 {
		raw = raw[:idx]
	}
	return raw
}
