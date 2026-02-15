package dashboard

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

// Data is a superset of all tab data, passed to the single-page dashboard.
type Data struct {
	// Catalog
	Setup     *metrics.CatalogSetup
	Sections  []metrics.CatalogSection
	Tables    []metrics.CatalogTable
	Summaries []metrics.CatalogSummaryEntry
	Timings   []metrics.CatalogTiming
	Sentinel  *metrics.CatalogSentinel
	Processes []metrics.CatalogProcess

	// Source PG
	SourceVersion  string
	SourceUptime   string
	SourceDBS      []metrics.PGDatabaseStat
	SourceConns    *metrics.PGConnectionSummary
	SourceSlots    []metrics.PGReplicationSlot
	SourceRepls    []metrics.PGReplicationStat
	SourceWALLSN   string
	SourceActivity []metrics.PGActivityRow

	// Target PG
	TargetVersion  string
	TargetUptime   string
	TargetDBS      []metrics.PGDatabaseStat
	TargetConns    *metrics.PGConnectionSummary
	TargetActivity []metrics.PGActivityRow

	// System
	SysStats *metrics.SystemStats

	// Computed rates (from Update, not View)
	SourceTPS float64
	TargetTPS float64
	NetRxRate float64
	NetTxRate float64
	WalRate   float64

	// UI state
	TablesCursor int
	TablesSortCol int
	FilterText   string
}

// Render produces the single-page dashboard content.
// height is the available content height (excluding header and status bar).
func Render(th *theme.Theme, width, height int, data Data) string {
	if width >= 160 {
		return renderTwoColumn(th, width, height, data)
	}
	return renderSingleColumn(th, width, height, data)
}

func renderSingleColumn(th *theme.Theme, width, height int, data Data) string {
	contentWidth := width - 2 // 2-char indent
	if contentWidth < 40 {
		contentWidth = 40
	}

	// Use a compact section title (no margin bottom)
	sectionStyle := th.SectionTitleStyle.MarginBottom(0)

	var sections []string

	// 1. CHECKLIST
	sections = append(sections, renderChecklist(th, sectionStyle, contentWidth, data))

	// 2. PROGRESS
	sections = append(sections, renderProgress(th, sectionStyle, contentWidth, data))

	// 3. VM
	sections = append(sections, renderVM(th, sectionStyle, contentWidth, data))

	// 4. Source
	sections = append(sections, renderSource(th, sectionStyle, contentWidth, data))

	// 5. Target
	sections = append(sections, renderTarget(th, sectionStyle, contentWidth, data))

	// Trim trailing newlines from each section, then join with single blank line
	for i, s := range sections {
		sections[i] = strings.TrimRight(s, "\n")
	}
	fixed := strings.Join(sections, "\n")

	// Count lines consumed by fixed sections
	fixedLines := strings.Count(fixed, "\n") + 1

	// 6. Top Tables — fills remaining height
	tableHeight := height - fixedLines - 1 // -1 for the section title line
	if tableHeight < 3 {
		tableHeight = 3
	}

	topTables := renderTopTables(th, sectionStyle, contentWidth, tableHeight, data)

	return fixed + "\n" + topTables
}

