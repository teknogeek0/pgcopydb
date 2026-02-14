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

	// Overall progress bar
	totalBytes, copiedBytes := computeProgress(data.Tables, data.Summaries)
	b.WriteString(components.RenderProgressBar(th, "Data", copiedBytes, totalBytes, contentWidth) + "\n")

	// Data transferred + ETA
	b.WriteString(renderDataSummary(th, totalBytes, copiedBytes, data.Summaries) + "\n")

	// Table count summary
	totalTables := len(data.Tables)
	doneTables := countDoneTables(data.Tables, data.Summaries)
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
				duration = formatSeconds(t.Duration)
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

func computeProgress(tables []metrics.CatalogTable, summaries []metrics.CatalogSummaryEntry) (total, copied int64) {
	for _, t := range tables {
		if !t.ExcludeData {
			total += t.Bytes
		}
	}

	for _, s := range summaries {
		if s.Bytes > 0 && strings.HasPrefix(s.Command, "COPY") {
			copied += s.Bytes
		}
	}

	return total, copied
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

func renderDataSummary(th *theme.Theme, total, copied int64, summaries []metrics.CatalogSummaryEntry) string {
	totalStr := metrics.FormatBytes(uint64(total))
	copiedStr := metrics.FormatBytes(uint64(copied))

	// Compute average speed from summaries
	var totalDuration int64
	for _, s := range summaries {
		if strings.HasPrefix(s.Command, "COPY") && s.Duration > 0 {
			totalDuration += s.Duration
		}
	}

	summary := fmt.Sprintf("  %s %s / %s",
		th.DimStyle.Render("Copied:"),
		th.BrightStyle.Render(copiedStr),
		totalStr,
	)

	if totalDuration > 0 && copied > 0 {
		rate := float64(copied) / float64(totalDuration)
		summary += fmt.Sprintf("  %s %s",
			th.DimStyle.Render("Speed:"),
			metrics.FormatBytesRate(rate),
		)

		remaining := total - copied
		if remaining > 0 && rate > 0 {
			eta := time.Duration(float64(remaining)/rate) * time.Second
			summary += fmt.Sprintf("  %s %s",
				th.DimStyle.Render("ETA:"),
				formatGoDuration(eta),
			)
		}
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

func formatSeconds(s int64) string {
	d := time.Duration(s) * time.Second
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
