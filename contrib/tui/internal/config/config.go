package config

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	WorkDir      string
	SourceURI    string
	TargetURI    string
	ReplicaURIs  []string
	Interval     int
	Theme        string
	PoolMaxConns int32
}

func New() *Config {
	return &Config{
		WorkDir:      "/tmp/pgcopydb",
		Interval:     2,
		Theme:        "dark",
		PoolMaxConns: 3,
	}
}

// ApplyDefaults fills in missing values from environment variables.
func (c *Config) ApplyDefaults() {
	if v := os.Getenv("PGCOPYDB_WORKDIR"); v != "" && c.WorkDir == "/tmp/pgcopydb" {
		c.WorkDir = v
	}
	if v := os.Getenv("PGCOPYDB_SOURCE_PGURI"); v != "" && c.SourceURI == "" {
		c.SourceURI = v
	}
	if v := os.Getenv("PGCOPYDB_TARGET_PGURI"); v != "" && c.TargetURI == "" {
		c.TargetURI = v
	}
}

// AutoDetect reads the pgcopydb SQLite catalog to fill in source/target URIs.
func (c *Config) AutoDetect() error {
	dbPath := filepath.Join(c.WorkDir, "schema", "source.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("catalog not found at %s: is pgcopydb running?", dbPath)
	}

	db, err := sql.Open("sqlite3", dbPath+"?mode=ro&_journal_mode=WAL")
	if err != nil {
		return fmt.Errorf("open catalog: %w", err)
	}
	defer db.Close()

	var sourceURI, targetURI sql.NullString
	err = db.QueryRow("SELECT source_pg_uri, target_pg_uri FROM setup WHERE id = 1").
		Scan(&sourceURI, &targetURI)
	if err != nil {
		return fmt.Errorf("read setup: %w", err)
	}

	if c.SourceURI == "" && sourceURI.Valid {
		c.SourceURI = sourceURI.String
	}
	if c.TargetURI == "" && targetURI.Valid {
		c.TargetURI = targetURI.String
	}

	return nil
}

// CatalogPath returns the path to the source.db SQLite catalog.
func (c *Config) CatalogPath() string {
	return filepath.Join(c.WorkDir, "schema", "source.db")
}
