# Usage

## Quick start

If pgcopydb is already running with defaults (`/tmp/pgcopydb` work directory), just run:

```bash
pgcopydb-tui
```

The tool auto-detects source and target PostgreSQL URIs from the SQLite catalog.

## CLI flags

```
pgcopydb-tui [flags]
```

| Flag                | Default          | Description                                      |
|---------------------|------------------|--------------------------------------------------|
| `--workdir`         | `/tmp/pgcopydb`  | pgcopydb work directory                          |
| `--source`          | (auto-detect)    | Source PostgreSQL URI                             |
| `--target`          | (auto-detect)    | Target PostgreSQL URI                             |
| `--replica`         | (none)           | Replica PostgreSQL URIs (repeatable)              |
| `--interval`        | `2`              | Refresh interval in seconds                       |
| `--theme`           | `dark`           | Theme name (`dark`, `light`, `solarized`) or path |
| `--pool-max-conns`  | `3`              | Max connections per PostgreSQL pool               |

## Environment variables

Environment variables are used as fallbacks when CLI flags are not provided:

| Variable                  | Maps to       |
|---------------------------|---------------|
| `PGCOPYDB_WORKDIR`        | `--workdir`   |
| `PGCOPYDB_SOURCE_PGURI`   | `--source`    |
| `PGCOPYDB_TARGET_PGURI`   | `--target`    |

## Config priority

Values are resolved in this order (highest to lowest priority):

1. CLI flags
2. Environment variables
3. Auto-detection from SQLite catalog (`{workdir}/schema/source.db`)
4. Defaults

## Examples

Monitor with explicit connections:

```bash
pgcopydb-tui \
  --source "postgres://user:pass@source:5432/mydb" \
  --target "postgres://user:pass@target:5432/mydb"
```

Monitor with a custom work directory and solarized theme:

```bash
pgcopydb-tui --workdir /data/pgcopydb --theme solarized
```

Monitor with a replica:

```bash
pgcopydb-tui \
  --replica "postgres://user:pass@replica1:5432/mydb" \
  --replica "postgres://user:pass@replica2:5432/mydb"
```

Use environment variables (same ones pgcopydb uses):

```bash
export PGCOPYDB_SOURCE_PGURI="postgres://user@source:5432/mydb"
export PGCOPYDB_TARGET_PGURI="postgres://user@target:5432/mydb"
pgcopydb-tui
```

## Keyboard shortcuts

| Key              | Action                |
|------------------|-----------------------|
| `tab`            | Next tab              |
| `shift+tab`      | Previous tab          |
| `1`–`5`          | Jump to tab           |
| `j` / `down`     | Scroll down           |
| `k` / `up`       | Scroll up             |
| `g`              | Jump to top           |
| `G`              | Jump to bottom        |
| `s`              | Cycle sort column     |
| `/`              | Enter filter mode     |
| `esc`            | Exit filter mode      |
| `?`              | Toggle help overlay   |
| `q` / `ctrl+c`   | Quit                  |
