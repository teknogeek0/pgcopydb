# Tabs

pgcopydb-tui has five tabs, each focused on a different aspect of the migration. Switch between them with `tab`/`shift+tab` or press `1`–`5` to jump directly.

## 1. Overview

The default tab. Shows a high-level summary of the entire migration.

**Content:**

- **Migration checklist** — each phase (from the `timings` table) shown as done/active/pending with duration and byte counts
- **Overall progress bar** — total bytes copied vs total table sizes
- **Data summary** — copied bytes, total bytes, average speed, ETA
- **Table count** — completed vs total tables
- **Active workers** — currently running COPY/INDEX/VACUUM processes with PIDs
- **CDC status** — sentinel LSN positions (startpos, endpos, write, flush, replay) and apply state
- **Configuration** — slot name, plugin, snapshot ID

**Data sources:** CatalogProvider (SQLite only — no PG queries needed)

## 2. Source

Detailed metrics for the source PostgreSQL server.

**Content:**

- **Server info** — PostgreSQL version, uptime
- **Database stats table** — per-database size, active/total connections, TPS, cache hit ratio
- **Connection summary** — active, idle, idle-in-transaction, waiting counts
- **WAL position** — current WAL flush LSN
- **Replication slots** — slot name, type, active status, WAL retention in bytes and estimated time, WAL status (reserved/extended/unreserved/lost)
- **Replication lag** — per-subscriber write/flush/replay lag from `pg_stat_replication`
- **Long-running queries** — active queries running longer than 5 seconds

**Data sources:** PGProvider (source), DeltaCalculator (TPS and WAL write rate)

### WAL retention tracking

For each replication slot, two values are displayed:

- **Bytes retained**: `pg_wal_lsn_diff(pg_current_wal_flush_lsn(), restart_lsn)`
- **Time estimate**: `retained_bytes / wal_write_rate_bytes_per_sec`

The WAL write rate is tracked by a DeltaCalculator watching `pg_current_wal_flush_lsn()` over successive ticks. Displayed as e.g. `4.2 GB (~2h 15m)`.

## 3. Target

Detailed metrics for the target PostgreSQL server.

**Content:**

- **Server info** — PostgreSQL version, uptime
- **Tables populated** — count of tables with completed COPY vs total, with progress bar
- **Database stats table** — same columns as Source tab
- **Connection summary** — same breakdown as Source tab
- **Average cache hit ratio** — color-coded (green >= 99%, yellow >= 90%, red otherwise)

**Data sources:** PGProvider (target) + CatalogProvider (for table counts)

## 4. Tables

Per-table progress for the data copy phase. Scrollable and sortable.

**Content:**

A table with columns:

| Column  | Description                                    |
|---------|------------------------------------------------|
| Table   | Fully-qualified table name (`schema.table`)    |
| Size    | Total table size from source                   |
| Copied  | Bytes copied so far                            |
| %       | Completion percentage                          |
| Speed   | Copy speed (bytes/sec from summary duration)   |
| ETA     | Estimated time remaining                       |
| Status  | `copying` / `done` / `pending`                 |

Tables with `exclude_data = true` are omitted. Sort by any column with `s`. Filter by table name with `/`.

**Data sources:** CatalogProvider (s_table, s_table_size, summary, process)

## 5. System

OS-level resource utilization gauges.

**Content:**

- **CPU** — percentage utilization gauge
- **Memory** — percentage gauge + used/total bytes
- **Disk** — percentage gauge + used/total bytes (root partition)
- **Network** — TX/RX rates (bytes/sec) and cumulative totals

Network rates are computed via DeltaCalculator tracking `gopsutil` counters over time.

**Data sources:** SystemProvider (gopsutil)

## Tab-dependent fetching

To minimize load on PostgreSQL servers, PG queries are only executed for the active tab:

| Active tab | Queries executed               |
|------------|--------------------------------|
| Overview   | Source PG + Target PG          |
| Source     | Source PG only                 |
| Target     | Target PG only                 |
| Tables     | None (SQLite only)             |
| System     | None (OS metrics only)         |

SQLite catalog data and system stats are always fetched on every tick regardless of active tab.