func renderTwoColumn(th *theme.Theme, width, height int, data Data) string {
	gap := 1
	leftWidth := width * 2 / 5 // 40%
	rightWidth := width - leftWidth - gap

	// Content widths inside bordered boxes: border(2) + padding(2) = 4
	leftContent := leftWidth - 4
	rightContent := rightWidth - 6 // extra safety margin for table rendering
	if leftContent < 30 {
		leftContent = 30
	}
	if rightContent < 40 {
		rightContent = 40
	}

	sectionStyle := th.SectionTitleStyle.MarginBottom(0)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(th.FgDim).
		Padding(0, 1).
		Width(leftWidth - 2) // Width excludes border, so subtract 2 for left+right border chars

	// Render left sections in 3 merged boxes to reduce border overhead:
	// Box 1: CHECKLIST
	// Box 2: PROGRESS + VM
	// Box 3: Source + Target
	checklist := renderChecklist(th, sectionStyle, leftContent, data)
	progressVM := renderProgress(th, sectionStyle, leftContent, data) +
		renderVM(th, sectionStyle, leftContent, data)
	sourceTarget := renderSource(th, sectionStyle, leftContent, data) +
		renderTarget(th, sectionStyle, leftContent, data)

	sectionContents := []string{checklist, progressVM, sourceTarget}

	var leftSections []string
	usedHeight := 0
	for _, content := range sectionContents {
		box := boxStyle.Render(strings.TrimRight(content, "\n"))
		boxHeight := strings.Count(box, "\n") + 1
		if usedHeight+boxHeight > height {
			break // skip remaining sections that won't fit
		}
		leftSections = append(leftSections, box)
		usedHeight += boxHeight
	}
	leftCol := lipgloss.JoinVertical(lipgloss.Left, leftSections...)

	// Right panel: one tall bordered box with the table
	// Account for border (2 lines top+bottom) + title line + scroll indicator
	tableHeight := height - 4
	if tableHeight < 5 {
		tableHeight = 5
	}
	tableContent := renderTopTables(th, sectionStyle, rightContent, tableHeight, data)

	rightBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(th.FgDim).
		Padding(0, 1).
		Width(rightWidth - 2).
		Height(height - 2) // fill available height inside border

	rightCol := rightBox.Render(tableContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, strings.Repeat(" ", gap), rightCol)
}

// renderChecklist renders the migration steps checklist.
func renderChecklist(th *theme.Theme, sectionStyle lipgloss.Style, contentWidth int, data Data) string {
	var b strings.Builder

	// Section header with start time
	header := "CHECKLIST"
	var timingInfo string
	if len(data.Timings) > 0 {
		// Find earliest start time
		var earliest int64
		var latest int64
		for _, t := range data.Timings {
			if t.StartTimeEpoch > 0 && (earliest == 0 || t.StartTimeEpoch < earliest) {
				earliest = t.StartTimeEpoch
			}
			if t.DoneTimeEpoch > latest {
				latest = t.DoneTimeEpoch
			}
		}
		if earliest > 0 {
			startStr := time.Unix(earliest, 0).UTC().Format("2006-01-02 15:04 UTC")
			// Find total duration from the dedicated timing entry
			var totalTime string
			for _, t := range data.Timings {
				if t.Label == "Total Wall Clock Duration" && t.Duration > 0 {
					totalTime = fmt.Sprintf(", total time: %s", formatMillis(t.Duration))
					break
				}
			}
			// Fallback: compute from epochs if no timing entry
			if totalTime == "" && latest > earliest {
				totalTime = fmt.Sprintf(", total time: %s", formatGoDuration(time.Duration(latest-earliest)*time.Second))
			}
			timingInfo = fmt.Sprintf("(started: %s%s)", startStr, totalTime)
		}
	}

	// If full header fits on one line, use it; otherwise split
	fullHeader := header
	if timingInfo != "" {
		fullHeader = header + "  " + timingInfo
	}
	if len("  "+fullHeader) <= contentWidth {
		b.WriteString(sectionStyle.Render("  "+fullHeader) + "\n")
	} else {
		b.WriteString(sectionStyle.Render("  "+header) + "\n")
		if timingInfo != "" {
			b.WriteString("  " + th.DimStyle.Render(timingInfo) + "\n")
		}
	}

	// Build items
	activeCount := countActiveProcessesByType(data.Processes, "COPY")
	totalTables := countDataTables(data.Tables)
	doneTables := countDoneTables(data.Tables, data.Summaries)
	copiedBytes := sumCopiedBytes(data.Tables, data.Summaries)
	totalBytes := sumTotalBytes(data.Tables)

	var items []components.CheckItem
	for _, t := range data.Timings {
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

		// Enrich COPY timing with extra details
		if status == components.StatusActive && isCopyTiming(t.Label) {
			var parts []string
			parts = append(parts, fmt.Sprintf("%d/%d tables", doneTables, totalTables))
			if activeCount > 0 {
				parts = append(parts, fmt.Sprintf("%d active", activeCount))
			}
			if totalBytes > 0 {
				pct := float64(copiedBytes) / float64(totalBytes) * 100
				parts = append(parts, fmt.Sprintf("%s/%s (%.0f%%)",
					metrics.FormatBytes(uint64(copiedBytes)),
					metrics.FormatBytes(uint64(totalBytes)),
					pct,
				))
			}
			// ETA based on wall clock
			if t.StartTimeEpoch > 0 && copiedBytes > 0 && totalBytes > copiedBytes {
				elapsed := time.Since(time.Unix(t.StartTimeEpoch, 0)).Seconds()
				if elapsed > 0 {
					rate := float64(copiedBytes) / elapsed
					remaining := float64(totalBytes-copiedBytes) / rate
					parts = append(parts, fmt.Sprintf("ETA %s", formatGoDuration(time.Duration(remaining)*time.Second)))
				}
			}
			detail = strings.Join(parts, ", ")
		}

		items = append(items, components.CheckItem{
			Label:    t.Label,
			Status:   status,
			Detail:   detail,
			Duration: duration,
		})
	}

	if len(items) == 0 {
		b.WriteString(th.DimStyle.Render("  No timing data available") + "\n")
	} else {
		b.WriteString(components.RenderChecklist(th, items))
	}

	return b.String()
}

