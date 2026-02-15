package app

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/catalog"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/metrics"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/pgmetrics"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/sysinfo"
)

func tickCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

func fetchCatalogData(provider *catalog.Provider) tea.Cmd {
	return func() tea.Msg {
		if provider == nil {
			return CatalogDataMsg{Err: nil}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		msg := CatalogDataMsg{}
		var err error

		msg.Setup, err = provider.Setup(ctx)
		if err != nil {
			msg.Err = err
			return msg
		}

		msg.Sections, err = provider.Sections(ctx)
		if err != nil {
			msg.Err = err
			return msg
		}

		msg.Tables, err = provider.Tables(ctx)
		if err != nil {
			msg.Err = err
			return msg
		}

		msg.Indexes, err = provider.Indexes(ctx)
		if err != nil {
			msg.Err = err
			return msg
		}

		msg.Summaries, err = provider.Summaries(ctx)
		if err != nil {
			msg.Err = err
			return msg
		}

		msg.Timings, err = provider.Timings(ctx)
		if err != nil {
			msg.Err = err
			return msg
		}

		msg.Sentinel, _ = provider.Sentinel(ctx) // sentinel may not exist

		msg.Processes, err = provider.ActiveProcesses(ctx)
		if err != nil {
			msg.Err = err
			return msg
		}

		return msg
	}
}

func fetchSourcePG(provider *pgmetrics.Provider) tea.Cmd {
	return func() tea.Msg {
		if provider == nil {
			return SourcePGMsg{}
		}

		ctx := context.Background()
		msg := SourcePGMsg{}

		msg.Version, msg.Uptime, msg.Err = provider.ServerInfo(ctx)
		if msg.Err != nil {
			return msg
		}

		msg.Databases, msg.Err = provider.DatabaseStats(ctx)
		if msg.Err != nil {
			return msg
		}

		msg.Conns, msg.Err = provider.ConnectionSummary(ctx)
		if msg.Err != nil {
			return msg
		}

		msg.Slots, msg.Err = provider.ReplicationSlots(ctx)
		if msg.Err != nil {
			return msg
		}

		msg.ReplStats, msg.Err = provider.ReplicationStats(ctx)
		if msg.Err != nil {
			return msg
		}

		msg.WALLSN, msg.Err = provider.CurrentWALLSN(ctx)
		if msg.Err != nil {
			return msg
		}

		msg.Activity, msg.Err = provider.Activity(ctx)
		return msg
	}
}

func fetchTargetPG(provider *pgmetrics.Provider) tea.Cmd {
	return func() tea.Msg {
		if provider == nil {
			return TargetPGMsg{}
		}

		ctx := context.Background()
		msg := TargetPGMsg{}

		msg.Version, msg.Uptime, msg.Err = provider.ServerInfo(ctx)
		if msg.Err != nil {
			return msg
		}

		msg.Databases, msg.Err = provider.DatabaseStats(ctx)
		if msg.Err != nil {
			return msg
		}

		msg.Conns, msg.Err = provider.ConnectionSummary(ctx)
		if msg.Err != nil {
			return msg
		}

		msg.Activity, msg.Err = provider.Activity(ctx)
		return msg
	}
}

func fetchSystemStats(provider metrics.SystemProvider) tea.Cmd {
	return func() tea.Msg {
		if provider == nil {
			return SystemMsg{}
		}
		stats, err := provider.Collect(context.Background())
		return SystemMsg{Stats: stats, Err: err}
	}
}

// fetchSystemStatsProvider wraps the sysinfo.Provider for the command.
func fetchSystemStatsCmd(provider *sysinfo.Provider) tea.Cmd {
	return fetchSystemStats(provider)
}
