package theme

import "github.com/charmbracelet/lipgloss"

// Theme holds pre-computed lipgloss styles for all UI components.
type Theme struct {
	// Header
	HeaderStyle    lipgloss.Style
	HeaderDimStyle lipgloss.Style

	// Tabs
	ActiveTabStyle   lipgloss.Style
	InactiveTabStyle lipgloss.Style
	TabBarStyle      lipgloss.Style

	// Content
	ContentStyle lipgloss.Style

	// Status bar
	StatusBarStyle  lipgloss.Style
	ErrorStyle      lipgloss.Style
	ConnectedStyle  lipgloss.Style

	// Table
	TableHeaderStyle lipgloss.Style
	TableRowStyle    lipgloss.Style
	TableRowAltStyle lipgloss.Style
	SelectedRowStyle lipgloss.Style

	// Gauge
	GaugeEmptyColor lipgloss.Color
	GaugeLabelColor lipgloss.Color
	GaugeThresholds []GaugeThreshold

	// Checklist
	CheckDoneStyle    lipgloss.Style
	CheckActiveStyle  lipgloss.Style
	CheckPendingStyle lipgloss.Style

	// Section title
	SectionTitleStyle lipgloss.Style

	// Named colors
	GreenStyle   lipgloss.Style
	YellowStyle  lipgloss.Style
	RedStyle     lipgloss.Style
	CyanStyle    lipgloss.Style
	BlueStyle    lipgloss.Style
	MagentaStyle lipgloss.Style
	DimStyle     lipgloss.Style
	BrightStyle  lipgloss.Style

	// Raw colors for programmatic use
	Green   lipgloss.Color
	Yellow  lipgloss.Color
	Red     lipgloss.Color
	Cyan    lipgloss.Color
	Blue    lipgloss.Color
	Magenta lipgloss.Color
	Accent  lipgloss.Color
	Success lipgloss.Color
	Warning lipgloss.Color
	Error   lipgloss.Color

	// Background colors
	BgPrimary   lipgloss.Color
	BgSecondary lipgloss.Color
	FgPrimary   lipgloss.Color
	FgDim       lipgloss.Color
}

type GaugeThreshold struct {
	Percent int
	Color   lipgloss.Color
}

