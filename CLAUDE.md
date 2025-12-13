# pgcopydb - Claude Development Guide

## Project Overview

pgcopydb is a PostgreSQL database migration and replication tool written in C that automates the `pg_dump | pg_restore` process between two running PostgreSQL servers. It enables fast parallel database cloning, Change Data Capture (CDC) with logical decoding, and online migration with continuous replication.

**Key Features:**
- Parallel database copying without intermediate file storage
- Concurrent index building (not sequential like pg_restore)
- Logical decoding with wal2json plugin for CDC
- Online migration support via `pgcopydb clone --follow`
- Schema/table filtering and partitioned copying

## Development Environment

### Prerequisites

**Required Build Dependencies:**
- `build-essential` (gcc, make)
- `postgresql-server-dev-XX` (version-specific, e.g., postgresql-server-dev-16)
- `libpq-dev, libpq5`
- `libgc-dev` (Boehm garbage collector)
- `libncurses-dev`
- `libedit-dev, libreadline-dev`
- `libssl-dev, libkrb5-dev`
- `libxslt1-dev, libxml2-dev`
- `zlib1g-dev, liblz4-dev, libzstd-dev`

**Runtime Requirements:**
- PostgreSQL client tools (pg_dump, pg_restore, psql)
- PostgreSQL 9.6+ source, 11+ target
- Logical decoding plugin (wal2json) for CDC features

### Initial Setup

```bash
# Install dependencies (Debian/Ubuntu)
sudo apt-get install -y \
  build-essential \
  postgresql-server-dev-16 \
  libpq-dev libgc-dev libncurses-dev \
  libedit-dev libssl-dev libkrb5-dev

# Build from source
make clean
make bin
sudo make install
```

## Build System

**Build Tool:** GNU Make with multi-level Makefiles

**Important Build Targets:**
```bash
make bin              # Build pgcopydb binary
make clean            # Clean all build artifacts
make install          # Install to $BINDIR
make indent           # Format code with citus_indent
make docs             # Build HTML docs and man pages
make update-docs      # Auto-update docs from CLI help text
make version          # Extract version from git tags
make tests            # Run all integration tests
make tests/pagila     # Run specific test suite
```

**Version Management:**
- Version extracted from git tags via `GIT-VERSION-GEN` script
- Semantic versioning (e.g., v0.17)
- Version embedded in binary at compile time

**Multi-Architecture Support:**
- Supports linux/amd64 and linux/arm64
- Platform-specific compiler flags for macOS, FreeBSD, Linux
- Docker multi-platform builds configured

## Testing Infrastructure

**Test Framework:** Docker Compose-based integration tests with 17 separate test suites

**Running Tests:**
```bash
make tests                      # Run all tests
make tests/pagila               # Run specific test (primary integration test)
make tests/unit                 # Run unit tests
make tests/cdc-wal2json        # Test CDC with wal2json
make tests/follow-wal2json     # Test continuous replication
PGVERSION=16 make test          # Specify PostgreSQL version
```

**Test Categories:**
- **Core:** pagila, pagila-multi-steps, pagila-standby, unit
- **Features:** blobs, extensions, filtering, timescaledb
- **CDC/Replication:** cdc-wal2json, cdc-test-decoding, follow-wal2json, follow-9.6, follow-data-only
- **Edge Cases:** cdc-endpos-between-transaction, endpos-in-multi-wal-txn, cdc-low-level

**Test Infrastructure:**
- Uses docker-compose to spin up PostgreSQL source/target instances
- Environment configuration: `tests/postgres.env`
- Automatic cleanup of containers and volumes
- 5-minute timeout per test in CI

**IMPORTANT:** Always run tests before committing changes. Do not commit if tests fail.

## Code Style and Formatting

**Style Tool:** `citus_indent` (wrapper around uncrustify)

**Before Committing:**
```bash
make indent    # Auto-format all changed files
```

**CI Enforcement:**
- Style checking runs automatically on every PR
- Uses `citus/stylechecker:no-py` container
- Pre-commit hook available in repository

**Code Standards:**
- ISO C99 with GNU extensions
- 4-space indentation
- Security hardening flags enabled
- Position-independent code

## Documentation

**Format:** Sphinx with reStructuredText (.rst files)

**Documentation Directory:** `/docs/`
- Auto-generated command reference from CLI help text
- Manual documentation for features, tutorials, design rationale
- Hosted on ReadTheDocs: https://pgcopydb.readthedocs.io/

