package system

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dimitri/pgcopydb/contrib/tui/internal/metrics"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/theme"
	"github.com/dimitri/pgcopydb/contrib/tui/internal/ui/components"
)

func Render(th *theme.Theme, width, height int, stats *metrics.SystemStats, netDelta *metrics.DeltaCalculator) string {
	var b strings.Builder
	contentWidth := width - 4
	if contentWidth < 40 {
		contentWidth = 40
	}

	b.WriteString(th.SectionTitleStyle.Render("  System Resources") + "\n\n")

	if stats == nil {
		b.WriteString(th.DimStyle.Render("  Collecting system metrics..."))
		return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
	}

	// CPU
	b.WriteString(components.RenderGauge(th, "CPU", stats.CPUPercent, contentWidth) + "\n")

	// Memory
	b.WriteString(components.RenderGauge(th, "Mem", stats.MemPercent, contentWidth) + "\n")
	b.WriteString(fmt.Sprintf("  %s %s / %s\n",
		th.DimStyle.Render("Memory:"),
		metrics.FormatBytes(stats.MemUsed),
		metrics.FormatBytes(stats.MemTotal),
	))

	// Disk
	b.WriteString("\n" + components.RenderGauge(th, "Disk", stats.DiskPercent, contentWidth) + "\n")
	b.WriteString(fmt.Sprintf("  %s %s / %s\n",
		th.DimStyle.Render("Disk:"),
		metrics.FormatBytes(stats.DiskUsed),
		metrics.FormatBytes(stats.DiskTotal),
	))

	// Network
	b.WriteString("\n" + th.SectionTitleStyle.Render("  Network") + "\n")

	txRate := float64(0)
	rxRate := float64(0)
	if netDelta != nil {
		txRate = netDelta.Rate("net_tx", int64(stats.NetTxBytes))
		rxRate = netDelta.Rate("net_rx", int64(stats.NetRxBytes))
	}

	b.WriteString(fmt.Sprintf("  %s %s   %s %s\n",
		th.DimStyle.Render("TX:"),
		th.BrightStyle.Render(metrics.FormatBytesRate(txRate)),
		th.DimStyle.Render("RX:"),
		th.BrightStyle.Render(metrics.FormatBytesRate(rxRate)),
	))
	b.WriteString(fmt.Sprintf("  %s %s   %s %s\n",
		th.DimStyle.Render("TX Total:"),
		metrics.FormatBytes(stats.NetTxBytes),
		th.DimStyle.Render("RX Total:"),
		metrics.FormatBytes(stats.NetRxBytes),
	))

	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}