// renderProgress renders the overall data progress section.
func renderProgress(th *theme.Theme, sectionStyle lipgloss.Style, contentWidth int, data Data) string {
	var b strings.Builder

	totalTables := countDataTables(data.Tables)
	doneTables := countDoneTables(data.Tables, data.Summaries)
	copiedBytes := sumCopiedBytes(data.Tables, data.Summaries)
	totalBytes := sumTotalBytes(data.Tables)
	procCount := len(data.Processes)
	copyCount := countActiveProcessesByType(data.Processes, "COPY")

	// Title with status
	isDone := doneTables == totalTables && totalTables > 0 && procCount == 0
	status := "IDLE"
	if procCount > 0 {
		status = fmt.Sprintf("RUNNING (%d procs)", procCount)
	} else if isDone {
		status = "DONE"
	}
	b.WriteString(sectionStyle.Render("  PROGRESS") + "  " + th.CyanStyle.Render(status) + "\n")

	// Progress bar (table-count based)
	b.WriteString(components.RenderProgressBar(th, "Data", int64(doneTables), int64(totalTables), contentWidth) + "\n")

	// Data line
	if totalBytes > 0 {
		pct := float64(copiedBytes) / float64(totalBytes) * 100
		if pct > 100 {
			pct = 100
		}
		b.WriteString(fmt.Sprintf("  %s %s / %s (%.0f%%)\n",
			th.DimStyle.Render("Data:"),
			metrics.FormatBytes(uint64(copiedBytes)),
			metrics.FormatBytes(uint64(totalBytes)),
			pct,
		))
	}

	// Rate + ETA (only when running or has valid wall clock data)
	var wallClockMs int64
	for _, t := range data.Timings {
		if isCopyTiming(t.Label) && t.StartTimeEpoch > 0 {
			if t.Duration > 0 {
				wallClockMs = t.Duration
			} else {
				wallClockMs = time.Since(time.Unix(t.StartTimeEpoch, 0)).Milliseconds()
			}
			break
		}
	}

	if wallClockMs > 0 && copiedBytes > 0 {
		wallClockSec := float64(wallClockMs) / 1000.0
		rate := float64(copiedBytes) / wallClockSec
		rateLine := fmt.Sprintf("  %s %s", th.DimStyle.Render("Rate:"), metrics.FormatBytesRate(rate))
		if copyCount > 0 {
			rateLine += fmt.Sprintf(", %d copying", copyCount)
		}
		b.WriteString(rateLine + "\n")

		// Only show ETA when not done
		if !isDone && totalBytes > copiedBytes {
			remaining := float64(totalBytes-copiedBytes) / rate
			b.WriteString(fmt.Sprintf("  %s ~%s\n",
				th.DimStyle.Render("ETA:"),
				formatGoDuration(time.Duration(remaining)*time.Second),
			))
		}
	}

	return b.String()
}