// NewTheme converts a ThemeConfig into a fully resolved Theme.
func NewTheme(tc *ThemeConfig) *Theme {
	p := tc.Palette
	c := tc.Components

	bgPrimary := resolveColor(p, c.Content.Background)
	bgSecondary := resolveColor(p, c.Header.Background)
	fgPrimary := resolveColor(p, c.Content.Foreground)

	t := &Theme{
		BgPrimary:   bgPrimary,
		BgSecondary: bgSecondary,
		FgPrimary:   fgPrimary,
		FgDim:       resolveColor(p, c.Colors.Dim),
		Accent:      resolveColor(p, "accent"),
		Success:     resolveColor(p, c.Colors.Green),
		Warning:     resolveColor(p, c.Colors.Yellow),
		Error:       resolveColor(p, c.Colors.Red),
	}

	// Header
	t.HeaderStyle = lipgloss.NewStyle().
		Foreground(resolveColor(p, c.Header.Foreground)).
		Background(bgSecondary).
		Bold(c.Header.TitleBold).
		Padding(0, 1)

	t.HeaderDimStyle = lipgloss.NewStyle().
		Foreground(resolveColor(p, c.Header.DimForeground)).
		Background(bgSecondary).
		Padding(0, 1)

	// Tabs
	t.ActiveTabStyle = lipgloss.NewStyle().
		Foreground(resolveColor(p, c.TabBar.ActiveFG)).
		Background(resolveColor(p, c.TabBar.ActiveBG)).
		Bold(true).
		Padding(0, 2)

	t.InactiveTabStyle = lipgloss.NewStyle().
		Foreground(resolveColor(p, c.TabBar.InactiveFG)).
		Background(resolveColor(p, c.TabBar.InactiveBG)).
		Padding(0, 2)

	t.TabBarStyle = lipgloss.NewStyle().
		Background(bgSecondary)

	// Content
	t.ContentStyle = lipgloss.NewStyle().
		Foreground(fgPrimary).
		Padding(1, 2)

	// Status bar
	t.StatusBarStyle = lipgloss.NewStyle().
		Foreground(resolveColor(p, c.StatusBar.Foreground)).
		Background(resolveColor(p, c.StatusBar.Background)).
		Padding(0, 1)

	t.ErrorStyle = lipgloss.NewStyle().
		Foreground(resolveColor(p, c.StatusBar.ErrorFG)).
		Bold(true)

	t.ConnectedStyle = lipgloss.NewStyle().
		Foreground(resolveColor(p, c.StatusBar.ConnectedFG))

	// Table
	t.TableHeaderStyle = lipgloss.NewStyle().
		Foreground(resolveColor(p, c.Table.HeaderFG)).
		Background(resolveColor(p, c.Table.HeaderBG)).
		Bold(c.Table.HeaderBold).
		Padding(0, 1)

	t.TableRowStyle = lipgloss.NewStyle().
		Foreground(resolveColor(p, c.Table.RowFG)).
		Padding(0, 1)

	t.TableRowAltStyle = lipgloss.NewStyle().
		Foreground(resolveColor(p, c.Table.RowFG)).
		Background(resolveColor(p, c.Table.RowAltBG)).
		Padding(0, 1)

	t.SelectedRowStyle = lipgloss.NewStyle().
		Foreground(resolveColor(p, c.Table.SelectedFG)).
		Background(resolveColor(p, c.Table.SelectedBG)).
		Bold(true).
		Padding(0, 1)

	// Gauge
	t.GaugeEmptyColor = resolveColor(p, c.Gauge.EmptyFG)
	t.GaugeLabelColor = resolveColor(p, c.Gauge.LabelFG)
	for _, th := range c.Gauge.Thresholds {
		t.GaugeThresholds = append(t.GaugeThresholds, GaugeThreshold{
			Percent: th.Percent,
			Color:   resolveColor(p, th.Color),
		})
	}

	// Checklist
	t.CheckDoneStyle = lipgloss.NewStyle().Foreground(resolveColor(p, c.Checklist.DoneFG))
	t.CheckActiveStyle = lipgloss.NewStyle().Foreground(resolveColor(p, c.Checklist.ActiveFG))
	t.CheckPendingStyle = lipgloss.NewStyle().Foreground(resolveColor(p, c.Checklist.PendingFG))

	// Section title
	t.SectionTitleStyle = lipgloss.NewStyle().
		Foreground(resolveColor(p, c.SectionTitle.Foreground)).
		Bold(c.SectionTitle.Bold).
		MarginBottom(1)

	// Named color styles
	t.Green = resolveColor(p, c.Colors.Green)
	t.Yellow = resolveColor(p, c.Colors.Yellow)
	t.Red = resolveColor(p, c.Colors.Red)
	t.Cyan = resolveColor(p, c.Colors.Cyan)
	t.Blue = resolveColor(p, c.Colors.Blue)
	t.Magenta = resolveColor(p, c.Colors.Magenta)

	t.GreenStyle = lipgloss.NewStyle().Foreground(t.Green)
	t.YellowStyle = lipgloss.NewStyle().Foreground(t.Yellow)
	t.RedStyle = lipgloss.NewStyle().Foreground(t.Red)
	t.CyanStyle = lipgloss.NewStyle().Foreground(t.Cyan)
	t.BlueStyle = lipgloss.NewStyle().Foreground(t.Blue)
	t.MagentaStyle = lipgloss.NewStyle().Foreground(t.Magenta)
	t.DimStyle = lipgloss.NewStyle().Foreground(resolveColor(p, c.Colors.Dim))
	t.BrightStyle = lipgloss.NewStyle().Foreground(resolveColor(p, c.Colors.Bright)).Bold(true)

	return t
}

// GaugeColor returns the appropriate color for a given percentage.
func (t *Theme) GaugeColor(percent float64) lipgloss.Color {
	var color lipgloss.Color
	for _, th := range t.GaugeThresholds {
		if int(percent) >= th.Percent {
			color = th.Color
		}
	}
	if string(color) == "" && len(t.GaugeThresholds) > 0 {
		color = t.GaugeThresholds[0].Color
	}
	return color
}

// Separator renders a horizontal line.
func (t *Theme) Separator(width int) string {
	style := lipgloss.NewStyle().Foreground(t.FgDim)
	line := ""
	for i := 0; i < width; i++ {
		line += "─"
	}
	return style.Render(line)
}
