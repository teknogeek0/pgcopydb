# Development

## Prerequisites

- Go 1.23+
- CGO-compatible C compiler
- A running pgcopydb migration (or a SQLite catalog from a previous run) for testing

## Build and run

```bash
cd contrib/tui
make tidy     # resolve dependencies
make build    # build binary
make run      # build and run
```

Pass arguments via `ARGS`:

```bash
make run ARGS="--workdir /data/pgcopydb --theme light"
```

## Project layout

All application code lives under `internal/` and is not importable by external packages. The public API is just the CLI.

| Package            | Responsibility                          |
|--------------------|-----------------------------------------|
| `cmd`              | CLI flags, provider initialization      |
| `internal/config`  | Configuration merging                   |
| `internal/metrics` | Interfaces, data structs, delta, LSN    |
| `internal/catalog` | SQLite catalog reader                   |
| `internal/pgmetrics` | PostgreSQL metrics fetcher            |
| `internal/sysinfo` | OS metrics collector                    |
| `internal/theme`   | YAML theme parsing and style resolution |
| `internal/app`     | Bubbletea model, update, view           |
| `internal/ui`      | Shared UI components (header, tabs, etc)|
| `internal/ui/*`    | Tab-specific renderers                  |

## Adding a new tab

1. **Define the tab constant** in `internal/app/model.go`:
   ```go
   const (
       // ... existing tabs
       TabMyTab
       TabCount  // must be last
   )
   var TabNames = []string{..., "My Tab"}
   ```

2. **Add data fields** to the `Model` struct for your tab's data.

3. **Create a message type** in `internal/app/messages.go`.

4. **Create a fetch command** in `internal/app/commands.go`.

5. **Handle the message** in `internal/app/update.go` inside `Update()`.

6. **Wire up tab-dependent fetching** in `fetchActiveTabPG()`.

7. **Create the view** at `internal/ui/mytab/view.go` with a `Render()` function.

8. **Route to the view** in `internal/app/view.go` inside `renderContent()`.

## Adding a new PG query

1. Add the SQL constant to `internal/pgmetrics/queries.go`.
2. Add a result struct to `internal/metrics/provider.go`.
3. Add a method to the `PGProvider` interface in `internal/metrics/provider.go`.
4. Implement the method in `internal/pgmetrics/pool.go`.

## Adding a new SQLite query

1. Add the SQL constant to `internal/catalog/queries.go`.
2. Add a result struct to `internal/metrics/provider.go`.
3. Add a method to the `CatalogProvider` interface in `internal/metrics/provider.go`.
4. Implement the method in `internal/catalog/catalog.go`.

## Adding a new theme

Create a YAML file in `internal/theme/themes/` following the format documented in [themes.md](themes.md). The file is automatically embedded at build time via `//go:embed`.

To make it selectable by name (e.g. `--theme mytheme`), add the name to the switch in `theme.Resolve()`:

```go
case "dark", "light", "solarized", "mytheme":
    tc, err = LoadEmbedded(strings.ToLower(nameOrPath))
```

## SQLite catalog schema reference

The SQLite tables are defined in `src/bin/pgcopydb/catalog.c` in the `sourceDBcreateDDLs` array. Key tables used by the TUI:

| Table            | Purpose                                            |
|------------------|----------------------------------------------------|
| `setup`          | Source/target URIs, snapshot, plugin, slot          |
| `section`        | Migration phase completion states                   |
| `s_table`        | All tables with metadata                            |
| `s_table_size`   | Table sizes in bytes                                |
| `s_table_part`   | COPY partitions for split tables                    |
| `summary`        | Completed jobs with bytes/duration                  |
| `timings`        | Top-level phase timings                             |
| `sentinel`       | CDC LSN positions (startpos/endpos/write/flush/replay) |
| `process`        | Currently running workers                           |

## Testing without a live migration

You can point the TUI at a catalog from a completed migration:

```bash
pgcopydb-tui --workdir /path/to/old/pgcopydb/workdir
```

The catalog data will be static (no active processes), but all tables, summaries, and timings will render. PG connections will fail gracefully if the servers are no longer reachable — the TUI continues to show catalog data.
