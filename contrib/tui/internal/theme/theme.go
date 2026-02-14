package theme

import (
	"embed"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

//go:embed themes/*.yaml
var embeddedThemes embed.FS

// ThemeConfig is the YAML structure of a theme file.
type ThemeConfig struct {
	Palette    map[string]string `yaml:"palette"`
	Components ComponentsConfig  `yaml:"components"`
}

type ComponentsConfig struct {
	Header       HeaderConfig       `yaml:"header"`
	TabBar       TabBarConfig       `yaml:"tab_bar"`
	Content      ContentConfig      `yaml:"content"`
	StatusBar    StatusBarConfig    `yaml:"status_bar"`
	Table        TableConfig        `yaml:"table"`
	Gauge        GaugeConfig        `yaml:"gauge"`
	Checklist    ChecklistConfig    `yaml:"checklist"`
	SectionTitle SectionTitleConfig `yaml:"section_title"`
	Colors       ColorsConfig       `yaml:"colors"`
}

type HeaderConfig struct {
	Background    string `yaml:"background"`
	Foreground    string `yaml:"foreground"`
	DimForeground string `yaml:"dim_foreground"`
	TitleBold     bool   `yaml:"title_bold"`
}

type TabBarConfig struct {
	Background string `yaml:"background"`
	ActiveFG   string `yaml:"active_fg"`
	ActiveBG   string `yaml:"active_bg"`
	InactiveFG string `yaml:"inactive_fg"`
	InactiveBG string `yaml:"inactive_bg"`
}

type ContentConfig struct {
	Foreground string `yaml:"foreground"`
	Background string `yaml:"background"`
}

type StatusBarConfig struct {
	Foreground  string `yaml:"foreground"`
	Background  string `yaml:"background"`
	ErrorFG     string `yaml:"error_fg"`
	ConnectedFG string `yaml:"connected_fg"`
}

type TableConfig struct {
	HeaderFG   string `yaml:"header_fg"`
	HeaderBG   string `yaml:"header_bg"`
	HeaderBold bool   `yaml:"header_bold"`
	RowFG      string `yaml:"row_fg"`
	RowAltBG   string `yaml:"row_alt_bg"`
	SelectedFG string `yaml:"selected_fg"`
	SelectedBG string `yaml:"selected_bg"`
}

type GaugeConfig struct {
	EmptyFG    string           `yaml:"empty_fg"`
	LabelFG    string           `yaml:"label_fg"`
	Thresholds []ThresholdEntry `yaml:"thresholds"`
}

type ThresholdEntry struct {
	Percent int    `yaml:"percent"`
	Color   string `yaml:"color"`
}

type ChecklistConfig struct {
	DoneFG    string `yaml:"done_fg"`
	ActiveFG  string `yaml:"active_fg"`
	PendingFG string `yaml:"pending_fg"`
}

type SectionTitleConfig struct {
	Foreground string `yaml:"foreground"`
	Bold       bool   `yaml:"bold"`
}

type ColorsConfig struct {
	Green   string `yaml:"green"`
	Yellow  string `yaml:"yellow"`
	Red     string `yaml:"red"`
	Cyan    string `yaml:"cyan"`
	Blue    string `yaml:"blue"`
	Magenta string `yaml:"magenta"`
	Dim     string `yaml:"dim"`
	Bright  string `yaml:"bright"`
}

// Load reads and parses a theme from a file path.
func Load(path string) (*ThemeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read theme %s: %w", path, err)
	}
	var tc ThemeConfig
	if err := yaml.Unmarshal(data, &tc); err != nil {
		return nil, fmt.Errorf("parse theme %s: %w", path, err)
	}
	return &tc, nil
}

// LoadEmbedded reads a theme from the embedded themes directory.
func LoadEmbedded(name string) (*ThemeConfig, error) {
	filename := "themes/" + name + ".yaml"
	data, err := embeddedThemes.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("embedded theme %q not found", name)
	}
	var tc ThemeConfig
	if err := yaml.Unmarshal(data, &tc); err != nil {
		return nil, fmt.Errorf("parse embedded theme %s: %w", name, err)
	}
	return &tc, nil
}

// Default returns the default dark theme.
func Default() *ThemeConfig {
	tc, err := LoadEmbedded("dark")
	if err != nil {
		// Fallback: return a minimal theme config so the app doesn't crash
		return &ThemeConfig{
			Palette: map[string]string{
				"bg_primary":   "#1a1b26",
				"bg_secondary": "#24283b",
				"fg_primary":   "#c0caf5",
				"fg_secondary": "#a9b1d6",
				"fg_dim":       "#565f89",
				"accent":       "#7aa2f7",
				"success":      "#9ece6a",
				"warning":      "#e0af68",
				"error":        "#f7768e",
				"info":         "#2ac3de",
				"magenta":      "#bb9af7",
			},
		}
	}
	return tc
}

// Resolve loads a theme by name or file path.
func Resolve(nameOrPath string) (*Theme, error) {
	var tc *ThemeConfig
	var err error

	switch strings.ToLower(nameOrPath) {
	case "dark", "light", "solarized":
		tc, err = LoadEmbedded(strings.ToLower(nameOrPath))
	default:
		// Try as file path
		if _, statErr := os.Stat(nameOrPath); statErr == nil {
			tc, err = Load(nameOrPath)
		} else {
			// Try as embedded name
			tc, err = LoadEmbedded(nameOrPath)
		}
	}

	if err != nil {
		return nil, err
	}

	return NewTheme(tc), nil
}

// resolveColor resolves a palette reference or returns the color directly.
func resolveColor(palette map[string]string, ref string) lipgloss.Color {
	if hex, ok := palette[ref]; ok {
		return lipgloss.Color(hex)
	}
	// Treat as direct hex color
	return lipgloss.Color(ref)
}