// renderVM renders the VM/system resources section.
func renderVM(th *theme.Theme, sectionStyle lipgloss.Style, contentWidth int, data Data) string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("  VM") + "\n")

	if data.SysStats == nil {
		b.WriteString(th.DimStyle.Render("  Collecting...") + "\n")
		return b.String()
	}

	s := data.SysStats
	procCount := len(data.Processes)
	copyCount := countActiveProcessesByType(data.Processes, "COPY")

	if contentWidth >= 80 {
		// Wide: pack onto two lines
		b.WriteString(fmt.Sprintf("  Disk: %s/%s (%.0f%%)  Mem: %s/%s (%.0f%%)  CPU: %.0f%%  %d procs, %d copying\n",
			metrics.FormatBytes(s.DiskUsed), metrics.FormatBytes(s.DiskTotal), s.DiskPercent,
			metrics.FormatBytes(s.MemUsed), metrics.FormatBytes(s.MemTotal), s.MemPercent,
			s.CPUPercent,
			procCount, copyCount,
		))
		b.WriteString(fmt.Sprintf("  Net: RX %s  TX %s\n",
			th.BrightStyle.Render(metrics.FormatBytesRate(data.NetRxRate)),
			th.BrightStyle.Render(metrics.FormatBytesRate(data.NetTxRate)),
		))
	} else {
		// Narrow: one metric per line
		b.WriteString(fmt.Sprintf("  %s %s/%s (%.0f%%)\n",
			th.DimStyle.Render("Disk:"),
			metrics.FormatBytes(s.DiskUsed), metrics.FormatBytes(s.DiskTotal), s.DiskPercent))
		b.WriteString(fmt.Sprintf("  %s %s/%s (%.0f%%)\n",
			th.DimStyle.Render("Mem: "),
			metrics.FormatBytes(s.MemUsed), metrics.FormatBytes(s.MemTotal), s.MemPercent))
		b.WriteString(fmt.Sprintf("  %s %.0f%%  %d procs, %d copying\n",
			th.DimStyle.Render("CPU: "),
			s.CPUPercent, procCount, copyCount))
		b.WriteString(fmt.Sprintf("  %s RX %s  TX %s\n",
			th.DimStyle.Render("Net: "),
			th.BrightStyle.Render(metrics.FormatBytesRate(data.NetRxRate)),
			th.BrightStyle.Render(metrics.FormatBytesRate(data.NetTxRate)),
		))
	}

	return b.String()
}

// renderSource renders the source database section.
func renderSource(th *theme.Theme, sectionStyle lipgloss.Style, contentWidth int, data Data) string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("  Source") + "\n")

	if data.SourceVersion == "" && len(data.SourceDBS) == 0 {
		b.WriteString(th.DimStyle.Render("  Not connected") + "\n")
		return b.String()
	}

	if contentWidth >= 80 {
		// Wide: pack onto one line
		var parts []string
		if len(data.SourceDBS) > 0 {
			var totalSize int64
			for _, db := range data.SourceDBS {
				totalSize += db.SizeBytes
			}
			parts = append(parts, fmt.Sprintf("DB: %s", metrics.FormatBytes(uint64(totalSize))))
			parts = append(parts, fmt.Sprintf("TPS: %.1f/s", data.SourceTPS))
		}
		if data.SourceConns != nil {
			c := data.SourceConns
			parts = append(parts, fmt.Sprintf("Conns: %d (%d active, %d idle)", c.Total, c.Active, c.Idle))
		}
		if data.SourceWALLSN != "" {
			walPart := fmt.Sprintf("WAL: %s", data.SourceWALLSN)
			if data.WalRate > 0 {
				walPart += fmt.Sprintf(" (%s)", metrics.FormatBytesRate(data.WalRate))
			}
			parts = append(parts, walPart)
		}
		if len(parts) > 0 {
			b.WriteString("  " + strings.Join(parts, "  ") + "\n")
		}
	} else {
		// Narrow: one metric per line
		if len(data.SourceDBS) > 0 {
			var totalSize int64
			for _, db := range data.SourceDBS {
				totalSize += db.SizeBytes
			}
			b.WriteString(fmt.Sprintf("  %s %s  %s %.1f/s\n",
				th.DimStyle.Render("DB:"),
				metrics.FormatBytes(uint64(totalSize)),
				th.DimStyle.Render("TPS:"),
				data.SourceTPS))
		}
		if data.SourceConns != nil {
			c := data.SourceConns
			b.WriteString(fmt.Sprintf("  %s %d (%d active, %d idle)\n",
				th.DimStyle.Render("Conns:"), c.Total, c.Active, c.Idle))
		}
		if data.SourceWALLSN != "" {
			walLine := fmt.Sprintf("  %s %s", th.DimStyle.Render("WAL:"), data.SourceWALLSN)
			if data.WalRate > 0 {
				walLine += fmt.Sprintf(" (%s)", metrics.FormatBytesRate(data.WalRate))
			}
			b.WriteString(walLine + "\n")
		}
	}

	// Replication slots (compact, one per line)
	for _, slot := range data.SourceSlots {
		activeStr := "inactive"
		if slot.Active {
			activeStr = th.GreenStyle.Render("active")
		} else {
			activeStr = th.RedStyle.Render("inactive")
		}
		retention := metrics.FormatBytes(uint64(slot.RetainedBytes))
		b.WriteString(fmt.Sprintf("  %s  %s  %s  retained: %s\n",
			th.BrightStyle.Render(slot.Name), slot.SlotType, activeStr, retention))
	}

	// Long queries: just a count
	longCount := 0
	for _, a := range data.SourceActivity {
		if a.DurationSeconds >= 5 && a.State == "active" {
			longCount++
		}
	}
	if longCount > 0 {
		b.WriteString(fmt.Sprintf("  %s %d\n",
			th.YellowStyle.Render("Long queries (>5s):"), longCount))
	}

	return b.String()
}

