# Architecture

## Data flow

```
SQLite (local, ~1ms)     PG Source      PG Target     PG Replica(s)    gopsutil
       |                    |               |               |              |
  CatalogProvider      PGProvider(src)  PGProvider(tgt) PGProvider(r)  SystemProvider
       |                    |               |               |              |
       +-------- tea.Cmd functions (async, return typed messages) ---------+
                                    |
                            Bubbletea message bus
                                    |
                            Model.Update()  -- dispatches on msg type, computes deltas
                                    |
                            Model.View()    -- routes to active tab renderer
                                    |
                            Tab Renderer    -- uses theme.Theme for all styles
                                    |
                            Terminal output
```

## Tick cycle

Every `--interval` seconds (default 2):

1. **Always fetched:** SQLite catalog data + system stats (gopsutil)
2. **Tab-dependent:** only query the PG instance(s) relevant to the active tab

This keeps PG load minimal — switching to the Tables tab issues zero PG queries.

## Directory structure

```
contrib/tui/
  main.go                              # Entry point
  go.mod                               # Go module definition
  Makefile                             # Build targets

  cmd/
    root.go                            # Cobra CLI, config merge, provider init, bubbletea launch

  internal/
    config/
      config.go                        # Config struct, CLI/env/YAML merge, auto-detect from SQLite

    metrics/
      provider.go                      # Interface definitions + all data structs
      delta.go                         # DeltaCalculator: per-key rate-of-change tracking
      lsn.go                           # LSN parsing ("X/Y" -> uint64), diff, WAL retention time

    catalog/
      catalog.go                       # SQLite read-only access, implements CatalogProvider
      queries.go                       # All SQL constants for SQLite

    pgmetrics/
      pool.go                          # pgxpool.Pool factory, implements PGProvider
      queries.go                       # All SQL constants for PG

    sysinfo/
      sysinfo.go                       # Provider wrapper
      sysinfo_darwin.go                # macOS implementation
      sysinfo_linux.go                 # Linux implementation

    theme/
      theme.go                         # ThemeConfig YAML parsing, Load(), Default(), Resolve()
      styles.go                        # Theme struct with pre-computed lipgloss.Style fields
      themes/
        dark.yaml                      # Default theme (embedded via embed.FS)
        light.yaml
        solarized.yaml

    app/
      model.go                         # Bubbletea Model: providers, tab state, all data fields
      messages.go                      # TickMsg, CatalogDataMsg, SourcePGMsg, TargetPGMsg, SystemMsg
      commands.go                      # tea.Cmd functions for async data fetching
      update.go                        # Update(): message dispatch, key handling, scrolling, filtering
      keymap.go                        # Key bindings
      view.go                          # View(): compose header + tabs + content + statusbar + help

    ui/
      header.go                        # Title bar: version, source/target hosts, runtime, clock
      tabs.go                          # Tab bar renderer
      statusbar.go                     # Bottom bar: keybind hints, connection status, errors

      components/
        gauge.go                       # Progress bar with color thresholds
        table.go                       # Scrollable table with sorting
        checklist.go                   # Migration step renderer: checkmark/spinner/pending

      overview/
        view.go                        # Checklist, progress bar, data/ETA, CDC status, config

      source/
        view.go                        # DB stats, conns, WAL, repl slots + retention, long queries

      target/
        view.go                        # DB stats, tables populated, conns, cache hit ratio

      tables/
        view.go                        # Per-table progress: size, copied, %, speed, ETA, status

      system/
        view.go                        # CPU, memory, disk, network gauges
```

## Provider interfaces

### CatalogProvider

Reads pgcopydb's SQLite catalog (`{workdir}/schema/source.db`). Opened in read-only WAL mode so it never interferes with pgcopydb's writes.

```go
type CatalogProvider interface {
    Setup(ctx)           -> *CatalogSetup
    Sections(ctx)        -> []CatalogSection
    Tables(ctx)          -> []CatalogTable
    TableParts(ctx, oid) -> []CatalogTablePart
    ActiveProcesses(ctx) -> []CatalogProcess
    Summaries(ctx)       -> []CatalogSummaryEntry
    Timings(ctx)         -> []CatalogTiming
    Sentinel(ctx)        -> *CatalogSentinel
    Close()
}
```

The struct definitions map directly to the SQLite tables defined in `src/bin/pgcopydb/catalog.c` (the `sourceDBcreateDDLs` array).

### PGProvider

One instance per PostgreSQL connection (source, target, each replica). Uses `pgxpool.Pool` for connection management.

```go
type PGProvider interface {
    Label()                 -> string
    ServerInfo(ctx)         -> (version, uptime, err)
    DatabaseStats(ctx)      -> []PGDatabaseStat
    ConnectionSummary(ctx)  -> *PGConnectionSummary
    ReplicationSlots(ctx)   -> []PGReplicationSlot
    ReplicationStats(ctx)   -> []PGReplicationStat
    CurrentWALLSN(ctx)      -> string
    Activity(ctx)           -> []PGActivityRow
    Close()
}
```

Key PG queries include:
- `pg_stat_replication` for write/flush/replay lag
- `pg_replication_slots` with `safe_wal_size` (PG13+)
- WAL retention via `pg_wal_lsn_diff(pg_current_wal_flush_lsn(), restart_lsn)`
- WAL retention time estimate via DeltaCalculator tracking LSN advancement

### SystemProvider

Collects OS-level metrics using gopsutil v4.

```go
type SystemProvider interface {
    Collect(ctx) -> *SystemStats   // CPU%, mem, disk, net TX/RX
}
```

Platform-specific implementations for macOS and Linux.

## DeltaCalculator

Tracks previous values and timestamps **per key** to compute per-second rates of change. Used for:

- **TPS**: tracking `xact_commit + xact_rollback` per database
- **Network rates**: tracking `gopsutil` byte counters
- **WAL write rate**: tracking `pg_current_wal_flush_lsn()` advancement for retention time estimates

Each key maintains its own independent previous value and timestamp, so counters from different sources don't interfere with each other.

## Bubbletea architecture

The app follows the standard Elm architecture:

1. **Model** (`model.go`) — all application state: providers, data, cursors, UI flags
2. **Messages** (`messages.go`) — typed messages for each async data fetch result
3. **Commands** (`commands.go`) — `tea.Cmd` functions that execute async and return messages
4. **Update** (`update.go`) — dispatches messages, updates state, returns new commands
5. **View** (`view.go`) — pure render function, routes to active tab's renderer

All data fetching is async via `tea.Cmd`. The model never blocks on I/O.
