package app

import (
	"time"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/metrics"
)

type TickMsg time.Time

type CatalogDataMsg struct {
	Setup     *metrics.CatalogSetup
	Sections  []metrics.CatalogSection
	Tables    []metrics.CatalogTable
	Indexes   []metrics.CatalogIndex
	Summaries []metrics.CatalogSummaryEntry
	Timings   []metrics.CatalogTiming
	Sentinel  *metrics.CatalogSentinel
	Processes []metrics.CatalogProcess
	Err       error
}

type SourcePGMsg struct {
	Version    string
	Uptime     string
	Databases  []metrics.PGDatabaseStat
	Conns      *metrics.PGConnectionSummary
	Slots      []metrics.PGReplicationSlot
	ReplStats  []metrics.PGReplicationStat
	WALLSN     string
	Activity   []metrics.PGActivityRow
	Err        error
}

type TargetPGMsg struct {
	Version   string
	Uptime    string
	Databases []metrics.PGDatabaseStat
	Conns     *metrics.PGConnectionSummary
	Activity  []metrics.PGActivityRow
	Err       error
}

type SystemMsg struct {
	Stats *metrics.SystemStats
	Err   error
}
