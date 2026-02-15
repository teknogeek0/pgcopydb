package catalog

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/metrics"
)

// Provider implements metrics.CatalogProvider by reading pgcopydb's SQLite catalog.
type Provider struct {
	db *sql.DB
}

// NewProvider opens the SQLite catalog in read-only WAL mode.
func NewProvider(dbPath string) (*Provider, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_journal_mode=WAL&_busy_timeout=1000", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open catalog %s: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping catalog: %w", err)
	}
	return &Provider{db: db}, nil
}

func (p *Provider) Setup(ctx context.Context) (*metrics.CatalogSetup, error) {
	s := &metrics.CatalogSetup{}
	err := p.db.QueryRowContext(ctx, QuerySetup).Scan(
		&s.SourcePgURI, &s.TargetPgURI, &s.Snapshot,
		&s.SplitTablesLargerThan, &s.SplitMaxParts,
		&s.Plugin, &s.SlotName,
	)
	if err != nil {
		return nil, fmt.Errorf("query setup: %w", err)
	}
	return s, nil
}

func (p *Provider) Sections(ctx context.Context) ([]metrics.CatalogSection, error) {
	rows, err := p.db.QueryContext(ctx, QuerySections)
	if err != nil {
		return nil, fmt.Errorf("query sections: %w", err)
	}
	defer rows.Close()

	var result []metrics.CatalogSection
	for rows.Next() {
		var s metrics.CatalogSection
		if err := rows.Scan(&s.Name, &s.Fetched, &s.StartTimeEpoch, &s.DoneTimeEpoch, &s.Duration); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func (p *Provider) Tables(ctx context.Context) ([]metrics.CatalogTable, error) {
	rows, err := p.db.QueryContext(ctx, QueryTables)
	if err != nil {
		return nil, fmt.Errorf("query tables: %w", err)
	}
	defer rows.Close()

	var result []metrics.CatalogTable
	for rows.Next() {
		var t metrics.CatalogTable
		if err := rows.Scan(
			&t.OID, &t.QName, &t.NspName, &t.RelName,
			&t.RelPages, &t.RelTuples, &t.Bytes, &t.BytesPretty,
			&t.ExcludeData, &t.PartKey,
		); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

func (p *Provider) Indexes(ctx context.Context) ([]metrics.CatalogIndex, error) {
	rows, err := p.db.QueryContext(ctx, QueryIndexes)
	if err != nil {
		return nil, fmt.Errorf("query indexes: %w", err)
	}
	defer rows.Close()

	var result []metrics.CatalogIndex
	for rows.Next() {
		var idx metrics.CatalogIndex
		if err := rows.Scan(
			&idx.OID, &idx.QName, &idx.RelName,
			&idx.TableOID, &idx.IsPrimary, &idx.IsUnique, &idx.Columns,
		); err != nil {
			return nil, err
		}
		result = append(result, idx)
	}
	return result, rows.Err()
}

func (p *Provider) TableParts(ctx context.Context, oid int64) ([]metrics.CatalogTablePart, error) {
	rows, err := p.db.QueryContext(ctx, QueryTableParts, oid)
	if err != nil {
		return nil, fmt.Errorf("query table parts: %w", err)
	}
	defer rows.Close()

	var result []metrics.CatalogTablePart
	for rows.Next() {
		var tp metrics.CatalogTablePart
		if err := rows.Scan(&tp.OID, &tp.PartNum, &tp.PartCount, &tp.Min, &tp.Max, &tp.Count); err != nil {
			return nil, err
		}
		result = append(result, tp)
	}
	return result, rows.Err()
}

func (p *Provider) ActiveProcesses(ctx context.Context) ([]metrics.CatalogProcess, error) {
	rows, err := p.db.QueryContext(ctx, QueryActiveProcesses)
	if err != nil {
		return nil, fmt.Errorf("query processes: %w", err)
	}
	defer rows.Close()

	var result []metrics.CatalogProcess
	for rows.Next() {
		var proc metrics.CatalogProcess
		if err := rows.Scan(&proc.PID, &proc.PSType, &proc.PSTitle, &proc.TableOID, &proc.PartNum, &proc.IndexOID); err != nil {
			return nil, err
		}
		result = append(result, proc)
	}
	return result, rows.Err()
}

func (p *Provider) Summaries(ctx context.Context) ([]metrics.CatalogSummaryEntry, error) {
	rows, err := p.db.QueryContext(ctx, QuerySummaries)
	if err != nil {
		return nil, fmt.Errorf("query summaries: %w", err)
	}
	defer rows.Close()

	var result []metrics.CatalogSummaryEntry
	for rows.Next() {
		var s metrics.CatalogSummaryEntry
		if err := rows.Scan(
			&s.PID, &s.TableOID, &s.PartNum, &s.IndexOID, &s.ConOID,
			&s.StartTimeEpoch, &s.DoneTimeEpoch, &s.Duration, &s.Bytes, &s.Command,
		); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func (p *Provider) Timings(ctx context.Context) ([]metrics.CatalogTiming, error) {
	rows, err := p.db.QueryContext(ctx, QueryTimings)
	if err != nil {
		return nil, fmt.Errorf("query timings: %w", err)
	}
	defer rows.Close()

	var result []metrics.CatalogTiming
	for rows.Next() {
		var t metrics.CatalogTiming
		if err := rows.Scan(
			&t.ID, &t.Label, &t.StartTimeEpoch, &t.DoneTimeEpoch,
			&t.Duration, &t.DurationPretty, &t.Count, &t.Bytes, &t.BytesPretty,
		); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

func (p *Provider) Sentinel(ctx context.Context) (*metrics.CatalogSentinel, error) {
	s := &metrics.CatalogSentinel{}
	err := p.db.QueryRowContext(ctx, QuerySentinel).Scan(
		&s.StartPos, &s.EndPos, &s.Apply,
		&s.WriteLSN, &s.FlushLSN, &s.ReplayLSN,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query sentinel: %w", err)
	}
	return s, nil
}

func (p *Provider) Close() error {
	return p.db.Close()
}
