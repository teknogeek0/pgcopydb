package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/ui"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/ui/overview"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/ui/source"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/ui/system"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/ui/tables"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/ui/target"
)

func (m *Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	// Header
	sourceHost := extractHost(m.cfg.SourceURI)
	targetHost := extractHost(m.cfg.TargetURI)
	runtime := formatDuration(time.Since(m.startTime))
	header := ui.RenderHeader(m.theme, m.width, m.version, sourceHost, targetHost, runtime)

	// Tabs
	tabBar := ui.RenderTabs(m.theme, m.width, TabNames[:], m.activeTab)

	// Content
	contentHeight := m.height - 4 // header(1) + tabs(1) + statusbar(1) + filter(1 if active)
	if m.filterMode {
		contentHeight--
	}
	if contentHeight < 1 {
		contentHeight = 1
	}
	content := m.renderContent(contentHeight)

	// Filter bar
	var filterBar string
	if m.filterMode {
		filterBar = m.theme.StatusBarStyle.Width(m.width).Render(
			fmt.Sprintf(" / %s█", m.filterText),
		)
	}

	// Status bar
	statusBar := m.renderStatusBar()

	// Help overlay
	if m.showHelp {
		return m.renderHelp()
	}

	parts := []string{header, tabBar, content}
	if filterBar != "" {
		parts = append(parts, filterBar)
	}
	parts = append(parts, statusBar)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m *Model) renderContent(height int) string {
	var content string

	switch m.activeTab {
	case TabOverview:
		content = overview.Render(m.theme, m.width, height, overview.Data{
			Setup:     m.catalogSetup,
			Sections:  m.catalogSections,
			Tables:    m.catalogTables,
			Summaries: m.catalogSummaries,
			Timings:   m.catalogTimings,
			Sentinel:  m.catalogSentinel,
			Processes: m.catalogProcesses,
		})
	case TabSource:
		content = source.Render(m.theme, m.width, height, source.Data{
			Version:   m.sourceVersion,
			Uptime:    m.sourceUptime,
			Databases: m.sourceDBS,
			Conns:     m.sourceConns,
			Slots:     m.sourceSlots,
			ReplStats: m.sourceRepls,
			WALLSN:    m.sourceWALLSN,
			Activity:  m.sourceActivity,
			TpsDelta:  m.tpsDelta,
			WalDelta:  m.walDelta,
			Cursor:    m.sourceCursor,
		})
	case TabTarget:
		content = target.Render(m.theme, m.width, height, target.Data{
			Version:       m.targetVersion,
			Uptime:        m.targetUptime,
			Databases:     m.targetDBS,
			Conns:         m.targetConns,
			Activity:      m.targetActivity,
			TpsDelta:      m.tpsDelta,
			CatalogTables: m.catalogTables,
			Summaries:     m.catalogSummaries,
		})
	case TabTables:
		content = tables.Render(m.theme, m.width, height, tables.Data{
			Tables:    m.filteredTables(),
			Summaries: m.catalogSummaries,
			Processes: m.catalogProcesses,
			Cursor:    m.tablesCursor,
			SortCol:   m.tablesSortCol,
		})
	case TabSystem:
		content = system.Render(m.theme, m.width, height, m.sysStats, m.netDelta)
	}

	// Pad content to fill height
	lines := strings.Count(content, "\n") + 1
	if lines < height {
		content += strings.Repeat("\n", height-lines)
	}

	return content
}

func (m *Model) renderStatusBar() string {
	left := m.theme.DimStyle.Render(" tab/1-5:switch  j/k:scroll  s:sort  /:filter  ?:help  q:quit")

	var right string
	if m.lastErr != nil {
		right = m.theme.ErrorStyle.Render(fmt.Sprintf("ERR: %v ", m.lastErr))
	} else {
		var connected []string
		if m.catalogProvider != nil {
			connected = append(connected, "catalog")
		}
		if m.sourcePG != nil {
			connected = append(connected, "source")
		}
		if m.targetPG != nil {
			connected = append(connected, "target")
		}
		if len(connected) > 0 {
			right = m.theme.ConnectedStyle.Render(fmt.Sprintf("● %s ", strings.Join(connected, ", ")))
		}
	}

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := m.width - leftW - rightW
	if gap < 0 {
		gap = 0
	}

	bar := left + strings.Repeat(" ", gap) + right
	return m.theme.StatusBarStyle.Width(m.width).Render(bar)
}

func (m *Model) renderHelp() string {
	help := `
  pgcopydb-tui — Migration Monitor

  Navigation
    tab / shift+tab    Next / previous tab
    1-5                Jump to tab
    j/k or arrows      Scroll up/down
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
	// Simple extraction: find @host:port/ or @host/
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
