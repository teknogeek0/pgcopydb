package sysinfo

import (
	"context"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/metrics"
)

// Provider implements metrics.SystemProvider.
type Provider struct{}

func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) Collect(ctx context.Context) (*metrics.SystemStats, error) {
	return collect()
}
