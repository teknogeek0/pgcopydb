# Installation

## Prerequisites

- **Go 1.23+** — required to build the binary
- **CGO enabled** — the SQLite driver (`go-sqlite3`) requires CGO
- A C compiler (`gcc` or `clang`) must be available

On macOS, Xcode command line tools provide the C compiler. On Linux, install `gcc` or `build-essential`.

## Build from source

```bash
cd contrib/tui
make build
```

This produces a `pgcopydb-tui` binary in the current directory. The version is injected from `git describe` automatically.

To install to a custom location:

```bash
cp pgcopydb-tui /usr/local/bin/
```

## Build flags

The Makefile supports:

| Variable  | Default                      | Description              |
|-----------|------------------------------|--------------------------|
| `VERSION` | `git describe --tags`        | Version string           |
| `BINARY`  | `pgcopydb-tui`               | Output binary name       |

Example:

```bash
make build VERSION=1.0.0
```

## Dependencies

All dependencies are managed via Go modules and vendored automatically on `go mod tidy`.

| Package                           | Version    | Purpose                  |
|-----------------------------------|------------|--------------------------|
| `charmbracelet/bubbletea`         | v1.3.10    | TUI framework            |
| `charmbracelet/lipgloss`          | v1.1.0     | Terminal styling          |
| `charmbracelet/bubbles`           | v1.0.0     | Key bindings             |
| `jackc/pgx/v5`                    | v5.8.0     | PostgreSQL driver        |
| `mattn/go-sqlite3`                | v1.14.24   | SQLite driver (CGO)      |
| `shirou/gopsutil/v4`              | v4.26.1    | OS metrics               |
| `spf13/cobra`                     | v1.10.2    | CLI framework            |
| `gopkg.in/yaml.v3`               | v3.0.1     | Theme file parsing       |

## Verify

```bash
./pgcopydb-tui --help
```

Should print all available flags and exit.
