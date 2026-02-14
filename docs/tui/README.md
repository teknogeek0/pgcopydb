# pgcopydb-tui: Migration Monitor

Interactive terminal dashboard for real-time monitoring of pgcopydb migrations. Combines pgcopydb's internal progress data (from its SQLite catalogs) with PostgreSQL system metrics and OS-level statistics.

Lives at `contrib/tui/` inside the pgcopydb repository as a standalone Go module.

## Documentation

- [Installation](install.md) — building from source, prerequisites
- [Usage](usage.md) — CLI flags, environment variables, quick start
- [Tabs](tabs.md) — what each tab shows and where the data comes from
- [Configuration](configuration.md) — config priority, auto-detection, connection pooling
- [Themes](themes.md) — built-in themes, custom YAML themes, palette reference
- [Architecture](architecture.md) — data flow, project structure, provider interfaces
- [Development](development.md) — contributing, adding tabs, extending providers
