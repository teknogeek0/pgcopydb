package overview

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/metrics"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/theme"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/ui/components"
)

type Data struct {
	Setup     *metrics.CatalogSetup
	Sections  []metrics.CatalogSection
	Tables    []metrics.CatalogTable
	Summaries []metrics.CatalogSummaryEntry
	Timings   []metrics.CatalogTiming
	Sentinel  *metrics.CatalogSentinel
	Processes []metrics.CatalogProcess
}

func Render(th *theme.Theme, width, height int, data Data) string {
	var b strings.Builder
	contentWidth := width - 4
	if contentWidth < 40 {
		contentWidth = 40
	}

	// Migration Checklist
	b.WriteString(th.SectionTitleStyle.Render("  Migration Progress") + "\n")
	b.WriteString(renderChecklist(th, data.Timings, data.Processes) + "\n")

	// Overall progress bar — use table count (bytes mismatch: pg_table_size includes
	// TOAST/FSM/VM overhead but COPY only transfers raw tuples)
	totalTables := countDataTables(data.Tables)
	doneTables := countDoneTables(data.Tables, data.Summaries)
	b.WriteString(components.RenderProgressBar(th, "Data", int64(doneTables), int64(totalTables), contentWidth) + "\n")

	// Data transferred
	copiedBytes := sumCopyBytes(data.Summaries)
	b.WriteString(renderDataSummary(th, data.Timings, copiedBytes, data.Summaries) + "\n")

	// Table count summary
	b.WriteString(fmt.Sprintf("  %s %d/%d tables completed\n",
		th.DimStyle.Render("Tables:"),
		doneTables, totalTables,
	))

	// Active workers
	if len(data.Processes) > 0 {
		b.WriteString("\n" + th.SectionTitleStyle.Render("  Active Workers") + "\n")
		for _, p := range data.Processes {
			status := th.CyanStyle.Render(fmt.Sprintf("  [%s]", p.PSType))
			b.WriteString(fmt.Sprintf("  %s %s (pid %d)\n", status, p.PSTitle, p.PID))
		}
	}

	// CDC Sentinel Status
	if data.Sentinel != nil {
		b.WriteString("\n" + th.SectionTitleStyle.Render("  CDC Status") + "\n")
		b.WriteString(renderSentinel(th, data.Sentinel))
	}

	// Setup info
	if data.Setup != nil {
		b.WriteString("\n" + th.SectionTitleStyle.Render("  Configuration") + "\n")
		b.WriteString(renderSetup(th, data.Setup))
	}

	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}

func renderChecklist(th *theme.Theme, timings []metrics.CatalogTiming, processes []metrics.CatalogProcess) string {
	var items []components.CheckItem

	// Build active process types set
	activeTypes := make(map[string]bool)
	for _, p := range processes {
		activeTypes[p.PSType] = true
	}

	for _, t := range timings {
		var status components.CheckStatus
		var duration string

		if t.DoneTimeEpoch > 0 {
			status = components.StatusDone
			if t.DurationPretty != "" {
				duration = t.DurationPretty
			} else if t.Duration > 0 {
				duration = formatMillis(t.Duration)
			}
		} else if t.StartTimeEpoch > 0 {
			status = components.StatusActive
			elapsed := time.Since(time.Unix(t.StartTimeEpoch, 0))
			duration = formatGoDuration(elapsed)
		} else {
			status = components.StatusPending
		}

		detail := ""
		if t.Bytes > 0 {
			detail = metrics.FormatBytes(uint64(t.Bytes))
		}
		if t.Count > 0 {
			if detail != "" {
				detail += ", "
			}
			detail += fmt.Sprintf("%d items", t.Count)
		}

		items = append(items, components.CheckItem{
			Label:    t.Label,
			Status:   status,
			Detail:   detail,
			Duration: duration,
		})
	}

	if len(items) == 0 {
		return th.DimStyle.Render("  No timing data available\n")
	}

	return components.RenderChecklist(th, items)
}

func countDataTables(tables []metrics.CatalogTable) int {
	n := 0
	for _, t := range tables {
		if !t.ExcludeData {
			n++
		}
	}
	return n
}

func sumCopyBytes(summaries []metrics.CatalogSummaryEntry) int64 {
	var total int64
	for _, s := range summaries {
		if s.Bytes > 0 && strings.HasPrefix(s.Command, "COPY") {
			total += s.Bytes
		}
	}
	return total
}

func countDoneTables(tables []metrics.CatalogTable, summaries []metrics.CatalogSummaryEntry) int {
	done := make(map[int64]bool)
	for _, s := range summaries {
		if s.TableOID > 0 && s.DoneTimeEpoch > 0 && strings.HasPrefix(s.Command, "COPY") {
			done[s.TableOID] = true
		}
	}
	return len(done)
}

func renderDataSummary(th *theme.Theme, timings []metrics.CatalogTiming, copied int64, summaries []metrics.CatalogSummaryEntry) string {
	copiedStr := metrics.FormatBytes(uint64(copied))

	// Get wall clock duration for COPY phase (duration is in milliseconds)
	var wallClockMs int64
	for _, t := range timings {
		if t.Label == "COPY, INDEX, CONSTRAINTS, VACUUM (wall clock)" && t.Duration > 0 {
			wallClockMs = t.Duration
			break
		}
	}

	summary := fmt.Sprintf("  %s %s",
		th.DimStyle.Render("Transferred:"),
		th.BrightStyle.Render(copiedStr),
	)

	if wallClockMs > 0 && copied > 0 {
		wallClockSec := float64(wallClockMs) / 1000.0
		rate := float64(copied) / wallClockSec
		summary += fmt.Sprintf("  %s %s",
			th.DimStyle.Render("Speed:"),
			metrics.FormatBytesRate(rate),
		)
	}

	return summary
}

func renderSentinel(th *theme.Theme, s *metrics.CatalogSentinel) string {
	var b strings.Builder
	applyStr := "no"
	if s.Apply {
		applyStr = th.GreenStyle.Render("yes")
	}
	b.WriteString(fmt.Sprintf("  %s %s   %s %s → %s\n",
		th.DimStyle.Render("Apply:"), applyStr,
		th.DimStyle.Render("Range:"), s.StartPos, s.EndPos,
	))
	b.WriteString(fmt.Sprintf("  %s %s   %s %s   %s %s\n",
		th.DimStyle.Render("Write:"), s.WriteLSN,
		th.DimStyle.Render("Flush:"), s.FlushLSN,
		th.DimStyle.Render("Replay:"), s.ReplayLSN,
	))
	return b.String()
}

func renderSetup(th *theme.Theme, s *metrics.CatalogSetup) string {
	var b strings.Builder
	if s.SlotName != "" {
		b.WriteString(fmt.Sprintf("  %s %s\n", th.DimStyle.Render("Slot:"), s.SlotName))
	}
	if s.Plugin != "" {
		b.WriteString(fmt.Sprintf("  %s %s\n", th.DimStyle.Render("Plugin:"), s.Plugin))
	}
	if s.Snapshot != "" {
		b.WriteString(fmt.Sprintf("  %s %s\n", th.DimStyle.Render("Snapshot:"), s.Snapshot))
	}
	return b.String()
}

func formatMillis(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	return formatGoDuration(d)
}

func formatGoDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