// renderTarget renders the target database section.
func renderTarget(th *theme.Theme, sectionStyle lipgloss.Style, contentWidth int, data Data) string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("  Target") + "\n")

	if data.TargetVersion == "" && len(data.TargetDBS) == 0 {
		b.WriteString(th.DimStyle.Render("  Not connected") + "\n")
		return b.String()
	}

	totalTables := len(data.Tables)
	populatedTables := countDoneTables(data.Tables, data.Summaries)

	if contentWidth >= 80 {
		// Wide: pack onto one line
		var parts []string
		if len(data.TargetDBS) > 0 {
			var totalSize int64
			var cacheSum float64
			for _, db := range data.TargetDBS {
				totalSize += db.SizeBytes
				cacheSum += db.CacheHitRatio
			}
			parts = append(parts, fmt.Sprintf("DB: %s", metrics.FormatBytes(uint64(totalSize))))
			parts = append(parts, fmt.Sprintf("Tables: %d/%d populated", populatedTables, totalTables))
			if data.TargetConns != nil {
				parts = append(parts, fmt.Sprintf("Conns: %d", data.TargetConns.Total))
			}
			parts = append(parts, fmt.Sprintf("TPS: %.1f/s", data.TargetTPS))
			avgCache := cacheSum / float64(len(data.TargetDBS))
			parts = append(parts, fmt.Sprintf("Cache: %.1f%%", avgCache))
		}
		if len(parts) > 0 {
			b.WriteString("  " + strings.Join(parts, "  ") + "\n")
		}
	} else {
		// Narrow: split onto separate lines
		if len(data.TargetDBS) > 0 {
			var totalSize int64
			var cacheSum float64
			for _, db := range data.TargetDBS {
				totalSize += db.SizeBytes
				cacheSum += db.CacheHitRatio
			}
			b.WriteString(fmt.Sprintf("  %s %s  %s %d/%d\n",
				th.DimStyle.Render("DB:"),
				metrics.FormatBytes(uint64(totalSize)),
				th.DimStyle.Render("Tables:"),
				populatedTables, totalTables))
			connStr := ""
			if data.TargetConns != nil {
				connStr = fmt.Sprintf("  %s %d", th.DimStyle.Render("Conns:"), data.TargetConns.Total)
			}
			avgCache := cacheSum / float64(len(data.TargetDBS))
			b.WriteString(fmt.Sprintf("  %s %.1f/s  %s %.1f%%%s\n",
				th.DimStyle.Render("TPS:"),
				data.TargetTPS,
				th.DimStyle.Render("Cache:"),
				avgCache,
				connStr))
		}
	}

	return b.String()
}

