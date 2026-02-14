# Themes

pgcopydb-tui supports YAML-based theming. Three themes are embedded in the binary; custom themes can be loaded from file.

## Built-in themes

| Name         | Description                              |
|--------------|------------------------------------------|
| `dark`       | Tokyo Night-inspired dark theme (default)|
| `light`      | Light theme with blue accents            |
| `solarized`  | Solarized Dark palette                   |

Select with `--theme`:

```bash
pgcopydb-tui --theme solarized
```

## Custom themes

Point `--theme` at a YAML file:

```bash
pgcopydb-tui --theme /path/to/my-theme.yaml
```

## YAML format

A theme file has two top-level keys: `palette` and `components`.

### Palette

Named colors as hex values. These are referenced by components.

```yaml
palette:
  bg_primary:    "#1a1b26"
  bg_secondary:  "#24283b"
  bg_highlight:  "#414868"
  fg_primary:    "#c0caf5"
  fg_secondary:  "#a9b1d6"
  fg_dim:        "#565f89"
  accent:        "#7aa2f7"
  success:       "#9ece6a"
  warning:       "#e0af68"
  error:         "#f7768e"
  info:          "#2ac3de"
  magenta:       "#bb9af7"
  orange:        "#ff9e64"
```

You can define any number of palette entries. Components reference them by name.

### Components

Each UI element references palette keys for its colors. You can also use direct hex values.

```yaml
components:
  header:
    background: "bg_secondary"
    foreground: "fg_primary"
    dim_foreground: "fg_secondary"
    title_bold: true

  tab_bar:
    background: "bg_secondary"
    active_fg: "bg_primary"
    active_bg: "accent"
    inactive_fg: "fg_secondary"
    inactive_bg: "bg_secondary"

  content:
    foreground: "fg_primary"
    background: "bg_primary"

  status_bar:
    foreground: "fg_dim"
    background: "bg_secondary"
    error_fg: "error"
    connected_fg: "success"

  table:
    header_fg: "fg_primary"
    header_bg: "bg_secondary"
    header_bold: true
    row_fg: "fg_primary"
    row_alt_bg: "bg_secondary"
    selected_fg: "bg_primary"
    selected_bg: "accent"

  gauge:
    empty_fg: "fg_dim"
    label_fg: "fg_secondary"
    thresholds:
      - { percent: 0,  color: "success" }
      - { percent: 70, color: "warning" }
      - { percent: 90, color: "error" }

  checklist:
    done_fg: "success"
    active_fg: "accent"
    pending_fg: "fg_dim"

  section_title:
    foreground: "accent"
    bold: true

  colors:
    green: "success"
    yellow: "warning"
    red: "error"
    cyan: "info"
    blue: "accent"
    magenta: "magenta"
    dim: "fg_dim"
    bright: "fg_primary"
```

### Gauge thresholds

The `gauge.thresholds` array defines color breakpoints for progress bars. Each entry has a `percent` (minimum) and a `color` (palette reference). The highest matching threshold wins:

```yaml
thresholds:
  - { percent: 0,  color: "success" }   # 0-69%: green
  - { percent: 70, color: "warning" }   # 70-89%: yellow
  - { percent: 90, color: "error" }     # 90-100%: red
```

## How theme resolution works

1. `theme.Resolve(nameOrPath)` is called with the `--theme` value
2. If the value is `dark`, `light`, or `solarized`, the embedded YAML is loaded via `embed.FS`
3. Otherwise, if a file exists at the path, it is loaded from disk
4. Otherwise, it tries to load as an embedded theme name
5. The YAML is parsed into a `ThemeConfig` struct
6. `NewTheme(config)` resolves all palette references into pre-computed `lipgloss.Style` values
7. The resulting `Theme` struct is passed to all UI components

Theme files are at `internal/theme/themes/` in the source tree (a symlink at `contrib/tui/themes/` points there for convenience).
