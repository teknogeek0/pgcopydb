# Configuration

## Config struct

```go
type Config struct {
    WorkDir      string     // --workdir / PGCOPYDB_WORKDIR / default "/tmp/pgcopydb"
    SourceURI    string     // --source / PGCOPYDB_SOURCE_PGURI / auto from setup table
    TargetURI    string     // --target / PGCOPYDB_TARGET_PGURI / auto from setup table
    ReplicaURIs  []string   // --replica (repeatable)
    Interval     int        // --interval / default 2 seconds
    Theme        string     // --theme / "dark" | "light" | "solarized" | path-to-yaml
    PoolMaxConns int32      // --pool-max-conns / default 3
}
```

## Priority order

Values are resolved highest to lowest:

1. **CLI flags** — explicit `--source`, `--target`, etc.
2. **Environment variables** — `PGCOPYDB_SOURCE_PGURI`, `PGCOPYDB_TARGET_PGURI`, `PGCOPYDB_WORKDIR`
3. **Auto-detection from SQLite** — reads `source_pg_uri` and `target_pg_uri` from the `setup` table in `{workdir}/schema/source.db`
4. **Defaults** — `/tmp/pgcopydb` for workdir, 2 seconds interval, `dark` theme, 3 pool max conns

## Auto-detection

When pgcopydb is running (or has run), it creates a SQLite catalog at `{workdir}/schema/source.db`. The `setup` table contains the source and target PostgreSQL URIs that were used:

```sql
SELECT source_pg_uri, target_pg_uri FROM setup WHERE id = 1;
```

pgcopydb-tui reads this on startup to fill in any missing `--source` or `--target` flags. This means users can simply run `pgcopydb-tui` with no arguments during an active migration.

## Connection pooling

Each PostgreSQL connection (source, target, each replica) gets its own `pgxpool.Pool` with:

- `MaxConns` = `--pool-max-conns` (default 3)
- `MinConns` = 1
- Connection timeout = 5 seconds
- Query timeout = 3 seconds per query

The TUI is designed to be lightweight on the monitored servers. With the default pool size of 3, at most 3 concurrent queries run against any single server.

## SQLite access

The SQLite catalog is opened in **read-only WAL mode** with a 1-second busy timeout:

```
file:{path}?mode=ro&_journal_mode=WAL&_busy_timeout=1000
```

This ensures the TUI never blocks or interferes with pgcopydb's writes to the catalog. Only one SQLite connection is used (`MaxOpenConns = 1`).

## Refresh interval

The `--interval` flag controls how often data is fetched (default 2 seconds). Each tick:

1. **Always fetched:** SQLite catalog data + system stats
2. **Tab-dependent:** only the PG connection(s) relevant to the active tab

Lower intervals give more responsive updates but increase query load. For production monitoring, 2–5 seconds is recommended.