// renderTopTables renders the scrollable per-table progress section.
func renderTopTables(th *theme.Theme, sectionStyle lipgloss.Style, contentWidth, tableHeight int, data Data) string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("  Top Tables (by remaining)") + "\n")

	// Build summary lookup
	summaryByOID := buildSummaryMap(data.Summaries)
	activeOIDs := buildActiveOIDs(data.Processes)

	tables := data.Tables
	if data.FilterText != "" {
		tables = filterTables(tables, data.FilterText)
	}

	var rows []tableRow
	for _, t := range tables {
		if t.ExcludeData {
			continue
		}
		row := tableRow{
			name:      t.QName,
			totalSize: t.Bytes,
		}

		if s, ok := summaryByOID[t.OID]; ok {
			row.copied = s.Bytes
			durationSec := float64(s.Duration) / 1000.0
			if durationSec > 0 {
				row.speed = float64(s.Bytes) / durationSec
			}

			if s.DoneTimeEpoch > 0 {
				row.status = "done"
				row.pct = 100.0
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
	sortRows(rows, data.TablesSortCol)

	// Build table columns — Table name column absorbs available width.
	// lipgloss Width EXCLUDES PaddingRight, so total rendered row =
	// sum(all Widths) + 6 (PaddingRight(1) on 6 of 7 columns).
	sizeW, copiedW, pctW, speedW, etaW, statusW := 9, 9, 6, 11, 9, 7
	if contentWidth > 100 {
		sizeW, copiedW, pctW, speedW, etaW, statusW = 10, 10, 7, 12, 10, 10
	}
	fixedColsWidth := sizeW + copiedW + pctW + speedW + etaW + statusW
	interColPadding := 6 // PaddingRight(1) on 6 of 7 columns
	nameWidth := contentWidth - fixedColsWidth - interColPadding
	if nameWidth < 15 {
		nameWidth = 15
	}

	cols := []components.Column{
		{Title: "Table", Width: nameWidth},
		{Title: "Size", Width: sizeW, Align: 1},
		{Title: "Copied", Width: copiedW, Align: 1},
		{Title: "%", Width: pctW, Align: 1},
		{Title: "Speed", Width: speedW, Align: 1},
		{Title: "ETA", Width: etaW, Align: 1},
		{Title: "Status", Width: statusW},
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

	visibleRows := tableHeight - 2 // header + scroll indicator
	if visibleRows < 3 {
		visibleRows = 3
	}

	b.WriteString(components.RenderTable(th, cols, tableRows, data.TablesCursor, visibleRows, data.TablesSortCol, contentWidth))

	return b.String()
}

// --- helpers ---

type tableRow struct {
	name      string
	totalSize int64
	copied    int64
	pct       float64
	speed     float64
	eta       string
	status    string
}

func isCopyTiming(label string) bool {
	return strings.Contains(strings.ToLower(label), "copy") &&
		strings.Contains(strings.ToLower(label), "wall clock")
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

func countDoneTables(tables []metrics.CatalogTable, summaries []metrics.CatalogSummaryEntry) int {
	done := make(map[int64]bool)
	for _, s := range summaries {
		if s.TableOID > 0 && s.DoneTimeEpoch > 0 && strings.HasPrefix(s.Command, "COPY") {
			done[s.TableOID] = true
		}
	}
	return len(done)
}

// sumCopiedBytes computes the effective "copied" bytes for progress display.
// For done tables, it uses the full table size (from s_table_size) since
// summary.bytes (COPY stream bytes) differs from the on-disk relation size.
// For in-progress tables, it uses summary.bytes as a best estimate.
func sumCopiedBytes(tables []metrics.CatalogTable, summaries []metrics.CatalogSummaryEntry) int64 {
	tableSize := make(map[int64]int64)
	for _, t := range tables {
		if !t.ExcludeData {
			tableSize[t.OID] = t.Bytes
		}
	}

	var total int64
	for _, s := range summaries {
		if s.TableOID > 0 && strings.HasPrefix(s.Command, "COPY") {
			if s.DoneTimeEpoch > 0 {
				// Done: count the full table size
				total += tableSize[s.TableOID]
			} else if s.Bytes > 0 {
				// In-progress: use COPY stream bytes
				total += s.Bytes
			}
		}
	}
	return total
}

func sumTotalBytes(tables []metrics.CatalogTable) int64 {
	var total int64
	for _, t := range tables {
		if !t.ExcludeData {
			total += t.Bytes
		}
	}
	return total
}

func countActiveProcessesByType(processes []metrics.CatalogProcess, psType string) int {
	n := 0
	for _, p := range processes {
		if p.PSType == psType {
			n++
		}
	}
	return n
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

func filterTables(tables []metrics.CatalogTable, filter string) []metrics.CatalogTable {
	f := strings.ToLower(filter)
	var result []metrics.CatalogTable
	for _, t := range tables {
		if strings.Contains(strings.ToLower(t.QName), f) {
			result = append(result, t)
		}
	}
	return result
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
