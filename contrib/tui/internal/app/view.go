package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/ui"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/ui/dashboard"
)

func (m *Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	// Header
	runtime := formatDuration(time.Since(m.startTime))
	header := ui.RenderHeader(m.theme, m.width, runtime)

	// Content — single dashboard page
	contentHeight := m.height - 2 // header + statusbar only
	if m.filterMode {
		contentHeight--
	}
	if contentHeight < 1 {
		contentHeight = 1
	}

	content := dashboard.Render(m.theme, m.width, contentHeight, dashboard.Data{
		// Catalog
		Setup:     m.catalogSetup,
		Sections:  m.catalogSections,
		Tables:    m.catalogTables,
		Summaries: m.catalogSummaries,
		Timings:   m.catalogTimings,
		Sentinel:  m.catalogSentinel,
		Processes: m.catalogProcesses,

		// Source PG
		SourceVersion:  m.sourceVersion,
		SourceUptime:   m.sourceUptime,
		SourceDBS:      m.sourceDBS,
		SourceConns:    m.sourceConns,
		SourceSlots:    m.sourceSlots,
		SourceRepls:    m.sourceRepls,
		SourceWALLSN:   m.sourceWALLSN,
		SourceActivity: m.sourceActivity,

		// Target PG
		TargetVersion:  m.targetVersion,
		TargetUptime:   m.targetUptime,
		TargetDBS:      m.targetDBS,
		TargetConns:    m.targetConns,
		TargetActivity: m.targetActivity,

		// System
		SysStats: m.sysStats,

		// Computed rates
		SourceTPS: m.sourceTPS,
		TargetTPS: m.targetTPS,
		NetRxRate: m.netRxRate,
		NetTxRate: m.netTxRate,
		WalRate:   m.walRate,

		// UI state
		TablesCursor:  m.tablesCursor,
		TablesSortCol: m.tablesSortCol,
		FilterText:    m.filterText,
	})

	// Filter bar
	var filterBar string
	if m.filterMode {
		filterBar = lipgloss.NewStyle().
			Foreground(m.theme.StatusBarStyle.GetForeground()).
			Background(m.theme.StatusBarStyle.GetBackground()).
			Width(m.width).
			Render(fmt.Sprintf(" / %s█", m.filterText))
	}

	// Status bar
	statusBar := m.renderStatusBar()

	// Help overlay
	if m.showHelp {
		return m.renderHelp()
	}

	parts := []string{header, content}
	if filterBar != "" {
		parts = append(parts, filterBar)
	}
	parts = append(parts, statusBar)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// renderTabContent is kept for future tab restoration.
func (m *Model) renderTabContent(height int) string {
	// Placeholder for future tab-based rendering.
	return ""
}

func (m *Model) renderStatusBar() string {
	left := " j/k:scroll s:sort /:filter ?:help q:quit"

	var right string
	if m.lastErr != nil {
		right = fmt.Sprintf("ERR: %v ", m.lastErr)
	} else {
		var connected []string
		if m.catalogProvider != nil {
			connected = append(connected, "cat")
		}
		if m.sourcePG != nil {
			connected = append(connected, "src")
		}
		if m.targetPG != nil {
			connected = append(connected, "tgt")
		}
		if len(connected) > 0 {
			right = fmt.Sprintf("● %s ", strings.Join(connected, ","))
		}
	}

	// Build the bar content within the available inner width
	leftStyled := m.theme.DimStyle.Render(left)
	var rightStyled string
	if m.lastErr != nil {
		rightStyled = m.theme.ErrorStyle.Render(right)
	} else if right != "" {
		rightStyled = m.theme.ConnectedStyle.Render(right)
	}

	leftW := lipgloss.Width(leftStyled)
	rightW := lipgloss.Width(rightStyled)
	gap := m.width - leftW - rightW
	if gap < 0 {
		gap = 0
	}

	bar := leftStyled + strings.Repeat(" ", gap) + rightStyled
	// StatusBarStyle has Padding(0,1) adding 2 chars; use inline style to avoid wrapping
	return lipgloss.NewStyle().
		Foreground(m.theme.StatusBarStyle.GetForeground()).
		Background(m.theme.StatusBarStyle.GetBackground()).
		Width(m.width).
		Render(bar)
}

func (m *Model) renderHelp() string {
	help := `
  pgcopydb-tui — Migration Monitor

  Navigation
    j/k or arrows      Scroll up/down (Top Tables)
    g/G                Jump to top/bottom

  Actions
    s                  Cycle sort column
    /                  Filter mode (type to filter, enter to confirm, esc to cancel)
    ?                  Toggle this help

  Quit
    q / ctrl+c         Exit
`

	style := lipgloss.NewStyle().
		Foreground(m.theme.FgPrimary).
		Background(m.theme.BgSecondary).
		Padding(2, 4).
		Width(m.width).
		Height(m.height)

	return style.Render(help)
}

func extractHost(uri string) string {
	if uri == "" {
		return "N/A"
	}
	at := strings.Index(uri, "@")
	if at == -1 {
		return uri
	}
	rest := uri[at+1:]
	if slash := strings.Index(rest, "/"); slash != -1 {
		rest = rest[:slash]
	}
	return rest
}

func formatDuration(d time.Duration) string {
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
