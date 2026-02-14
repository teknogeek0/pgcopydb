package source

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/metrics"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/theme"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/ui/components"
)

type Data struct {
	Version   string
	Uptime    string
	Databases []metrics.PGDatabaseStat
	Conns     *metrics.PGConnectionSummary
	Slots     []metrics.PGReplicationSlot
	ReplStats []metrics.PGReplicationStat
	WALLSN    string
	Activity  []metrics.PGActivityRow
	TpsDelta  *metrics.DeltaCalculator
	WalDelta  *metrics.DeltaCalculator
	Cursor    int
}

func Render(th *theme.Theme, width, height int, data Data) string {
	var b strings.Builder
	contentWidth := width - 4
	if contentWidth < 40 {
		contentWidth = 40
	}

	// Server info
	b.WriteString(th.SectionTitleStyle.Render("  Source Database") + "\n")
	if data.Version != "" {
		b.WriteString(fmt.Sprintf("  %s %s\n", th.DimStyle.Render("Version:"), truncateVersion(data.Version)))
	}
	if data.Uptime != "" {
		b.WriteString(fmt.Sprintf("  %s %s\n", th.DimStyle.Render("Uptime:"), formatUptime(data.Uptime)))
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
				tps = data.TpsDelta.Rate("src_tps_"+db.DatName, db.TotalXacts)
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

	// WAL position
	if data.WALLSN != "" {
		b.WriteString("\n" + th.SectionTitleStyle.Render("  WAL") + "\n")
		b.WriteString(fmt.Sprintf("  %s %s\n",
			th.DimStyle.Render("Current LSN:"),
			th.BrightStyle.Render(data.WALLSN),
		))
	}

	// Replication slots with retention
	if len(data.Slots) > 0 {
		b.WriteString("\n" + th.SectionTitleStyle.Render("  Replication Slots") + "\n")
		for _, slot := range data.Slots {
			activeStr := th.RedStyle.Render("inactive")
			if slot.Active {
				activeStr = th.GreenStyle.Render("active")
			}

			retention := metrics.FormatBytes(uint64(slot.RetainedBytes))

			// Compute WAL retention time estimate
			walRate := float64(0)
			if data.WalDelta != nil {
				// Get the rate from the global WAL delta tracker
				walRate = data.WalDelta.RateFloat64("wal_rate_estimate", 0)
			}
			timeEst := metrics.WALRetentionTime(slot.RetainedBytes, walRate)

			walStatus := slot.WALStatus
			switch walStatus {
			case "reserved":
				walStatus = th.GreenStyle.Render(walStatus)
			case "extended":
				walStatus = th.YellowStyle.Render(walStatus)
			case "unreserved", "lost":
				walStatus = th.RedStyle.Render(walStatus)
			}

			b.WriteString(fmt.Sprintf("  %s  %s  %s  retained: %s (~%s)  wal: %s\n",
				th.BrightStyle.Render(slot.Name),
				slot.SlotType,
				activeStr,
				th.BrightStyle.Render(retention),
				timeEst,
				walStatus,
			))
		}
	}

	// Replication stats
	if len(data.ReplStats) > 0 {
		b.WriteString("\n" + th.SectionTitleStyle.Render("  Replication Lag") + "\n")
		cols := []components.Column{
			{Title: "App", Width: 20},
			{Title: "State", Width: 12},
			{Title: "Write Lag", Width: 14},
			{Title: "Flush Lag", Width: 14},
			{Title: "Replay Lag", Width: 14},
		}
		var rows [][]string
		for _, r := range data.ReplStats {
			rows = append(rows, []string{
				r.ApplicationName,
				r.State,
				r.WriteLag,
				r.FlushLag,
				r.ReplayLag,
			})
		}
		b.WriteString(components.RenderTable(th, cols, rows, -1, len(rows), -1, contentWidth))
		b.WriteString("\n")
	}

	// Active queries (long-running)
	longQueries := filterLongQueries(data.Activity)
	if len(longQueries) > 0 {
		b.WriteString("\n" + th.SectionTitleStyle.Render("  Long Running Queries (>5s)") + "\n")
		cols := []components.Column{
			{Title: "PID", Width: 8, Align: 1},
			{Title: "Database", Width: 14},
			{Title: "User", Width: 12},
			{Title: "Duration", Width: 10, Align: 1},
			{Title: "State", Width: 12},
			{Title: "Query", Width: contentWidth - 60},
		}
		var rows [][]string
		for _, a := range longQueries {
			rows = append(rows, []string{
				fmt.Sprintf("%d", a.PID),
				a.DatName,
				a.UserName,
				formatDuration(a.DurationSeconds),
				a.State,
				normalizeQuery(a.Query),
			})
		}
		visibleRows := height - 25
		if visibleRows < 5 {
			visibleRows = 5
		}
		b.WriteString(components.RenderTable(th, cols, rows, data.Cursor, visibleRows, -1, contentWidth))
	}

	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}

func filterLongQueries(activity []metrics.PGActivityRow) []metrics.PGActivityRow {
	var result []metrics.PGActivityRow
	for _, a := range activity {
		if a.DurationSeconds >= 5 && a.State == "active" {
			result = append(result, a)
		}
	}
	return result
}

func formatDuration(seconds float64) string {
	if seconds < 1 {
		return fmt.Sprintf("%.0fms", seconds*1000)
	}
	if seconds < 60 {
		return fmt.Sprintf("%.1fs", seconds)
	}
	m := int(seconds) / 60
	s := int(seconds) % 60
	if m < 60 {
		return fmt.Sprintf("%d:%02d", m, s)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("%d:%02d:%02d", h, m, s)
}

func normalizeQuery(q string) string {
	q = strings.ReplaceAll(q, "\n", " ")
	q = strings.ReplaceAll(q, "\t", " ")
	// Collapse multiple spaces
	for strings.Contains(q, "  ") {
		q = strings.ReplaceAll(q, "  ", " ")
	}
	q = strings.TrimSpace(q)
	if len(q) > 100 {
		q = q[:97] + "..."
	}
	return q
}

func truncateVersion(v string) string {
	// PostgreSQL version strings can be very long
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
