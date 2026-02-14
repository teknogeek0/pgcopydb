package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/app"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/catalog"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/config"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/pgmetrics"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/sysinfo"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/theme"
)

var (
	Version = "dev"
	cfg     = config.New()
)

var rootCmd = &cobra.Command{
	Use:   "pgcopydb-tui",
	Short: "TUI monitoring dashboard for pgcopydb migrations",
	RunE:  run,
}

func init() {
	f := rootCmd.Flags()
	f.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "pgcopydb work directory")
	f.StringVar(&cfg.SourceURI, "source", "", "source PostgreSQL URI")
	f.StringVar(&cfg.TargetURI, "target", "", "target PostgreSQL URI")
	f.StringSliceVar(&cfg.ReplicaURIs, "replica", nil, "replica PostgreSQL URIs (repeatable)")
	f.IntVar(&cfg.Interval, "interval", cfg.Interval, "refresh interval in seconds")
	f.StringVar(&cfg.Theme, "theme", cfg.Theme, "theme name or path (dark, light, solarized, or file path)")
	f.Int32Var(&cfg.PoolMaxConns, "pool-max-conns", cfg.PoolMaxConns, "max connections per PG pool")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	cfg.ApplyDefaults()

	// Load theme
	th, err := theme.Resolve(cfg.Theme)
	if err != nil {
		return fmt.Errorf("load theme: %w", err)
	}

	// Open SQLite catalog
	var catalogProvider *catalog.Provider
	catalogProvider, err = catalog.NewProvider(cfg.CatalogPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not open catalog: %v\n", err)
		// Continue without catalog - PG metrics still work
	}

	// Auto-detect source/target from catalog if available
	if catalogProvider != nil {
		if detectErr := cfg.AutoDetect(); detectErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: auto-detect failed: %v\n", detectErr)
		}
	}

	// Connect to source PG
	var sourcePG *pgmetrics.Provider
	if cfg.SourceURI != "" {
		sourcePG, err = pgmetrics.NewProvider("source", cfg.SourceURI, cfg.PoolMaxConns)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not connect to source: %v\n", err)
		}
	}

	// Connect to target PG
	var targetPG *pgmetrics.Provider
	if cfg.TargetURI != "" {
		targetPG, err = pgmetrics.NewProvider("target", cfg.TargetURI, cfg.PoolMaxConns)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not connect to target: %v\n", err)
		}
	}

	// Connect to replicas
	var replicaPGs []*pgmetrics.Provider
	for i, uri := range cfg.ReplicaURIs {
		label := fmt.Sprintf("replica-%d", i+1)
		rp, err := pgmetrics.NewProvider(label, uri, cfg.PoolMaxConns)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not connect to %s: %v\n", label, err)
			continue
		}
		replicaPGs = append(replicaPGs, rp)
	}

	// System provider
	sysProv := sysinfo.NewProvider()

	// Build model
	model := app.NewModel(cfg, th, catalogProvider, sourcePG, targetPG, replicaPGs, sysProv, Version)

	// Run bubbletea
	p := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	// Cleanup
	if m, ok := finalModel.(*app.Model); ok {
		m.Cleanup()
	}

	return nil
}
