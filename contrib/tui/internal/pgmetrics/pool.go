package pgmetrics

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/metrics"
)

// Provider implements metrics.PGProvider for a single PG connection.
type Provider struct {
	label    string
	pool     *pgxpool.Pool
	maxConns int32
}

// NewProvider creates a PGProvider connected to the given URI.
func NewProvider(label, uri string, maxConns int32) (*Provider, error) {
	poolCfg, err := pgxpool.ParseConfig(uri)
	if err != nil {
		return nil, fmt.Errorf("parse %s config: %w", label, err)
	}
	poolCfg.MaxConns = maxConns
	poolCfg.MinConns = 1

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", label, err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping %s: %w", label, err)
	}

	return &Provider{label: label, pool: pool, maxConns: maxConns}, nil
}

func (p *Provider) Label() string { return p.label }

func (p *Provider) ServerInfo(ctx context.Context) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var version string
	if err := p.pool.QueryRow(ctx, QueryVersion).Scan(&version); err != nil {
		return "", "", err
	}

	var uptime string
	if err := p.pool.QueryRow(ctx, QueryUptime).Scan(&uptime); err != nil {
		return version, "", err
	}

	return version, uptime, nil
}

func (p *Provider) DatabaseStats(ctx context.Context) ([]metrics.PGDatabaseStat, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	rows, err := p.pool.Query(ctx, QueryDatabaseStats)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []metrics.PGDatabaseStat
	for rows.Next() {
		var s metrics.PGDatabaseStat
		if err := rows.Scan(&s.DatName, &s.SizeBytes, &s.ActiveConns, &s.TotalConns, &s.TotalXacts, &s.CacheHitRatio); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func (p *Provider) ConnectionSummary(ctx context.Context) (*metrics.PGConnectionSummary, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	s := &metrics.PGConnectionSummary{}
	err := p.pool.QueryRow(ctx, QueryConnectionSummary).Scan(
		&s.Total, &s.Active, &s.Idle, &s.IdleInTransaction, &s.IdleInTxAborted, &s.Waiting,
	)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (p *Provider) ReplicationSlots(ctx context.Context) ([]metrics.PGReplicationSlot, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	rows, err := p.pool.Query(ctx, QueryReplicationSlots)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []metrics.PGReplicationSlot
	for rows.Next() {
		var s metrics.PGReplicationSlot
		if err := rows.Scan(
			&s.Name, &s.SlotType, &s.Active,
			&s.RestartLSN, &s.ConfirmedFlushLSN, &s.WALStatus,
			&s.SafeWALSize, &s.RetainedBytes,
		); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func (p *Provider) ReplicationStats(ctx context.Context) ([]metrics.PGReplicationStat, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	rows, err := p.pool.Query(ctx, QueryReplicationStats)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []metrics.PGReplicationStat
	for rows.Next() {
		var s metrics.PGReplicationStat
		if err := rows.Scan(
			&s.PID, &s.ApplicationName, &s.ClientAddr, &s.State,
			&s.SentLSN, &s.WriteLSN, &s.FlushLSN, &s.ReplayLSN,
			&s.WriteLag, &s.FlushLag, &s.ReplayLag,
		); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func (p *Provider) CurrentWALLSN(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var lsn string
	err := p.pool.QueryRow(ctx, QueryCurrentWALFlushLSN).Scan(&lsn)
	return lsn, err
}

func (p *Provider) Activity(ctx context.Context) ([]metrics.PGActivityRow, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	rows, err := p.pool.Query(ctx, QueryActivity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []metrics.PGActivityRow
	for rows.Next() {
		var a metrics.PGActivityRow
		if err := rows.Scan(
			&a.PID, &a.DatName, &a.UserName, &a.ApplicationName,
			&a.State, &a.WaitEventType, &a.WaitEvent,
			&a.Query, &a.DurationSeconds,
		); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func (p *Provider) Close() {
	p.pool.Close()
}
