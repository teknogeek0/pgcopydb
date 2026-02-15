package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/catalog"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/config"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/metrics"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/pgmetrics"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/sysinfo"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/theme"
)

const (
	TabOverview = iota
	TabSource
	TabTarget
	TabTables
	TabSystem
	TabCount
)

var TabNames = []string{"Overview", "Source", "Target", "Tables", "System"}

type Model struct {
	// Config
	cfg     *config.Config
	theme   *theme.Theme
	version string

	// Providers
	catalogProvider *catalog.Provider
	sourcePG        *pgmetrics.Provider
	targetPG        *pgmetrics.Provider
	replicaPGs      []*pgmetrics.Provider
	sysProvider     *sysinfo.Provider

	// Window
	width  int
	height int
	ready  bool

	// Tab state
	activeTab int

	// Catalog data
	catalogSetup     *metrics.CatalogSetup
	catalogSections  []metrics.CatalogSection
	catalogTables    []metrics.CatalogTable
	catalogSummaries []metrics.CatalogSummaryEntry
	catalogTimings   []metrics.CatalogTiming
	catalogSentinel  *metrics.CatalogSentinel
	catalogProcesses []metrics.CatalogProcess

	// Source PG data
	sourceVersion string
	sourceUptime  string
	sourceDBS     []metrics.PGDatabaseStat
	sourceConns   *metrics.PGConnectionSummary
	sourceSlots   []metrics.PGReplicationSlot
	sourceRepls   []metrics.PGReplicationStat
	sourceWALLSN  string
	sourceActivity []metrics.PGActivityRow

	// Target PG data
	targetVersion string
	targetUptime  string
	targetDBS     []metrics.PGDatabaseStat
	targetConns   *metrics.PGConnectionSummary
	targetActivity []metrics.PGActivityRow

	// System data
	sysStats *metrics.SystemStats

	// Deltas (internal, used only in Update)
	tpsDelta *metrics.DeltaCalculator
	netDelta *metrics.DeltaCalculator
	walDelta *metrics.DeltaCalculator

	// Computed rates (set in Update, read in View)
	sourceTPS float64
	targetTPS float64
	netRxRate float64
	netTxRate float64
	walRate   float64

	// UI state
	tablesCursor  int
	tablesSortCol int
	sourceCursor  int
	showHelp      bool
	filterMode    bool
	filterText    string
	lastErr       error
	startTime     time.Time
}

func NewModel(
	cfg *config.Config,
	th *theme.Theme,
	catalogProv *catalog.Provider,
	sourcePG *pgmetrics.Provider,
	targetPG *pgmetrics.Provider,
	replicaPGs []*pgmetrics.Provider,
	sysProv *sysinfo.Provider,
	version string,
) *Model {
	return &Model{
		cfg:             cfg,
		theme:           th,
		version:         version,
		catalogProvider: catalogProv,
		sourcePG:        sourcePG,
		targetPG:        targetPG,
		replicaPGs:      replicaPGs,
		sysProvider:     sysProv,
		tpsDelta:        metrics.NewDeltaCalculator(),
		netDelta:        metrics.NewDeltaCalculator(),
		walDelta:        metrics.NewDeltaCalculator(),
		startTime:       time.Now(),
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		fetchCatalogData(m.catalogProvider),
		fetchSystemStatsCmd(m.sysProvider),
		fetchSourcePG(m.sourcePG),
		fetchTargetPG(m.targetPG),
		tickCmd(time.Duration(m.cfg.Interval)*time.Second),
	)
}

func (m *Model) Cleanup() {
	if m.catalogProvider != nil {
		m.catalogProvider.Close()
	}
	if m.sourcePG != nil {
		m.sourcePG.Close()
	}
	if m.targetPG != nil {
		m.targetPG.Close()
	}
	for _, rp := range m.replicaPGs {
		rp.Close()
	}
}
