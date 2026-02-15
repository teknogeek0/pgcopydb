# TUI Architecture: Data Collection Layer

## Overview

The TUI collects data from three sources, each behind a provider:

```
cmd/root.go
    |
    +-- catalog.Provider    SQLite (pgcopydb's source.db)
    +-- pgmetrics.Provider  PostgreSQL (source, target, replicas)
    +-- sysinfo.Provider    OS metrics (gopsutil)
    |
    v
app.NewModel(providers...)
    |
    v
bubbletea event loop: Init -> tickFetch -> Update -> View
```

All three can be nil — the TUI degrades gracefully, showing "Not connected"
or "Collecting..." for missing sources.

## Providers

### Catalog (`internal/catalog/`)

Reads pgcopydb's SQLite catalog in read-only WAL mode.

| File | Purpose |
|---|---|
| `catalog.go` | `Provider` struct, `NewProvider(dbPath)`, all query methods |
| `queries.go` | SQL constants targeting pgcopydb's schema |

Key tables: `setup`, `section`, `s_table`, `s_table_size`, `summary`,
`timings`, `sentinel`, `process`.

Design choices:
- Read-only mode (`mode=ro`) — TUI never writes
- WAL journal — allows reads while pgcopydb writes
- Single connection (`SetMaxOpenConns(1)`) — no pool overhead
- 1s busy timeout — waits if catalog is locked during a write

### PG Metrics (`internal/pgmetrics/`)

Queries PostgreSQL system catalogs via pgx v5 connection pool.

| File | Purpose |
|---|---|
| `pool.go` | `Provider` struct, `NewProvider(label, uri, maxConns)`, all query methods |
| `queries.go` | SQL constants targeting pg_stat_* views |

Same provider type used for source, target, and replicas — distinguished by
label. Each method has a 3-second context timeout.

Queries target:
- `pg_stat_database` — size, connections, TPS, cache hit ratio
- `pg_stat_activity` — connection states, running queries
- `pg_replication_slots` — slot status, WAL retention
- `pg_stat_replication` — standby lag
- `pg_current_wal_flush_lsn()` — WAL position

Requires PostgreSQL 13+ (uses `safe_wal_size` column in `pg_replication_slots`).

### System Info (`internal/sysinfo/`)

Collects OS-level metrics using gopsutil v4.

| File | Purpose |
|---|---|
| `sysinfo.go` | `Provider` struct, `NewProvider()`, delegates to `collect()` |
| `sysinfo_darwin.go` | macOS implementation (build tag) |
| `sysinfo_linux.go` | Linux implementation (build tag) |

Both OS files are currently identical — gopsutil abstracts the OS differences.
Separate files exist so we can add OS-specific logic later (e.g. Linux cgroups
for container-aware memory, or `/proc` parsing).

Collects: CPU%, memory (used/total), disk (used/total on `/`), network
(aggregate TX/RX bytes across all interfaces).

### Delta Calculators (`internal/metrics/delta.go`)

Track per-second rates for monotonically increasing counters.

Three instances created in `app.NewModel()`, stored as private fields:
- `tpsDelta` — transaction counts (keyed per-database)
- `netDelta` — network byte counters
- `walDelta` — WAL LSN positions

`Rate(key, newValue)` stores the value + timestamp and returns the per-second
delta since the previous call. First call always returns 0 (no previous value).

**Important: `Rate()` must only be called in `Update()`, never in `View()`.**
Each call mutates internal state (updates prev value/time). If called from
`View()` — which runs on every render frame — the elapsed time between calls
is one frame (~16ms) instead of the fetch interval (~2s). When new data
arrives, this produces `2s_of_counter_delta / 16ms_elapsed` = wildly inflated
rates.

The model computes rates in `Update()` message handlers and stores them as
plain `float64` fields (`sourceTPS`, `targetTPS`, `netRxRate`, `netTxRate`,
`walRate`). The dashboard reads these values directly — no DeltaCalculator
references cross the Update/View boundary.

