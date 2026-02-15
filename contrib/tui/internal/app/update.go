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
			m.catalogIndexes = msg.Indexes
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

			// Compute TPS (called once per fetch, so DeltaCalculator works correctly)
			var tps float64
			for _, db := range msg.Databases {
				tps += m.tpsDelta.Rate("src_tps_"+db.DatName, db.TotalXacts)
			}
			m.sourceTPS = tps

			// Compute WAL write rate
			if msg.WALLSN != "" {
				lsn, err := metrics.ParseLSN(msg.WALLSN)
				if err == nil {
					m.walRate = m.walDelta.Rate("wal_lsn", int64(lsn))
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

			// Compute TPS
			var tps float64
			for _, db := range msg.Databases {
				tps += m.tpsDelta.Rate("tgt_tps_"+db.DatName, db.TotalXacts)
			}
			m.targetTPS = tps
		}
		return m, nil

	case SystemMsg:
		if msg.Err != nil {
			m.lastErr = msg.Err
		} else if msg.Stats != nil {
			m.sysStats = msg.Stats

			// Compute network rates
			m.netRxRate = m.netDelta.Rate("net_rx", int64(msg.Stats.NetRxBytes))
			m.netTxRate = m.netDelta.Rate("net_tx", int64(msg.Stats.NetTxBytes))
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

	// Tab keys are no-ops in single-page mode (kept for future use)
	case key.Matches(msg, Keys.NextTab),
		key.Matches(msg, Keys.PrevTab),
		key.Matches(msg, Keys.Tab1),
		key.Matches(msg, Keys.Tab2),
		key.Matches(msg, Keys.Tab3),
		key.Matches(msg, Keys.Tab4),
		key.Matches(msg, Keys.Tab5):
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

	case key.Matches(msg, Keys.ToggleIdx):
		m.showIndexes = !m.showIndexes
		// Reset cursor to avoid out-of-bounds after hiding indexes
		m.tablesCursor = 0
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
		fetchSourcePG(m.sourcePG),
		fetchTargetPG(m.targetPG),
	}
	return tea.Batch(cmds...)
}

// scrollDown always scrolls the Top Tables cursor.
func (m *Model) scrollDown() {
	max := m.filteredRowCount() - 1
	if m.tablesCursor < max {
		m.tablesCursor++
	}
}

// scrollUp always scrolls the Top Tables cursor.
func (m *Model) scrollUp() {
	if m.tablesCursor > 0 {
		m.tablesCursor--
	}
}

func (m *Model) scrollToTop() {
	m.tablesCursor = 0
}

func (m *Model) scrollToBottom() {
	if n := m.filteredRowCount(); n > 0 {
		m.tablesCursor = n - 1
	}
}

// cycleSort always cycles the Top Tables sort column.
func (m *Model) cycleSort() {
	m.tablesSortCol = (m.tablesSortCol + 1) % 6
}

// filteredRowCount returns the total number of displayed rows (tables + index sub-rows).
func (m *Model) filteredRowCount() int {
	tables := m.filteredTables()
	count := 0
	for _, t := range tables {
		if t.ExcludeData {
			continue
		}
		count++ // the table row itself
		if m.showIndexes {
			for _, idx := range m.catalogIndexes {
				if idx.TableOID == t.OID {
					count++
				}
			}
		}
	}
	return count
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

