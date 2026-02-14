package metrics

import "context"

// CatalogProvider reads pgcopydb's SQLite catalogs.
type CatalogProvider interface {
	Setup(ctx context.Context) (*CatalogSetup, error)
	Sections(ctx context.Context) ([]CatalogSection, error)
	Tables(ctx context.Context) ([]CatalogTable, error)
	TableParts(ctx context.Context, oid int64) ([]CatalogTablePart, error)
	ActiveProcesses(ctx context.Context) ([]CatalogProcess, error)
	Summaries(ctx context.Context) ([]CatalogSummaryEntry, error)
	Timings(ctx context.Context) ([]CatalogTiming, error)
	Sentinel(ctx context.Context) (*CatalogSentinel, error)
	Close() error
}

// PGProvider queries a single PostgreSQL connection (source, target, or replica).
type PGProvider interface {
	Label() string
	ServerInfo(ctx context.Context) (version string, uptime string, err error)
	DatabaseStats(ctx context.Context) ([]PGDatabaseStat, error)
	ConnectionSummary(ctx context.Context) (*PGConnectionSummary, error)
	ReplicationSlots(ctx context.Context) ([]PGReplicationSlot, error)
	ReplicationStats(ctx context.Context) ([]PGReplicationStat, error)
	CurrentWALLSN(ctx context.Context) (string, error)
	Activity(ctx context.Context) ([]PGActivityRow, error)
	Close()
}

// SystemProvider collects OS-level metrics.
type SystemProvider interface {
	Collect(ctx context.Context) (*SystemStats, error)
}

// --- Catalog structs ---

type CatalogSetup struct {
	SourcePgURI            string
	TargetPgURI            string
	Snapshot               string
	SplitTablesLargerThan  int64
	SplitMaxParts          int64
	Plugin                 string
	SlotName               string
}

type CatalogSection struct {
	Name          string
	Fetched       bool
	StartTimeEpoch int64
	DoneTimeEpoch  int64
	Duration       int64
}

type CatalogTable struct {
	OID            int64
	QName          string
	NspName        string
	RelName        string
	RelPages       int64
	RelTuples      float64
	Bytes          int64
	BytesPretty    string
	ExcludeData    bool
	PartKey        string
}

type CatalogTablePart struct {
	OID      int64
	PartNum  int
	PartCount int
	Min      int64
	Max      int64
	Count    int64
}

type CatalogProcess struct {
	PID      int64
	PSType   string
	PSTitle  string
	TableOID int64
	PartNum  int
	IndexOID int64
}

type CatalogSummaryEntry struct {
	PID            int64
	TableOID       int64
	PartNum        int
	IndexOID       int64
	ConOID         int64
	StartTimeEpoch int64
	DoneTimeEpoch  int64
	Duration       int64
	Bytes          int64
	Command        string
}

type CatalogTiming struct {
	ID              int
	Label           string
	StartTimeEpoch  int64
	DoneTimeEpoch   int64
	Duration        int64
	DurationPretty  string
	Count           int64
	Bytes           int64
	BytesPretty     string
}

type CatalogSentinel struct {
	StartPos  string
	EndPos    string
	Apply     bool
	WriteLSN  string
	FlushLSN  string
	ReplayLSN string
}

// --- PG structs ---

type PGDatabaseStat struct {
	DatName       string
	SizeBytes     int64
	ActiveConns   int64
	TotalConns    int64
	TotalXacts    int64
	CacheHitRatio float64
}

type PGConnectionSummary struct {
	Total             int
	Active            int
	Idle              int
	IdleInTransaction int
	IdleInTxAborted   int
	Waiting           int
}

type PGReplicationSlot struct {
	Name              string
	SlotType          string
	Active            bool
	RestartLSN        string
	ConfirmedFlushLSN string
	WALStatus         string
	SafeWALSize       *int64
	RetainedBytes     int64
}

type PGReplicationStat struct {
	PID          int
	ApplicationName string
	ClientAddr   string
	State        string
	SentLSN      string
	WriteLSN     string
	FlushLSN     string
	ReplayLSN    string
	WriteLag     string
	FlushLag     string
	ReplayLag    string
}

type PGActivityRow struct {
	PID             int
	DatName         string
	UserName        string
	ApplicationName string
	State           string
	WaitEventType   string
	WaitEvent       string
	Query           string
	DurationSeconds float64
}

// --- System structs ---

type SystemStats struct {
	CPUPercent  float64
	MemTotal    uint64
	MemUsed     uint64
	MemPercent  float64
	DiskTotal   uint64
	DiskUsed    uint64
	DiskPercent float64
	NetTxBytes  uint64
	NetRxBytes  uint64
}