## Interfaces

Three interfaces are defined in `internal/metrics/provider.go`:

```go
type CatalogProvider interface { ... }  // 8 methods
type PGProvider interface { ... }       // 8 methods
type SystemProvider interface { ... }   // 1 method (Collect)
```

Each concrete provider implements its interface via Go structural typing.

### Do we need them?

**Current state:** The model stores concrete types (`*catalog.Provider`,
`*pgmetrics.Provider`, `*sysinfo.Provider`). The interfaces exist but aren't
used as parameter types anywhere in the app layer — commands.go accepts the
concrete types directly.

**Assessment:** For a focused migration tool without unit tests in the near
term, the interfaces add structure without practical benefit right now. They'd
matter if we needed:
- Mock providers for testing
- Alternative backends (e.g. remote metrics API, container-aware sysinfo)
- Plugin architecture

**Decision: Keep the interfaces as documentation of the contract.** They cost
nothing, make the expected API explicit, and are there if we ever need them.
Don't bother switching the model/commands to use interface types — that's
refactoring for refactoring's sake with no current consumer.

## Data Flow

```
tickCmd (every N seconds)
    |
    v
tea.Batch(
    fetchCatalogData()   --> CatalogDataMsg
    fetchSourcePG()      --> SourcePGMsg
    fetchTargetPG()      --> TargetPGMsg
    fetchSystemStats()   --> SystemMsg
)
    |  (all run concurrently as goroutines)
    v
Update(msg)
    |  stores data in model fields
    |  updates DeltaCalculators
    v
View()
    |  builds dashboard.Data from model fields
    |  passes DeltaCalculators by reference
    v
dashboard.Render() --> string
```

Each fetch function is nil-safe: if the provider is nil, it returns an empty
message (no error). This is how graceful degradation works — you can run the
TUI with just a catalog and no PG connections, or vice versa.

## Current Gaps

### TODO: sysinfo improvements

- [ ] Monitor the actual pgcopydb workdir mount instead of hardcoded `/`
      (pass workdir path to `NewProvider`, resolve mount point)
- [ ] Expose load average on Linux (gopsutil `load.Avg()`)
- [ ] Add process count from OS (currently only from catalog's process table,
      which is empty when migration is done)

### TODO: pgmetrics robustness

- [ ] Handle `safe_wal_size` being NULL gracefully — the column exists in
      PG 13+ but can be NULL if `max_slot_wal_keep_size` is not set.
      Currently scanned into `*int64` (nullable) so this should be fine,
      but verify.
- [ ] Consider adding `pg_is_in_recovery()` check to skip source-only
      queries (replication slots, WAL LSN) when connected to a standby

### TODO: first-tick TPS spike

- [ ] DeltaCalculator returns 0 on first call, but TPS shows a huge number
      on the first render because `Rate()` gets called in the view layer
      (dashboard) on the second tick with a large accumulated xact count.
      Fix: either suppress display until second tick, or call `Rate()` in
      Update() instead of View() so the first-call zero happens during
      initial data load.

### TODO: catalog resilience

- [ ] If pgcopydb is still running and recreates the catalog mid-migration,
      the SQLite connection could break. Consider re-opening on error.
- [ ] The `sentinel` table may not exist in non-CDC migrations. Currently
      handled (error ignored), but worth a comment.

### Not planned (and why)

- **PG version detection / query fallbacks**: Targeting PG 13+ is sufficient.
  pgcopydb itself requires PG 10+ but the replication slot columns we use
  (`wal_status`, `safe_wal_size`) are PG 13+. If someone runs the TUI against
  PG 12, the replication slots query will fail but other queries work fine —
  the error shows in the status bar and life goes on.

- **Switching model to interface types**: No practical benefit without tests
  or alternative implementations. The interfaces serve as documentation.

- **Per-interface network stats**: Would need to know which NIC carries PG
  traffic. Not worth the complexity — aggregate is good enough for a
  migration monitor.
