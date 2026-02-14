package app

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/metrics"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		if m.filterMode {
			return m.handleFilterKey(msg)
		}
		return m.handleKey(msg)

	case TickMsg:
		return m, m.tickFetch()

	case CatalogDataMsg:
		if msg.Err != nil {
			m.lastErr = msg.Err
		} else {
			m.lastErr = nil
			m.catalogSetup = msg.Setup
			m.catalogSections = msg.Sections
			m.catalogTables = msg.Tables
			m.catalogSummaries = msg.Summaries
			m.catalogTimings = msg.Timings
			m.catalogSentinel = msg.Sentinel
			m.catalogProcesses = msg.Processes
		}
		return m, nil

	case SourcePGMsg:
		if msg.Err != nil {
			m.lastErr = msg.Err
		} else {
			m.sourceVersion = msg.Version
			m.sourceUptime = msg.Uptime
			m.sourceDBS = msg.Databases
			m.sourceConns = msg.Conns
			m.sourceSlots = msg.Slots
			m.sourceRepls = msg.ReplStats
			m.sourceWALLSN = msg.WALLSN
			m.sourceActivity = msg.Activity

			// Track WAL write rate
			if msg.WALLSN != "" {
				lsn, err := metrics.ParseLSN(msg.WALLSN)
				if err == nil {
					m.walDelta.Rate("wal_lsn", int64(lsn))
				}
			}
		}
		return m, nil

	case TargetPGMsg:
		if msg.Err != nil {
			m.lastErr = msg.Err
		} else {
			m.targetVersion = msg.Version
			m.targetUptime = msg.Uptime
			m.targetDBS = msg.Databases
			m.targetConns = msg.Conns
			m.targetActivity = msg.Activity
		}
		return m, nil

	case SystemMsg:
		if msg.Err != nil {
			m.lastErr = msg.Err
		} else if msg.Stats != nil {
			m.sysStats = msg.Stats
		}
		return m, nil
	}

	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (*Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, Keys.Help):
		m.showHelp = !m.showHelp
		return m, nil

	case key.Matches(msg, Keys.NextTab):
		m.activeTab = (m.activeTab + 1) % TabCount
		return m, m.fetchActiveTabPG()

	case key.Matches(msg, Keys.PrevTab):
		m.activeTab = (m.activeTab - 1 + TabCount) % TabCount
		return m, m.fetchActiveTabPG()

	case key.Matches(msg, Keys.Tab1):
		m.activeTab = TabOverview
		return m, m.fetchActiveTabPG()
	case key.Matches(msg, Keys.Tab2):
		m.activeTab = TabSource
		return m, m.fetchActiveTabPG()
	case key.Matches(msg, Keys.Tab3):
		m.activeTab = TabTarget
		return m, m.fetchActiveTabPG()
	case key.Matches(msg, Keys.Tab4):
		m.activeTab = TabTables
		return m, nil
	case key.Matches(msg, Keys.Tab5):
		m.activeTab = TabSystem
		return m, nil

	case key.Matches(msg, Keys.Down):
		m.scrollDown()
		return m, nil
	case key.Matches(msg, Keys.Up):
		m.scrollUp()
		return m, nil
	case key.Matches(msg, Keys.Top):
		m.scrollToTop()
		return m, nil
	case key.Matches(msg, Keys.Bottom):
		m.scrollToBottom()
		return m, nil

	case key.Matches(msg, Keys.Sort):
		m.cycleSort()
		return m, nil

	case key.Matches(msg, Keys.Filter):
		m.filterMode = true
		m.filterText = ""
		return m, nil
	}

	return m, nil
}

func (m *Model) handleFilterKey(msg tea.KeyMsg) (*Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterMode = false
		m.filterText = ""
		return m, nil
	case "enter":
		m.filterMode = false
		return m, nil
	case "backspace":
		if len(m.filterText) > 0 {
			m.filterText = m.filterText[:len(m.filterText)-1]
		}
		return m, nil
	default:
		if len(msg.String()) == 1 {
			m.filterText += msg.String()
		}
		return m, nil
	}
}

func (m *Model) tickFetch() tea.Cmd {
	cmds := []tea.Cmd{
		tickCmd(time.Duration(m.cfg.Interval) * time.Second),
		fetchCatalogData(m.catalogProvider),
		fetchSystemStatsCmd(m.sysProvider),
	}

	pgCmd := m.fetchActiveTabPG()
	if pgCmd != nil {
		cmds = append(cmds, pgCmd)
	}

	return tea.Batch(cmds...)
}

func (m *Model) scrollDown() {
	switch m.activeTab {
	case TabTables:
		max := len(m.filteredTables()) - 1
		if m.tablesCursor < max {
			m.tablesCursor++
		}
	case TabSource:
		max := len(m.sourceActivity) - 1
		if m.sourceCursor < max {
			m.sourceCursor++
		}
	}
}

func (m *Model) scrollUp() {
	switch m.activeTab {
	case TabTables:
		if m.tablesCursor > 0 {
			m.tablesCursor--
		}
	case TabSource:
		if m.sourceCursor > 0 {
			m.sourceCursor--
		}
	}
}

func (m *Model) scrollToTop() {
	switch m.activeTab {
	case TabTables:
		m.tablesCursor = 0
	case TabSource:
		m.sourceCursor = 0
	}
}

func (m *Model) scrollToBottom() {
	switch m.activeTab {
	case TabTables:
		if n := len(m.filteredTables()); n > 0 {
			m.tablesCursor = n - 1
		}
	case TabSource:
		if n := len(m.sourceActivity); n > 0 {
			m.sourceCursor = n - 1
		}
	}
}

func (m *Model) cycleSort() {
	if m.activeTab == TabTables {
		m.tablesSortCol = (m.tablesSortCol + 1) % 6
	}
}

func (m *Model) filteredTables() []metrics.CatalogTable {
	if m.filterText == "" {
		return m.catalogTables
	}
	f := strings.ToLower(m.filterText)
	var result []metrics.CatalogTable
	for _, t := range m.catalogTables {
		if strings.Contains(strings.ToLower(t.QName), f) {
			result = append(result, t)
		}
	}
	return result
}

// WALWriteRate returns the current WAL write rate in bytes per second.
func (m *Model) WALWriteRate() float64 {
	return m.walDelta.RateFloat64("wal_rate", 0)
}