**Updating Documentation:**
```bash
make update-docs    # Auto-update CLI help text in docs
make docs           # Build HTML and man pages locally
```

**IMPORTANT:** New features must include documentation updates. Update both:
1. Relevant `.rst` files in `/docs/` for feature documentation
2. CLI help text in source (auto-extracted via `make update-docs`)

## Key Source Files and Components

**Entry Point:**
- `src/bin/pgcopydb/main.c` - Application entry point
- `src/bin/pgcopydb/cli_root.c` - Root command dispatcher

**Core Copy Logic:**
- `src/bin/pgcopydb/copydb.c` - Main copy orchestration
- `src/bin/pgcopydb/copydb_schema.c` - Schema handling
- `src/bin/pgcopydb/catalog.c` - Database catalog queries
- `src/bin/pgcopydb/dump_restore.c` - pg_dump/pg_restore wrapper

**CDC/Streaming (Logical Decoding):**
- `src/bin/pgcopydb/ld_stream.c` - Logical decoding streaming
- `src/bin/pgcopydb/ld_transform.c` - SQL transformation
- `src/bin/pgcopydb/ld_apply.c` - Apply changes to target
- `src/bin/pgcopydb/ld_wal2json.c` - wal2json plugin support
- `src/bin/pgcopydb/follow.c` - Continuous follow mode

**Feature Components:**
- `src/bin/pgcopydb/indexes.c` - Concurrent index building
- `src/bin/pgcopydb/blobs.c` - Large object (LOB) handling
- `src/bin/pgcopydb/extensions.c` - Extension support
- `src/bin/pgcopydb/filtering.c` - Schema/table filtering
- `src/bin/pgcopydb/compare.c` - Source/target comparison

**CLI Subcommands:**
- `src/bin/pgcopydb/cli_clone_follow.c` - Online migration
- `src/bin/pgcopydb/cli_copy.c` - Copy operations
- `src/bin/pgcopydb/cli_stream.c` - Streaming/CDC commands
- `src/bin/pgcopydb/cli_compare.c` - Comparison commands

**Utilities:**
- `src/bin/pgcopydb/file_utils.c` - File operations
- `src/bin/pgcopydb/parsing_utils.c` - SQL/URI parsing
- `src/bin/pgcopydb/lock_utils.c` - Locking primitives
- `src/bin/pgcopydb/pgcmd.c` - PostgreSQL command execution
- `src/bin/pgcopydb/pgsql_timeline.c` - Timeline management

**Vendored Libraries** (`src/bin/lib/`):
- `sqlite/` - Embedded database for state tracking
- `log/` - Logging framework (log.c)
- `parson/` - JSON parsing
- `subcommands.c` - CLI argument parsing
- `uthash/` - Hash tables (header-only)
- `pg/` - PostgreSQL utilities (snprintf, dumputils, string functions)

## Common Development Workflows

### Adding a New Feature

1. Read relevant source files to understand existing patterns
2. Implement feature following existing code structure
3. Update CLI help text if adding new commands
4. Run `make indent` to format code
5. Add or update integration tests in `tests/`
6. Run `make tests` to verify all tests pass
7. Update documentation with `make update-docs` and manual `.rst` edits
8. Commit with concise message (no self-attribution per user instructions)

### Fixing a Bug

1. Identify affected component (schema, copy, CDC, indexes, etc.)
2. Add regression test to appropriate test suite if possible
3. Implement fix
4. Run `make indent`
5. Run relevant test suite: `make tests/<suite-name>`
6. Run full test suite: `make tests`
7. Commit with description of bug and fix

### Working with CDC/Logical Decoding

**Key Concepts:**
- Logical replication slots track WAL position (LSN)
- wal2json plugin converts WAL to JSON format
- `ld_stream.c` manages streaming from replication slot
- `ld_transform.c` converts JSON to SQL statements
- `ld_apply.c` applies SQL to target database

**Common Files:**
- LSN tracking and position management: `ld_stream.c`, `pgsql_timeline.c`
- WAL message parsing: `ld_wal2json.c`, `ld_test_decoding.c`
- Apply logic: `ld_apply.c`
- Follow mode orchestration: `follow.c`

### Working with SQLite State

pgcopydb uses SQLite for:
- Tracking copy progress across tables
- Storing index metadata for concurrent builds
- Managing logical decoding state (LSNs, transactions)
- Filtering rules and catalog caching

**SQLite schema definitions:** Embedded in relevant source files (catalog.c, ld_stream.c, etc.)

## Technical Considerations

### Memory Management

- Uses **Boehm-Demers-Weiser garbage collector** (libgc)
- Automatic memory management reduces leak potential
- Still requires careful resource handling for file descriptors, database connections

### Concurrency Model

- Multi-process architecture (fork-based parallelism)
- Parallel COPY operations for table data
- Concurrent index building after data load
- IPC via files, SQLite, and shared state
- See `docs/concurrency.rst` for detailed documentation

### Database Connections

- Uses libpq for all PostgreSQL communication
- Connection pooling per operation type
- Separate connections for catalog queries, COPY, index builds, logical decoding
- Connection string parsing in `parsing_utils.c`

### Error Handling

- Extensive error checking with libpq result status codes
- Custom error messages in `src/bin/lib/pg/` for libpq static errors
- Logging via `log.c` framework
- State recovery via SQLite tracking

### Security Considerations

- No SQL injection: Uses parameterized queries
- Credentials via connection strings (environment variables supported)
- No intermediate file storage of sensitive data (streams directly)
- Position-independent executable (PIE) enabled
- Stack protection and security hardening flags

## CI/CD Pipeline

**GitHub Actions Workflows:**

**Test Pipeline** (`.github/workflows/run-tests.yml`):
- Runs on pushes, PRs, manual dispatch
- Matrix: PostgreSQL 16, all 17 test suites
- Style checking with citus_indent
- Documentation build verification
- 5-minute timeout per test

**Docker Publishing** (`.github/workflows/docker-publish.yml`):
- Multi-arch builds: linux/amd64, linux/arm64
- Registry: ghcr.io
- Tags: `:latest` on main, `:vX.Y.Z` on version tags
- Image signing with cosign

**Before Pushing:**
1. Run `make indent`
2. Run `make tests`
3. Verify docs build: `make docs`
4. Check that docs are current: `make update-docs` (should show no changes)

## Git Commit Guidelines

**Per User Instructions:**
- Keep commit messages **short and concise**
- Focus on "what" and "why", not "how"
- Let the diff speak for itself
- NO self-attribution or co-author tags
- NO "Generated with Claude Code" messages
- Always run tests before committing
- Do NOT use `git add` without confirmation

## Debugging Tips

### Build Issues

```bash
# Verify PostgreSQL development headers
pg_config --includedir
pg_config --libdir

# Check compiler flags
make clean
make bin V=1    # Verbose output

# Check for missing dependencies
ldd ./pgcopydb
```

### Test Failures

```bash
# Run single test with verbose output
cd tests/pagila
bash test.sh

# Check Docker Compose logs
docker-compose -f tests/pagila/docker-compose.yml logs

# Inspect SQLite state
sqlite3 /tmp/pgcopydb/pgcopydb.db ".schema"
```

### Runtime Debugging

```bash
# Enable verbose logging
pgcopydb --verbose clone ...

# Trace PostgreSQL queries
PGOPTIONS='-c log_statement=all' pgcopydb ...

# Check logical replication slot
psql -c "SELECT * FROM pg_replication_slots;"
```

## External Resources

- **Documentation:** https://pgcopydb.readthedocs.io/
- **Repository:** https://github.com/dimitri/pgcopydb
- **Issue Tracker:** GitHub Issues
- **PostgreSQL Documentation:** https://www.postgresql.org/docs/current/
- **Logical Decoding:** https://www.postgresql.org/docs/current/logicaldecoding.html
- **wal2json Plugin:** https://github.com/eulerto/wal2json

## Quick Reference

```bash
# Development cycle
make clean && make bin           # Build
make indent                      # Format
make tests                       # Test
make install                     # Install

# Documentation
make update-docs                 # Update from CLI
make docs                        # Build docs

# Docker
make build                       # Build Docker image
docker run -it ghcr.io/dimitri/pgcopydb:latest pgcopydb --version

# Common commands
pgcopydb clone --source ... --target ...        # Clone database
pgcopydb clone --follow --source ... --target ... # Online migration
pgcopydb list tables --source ...               # List tables
pgcopydb compare schema --source ... --target ... # Compare schemas
```
