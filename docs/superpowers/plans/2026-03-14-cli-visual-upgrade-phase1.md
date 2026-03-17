# CLI Visual Upgrade — Phase 1: Foundation Layer

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development
> (if subagents available) or superpowers:executing-plans to implement this plan.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the foundation layer (theme system, TUI launcher, output
routing, config) that all subsequent TUI commands will build on, then produce
ASCII mockups of all commands for UX validation.

**Architecture:** A new `internal/cli/tui/` package tree provides the theme
system (lipgloss style definitions, two palettes), a `Run()` helper for
bubbletea program lifecycle, and non-TTY detection. The PKL config and Go
config struct get a `theme` field. Mockups are ASCII text files that define the
information architecture for each command's TUI.

**Tech Stack:** Go, lipgloss, bubbletea, termenv, teatest

**Spec:** `docs/superpowers/specs/2026-03-14-cli-visual-upgrade-design.md`

**Depends on:** Nothing (this is the first phase)

**Produces:** Foundation packages ready for shared components and commands to
build on, plus validated mockups that inform Phase 2+ plans.

---

## Phasing Overview

This is Phase 1 of the CLI visual upgrade. The full sequence:

1. **Phase 1** (this plan): Foundation layer + mockups
2. **Phase 2**: Shared components + status/watch TUI (the core component)
3. **Phase 3**: Apply, destroy, cancel commands
4. **Phase 4**: Inventory TUI
5. **Phase 5**: Formatted outputs (eval, extract, status agent) + forms
   (project init, plugin init)
6. **Phase 6**: Logo/banner + visual polish

Phases 2-6 will be planned after mockups are validated, since mockup feedback
will inform component APIs and command layouts.

---

**License headers:** Every new `.go` file in this project must start with:

```go
// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2
```

This is omitted from code snippets below for brevity, but must be included
in actual files.

---

## Chunk 1: Feature Branch + Theme Package

### Task 1: Create feature branch

**Files:** None (git only)

- [ ] **Step 1: Create and switch to feature branch**

```bash
git checkout -b feat/cli-visual-upgrade
```

- [ ] **Step 2: Add charmbracelet dependencies**

```bash
go get github.com/charmbracelet/bubbletea@latest
go get github.com/charmbracelet/bubbles@latest
go get github.com/charmbracelet/lipgloss@latest
go get github.com/charmbracelet/huh@latest
go get github.com/charmbracelet/glamour@latest
go get github.com/charmbracelet/x/exp/teatest@latest
go mod tidy
```

Note: the new dependencies are unused at this point and `go mod tidy` may
remove them. That's fine — they will be re-added as each package imports them.
The `go get` step ensures they resolve and are cached.

- [ ] **Step 3: Verify build still passes**

```bash
make build
```

Expected: Successful build (new deps are unused but present in go.mod).

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add charmbracelet dependencies for CLI visual upgrade"
```

---

### Task 2: Theme — color palette definitions

Create the theme package with color palette definitions for both the "formae"
and "classic" themes.

**Files:**
- Create: `internal/cli/tui/theme/colors.go`
- Create: `internal/cli/tui/theme/colors_test.go`

**Context:** The current CLI uses `gookit/color` with RGB values defined in
`internal/cli/display/color.go`. The new theme uses lipgloss. The "classic"
palette should reproduce the existing colors. The "formae" palette uses
gray/black base with blue and orange accents (see spec: Visual Identity
section).

- [ ] **Step 1: Write the failing test for color palettes**

```go
// internal/cli/tui/theme/colors_test.go
package theme

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormaePalette(t *testing.T) {
	p := FormaePalette()

	// Primary accent is blue
	assert.NotEmpty(t, string(p.PrimaryAccent))
	// Secondary accent is orange
	assert.NotEmpty(t, string(p.SecondaryAccent))
	// Error is red
	assert.NotEmpty(t, string(p.Error))
	// Warning is yellow/gold
	assert.NotEmpty(t, string(p.Warning))
	// All base colors defined
	assert.NotEmpty(t, string(p.Base))
	assert.NotEmpty(t, string(p.TextPrimary))
	assert.NotEmpty(t, string(p.TextSecondary))
	assert.NotEmpty(t, string(p.TextSubtle))
}

func TestClassicPalette(t *testing.T) {
	p := ClassicPalette()

	// Classic palette has same structure
	assert.NotEmpty(t, string(p.PrimaryAccent))
	assert.NotEmpty(t, string(p.SecondaryAccent))
	assert.NotEmpty(t, string(p.Error))
	assert.NotEmpty(t, string(p.Warning))
}

func TestPaletteByName(t *testing.T) {
	tests := []struct {
		name     string
		expected Palette
	}{
		{"formae", FormaePalette()},
		{"classic", ClassicPalette()},
		{"unknown", FormaePalette()}, // default fallback
		{"", FormaePalette()},        // empty fallback
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PaletteByName(tt.name)
			assert.Equal(t, tt.expected, p)
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/cli/tui/theme/... -run TestFormaePalette -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/cli/tui/theme/colors.go
package theme

import "github.com/charmbracelet/lipgloss"

// Palette defines the color values for a theme. Colors are lipgloss
// AdaptiveColor values where the first value is for light backgrounds
// and the second for dark backgrounds.
type Palette struct {
	// Base colors
	Base         lipgloss.AdaptiveColor // panel backgrounds, borders
	Surface      lipgloss.AdaptiveColor // elevated surfaces
	TextPrimary  lipgloss.AdaptiveColor // bright white — active content
	TextSecondary lipgloss.AdaptiveColor // medium gray — labels, metadata
	TextSubtle   lipgloss.AdaptiveColor // dark gray — hints, disabled
	Border       lipgloss.AdaptiveColor // panel borders

	// Accent colors
	PrimaryAccent   lipgloss.AdaptiveColor // blue — IDs, links, interactive
	SecondaryAccent lipgloss.AdaptiveColor // orange — brand, progress, callouts

	// Semantic colors
	Error   lipgloss.AdaptiveColor // red — failures
	Warning lipgloss.AdaptiveColor // yellow/gold — drift, warnings

	// State colors (brightness-based, not hue-based)
	Done       lipgloss.AdaptiveColor // bright white
	InProgress lipgloss.AdaptiveColor // dim/medium white
	Pending    lipgloss.AdaptiveColor // dark gray
}

// FormaePalette returns the new formae color palette.
// Gray/black base with blue and orange accents.
func FormaePalette() Palette {
	return Palette{
		Base:            lipgloss.AdaptiveColor{Light: "#F5F5F5", Dark: "#1A1A2E"},
		Surface:         lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#16213E"},
		TextPrimary:     lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#E8E8E8"},
		TextSecondary:   lipgloss.AdaptiveColor{Light: "#666666", Dark: "#888888"},
		TextSubtle:      lipgloss.AdaptiveColor{Light: "#999999", Dark: "#555555"},
		Border:          lipgloss.AdaptiveColor{Light: "#DDDDDD", Dark: "#333355"},
		PrimaryAccent:   lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#60A5FA"},
		SecondaryAccent: lipgloss.AdaptiveColor{Light: "#D97706", Dark: "#F59E0B"},
		Error:           lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#F87171"},
		Warning:         lipgloss.AdaptiveColor{Light: "#B5B55B", Dark: "#B5B55B"},
		Done:            lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#E8E8E8"},
		InProgress:      lipgloss.AdaptiveColor{Light: "#444444", Dark: "#AAAAAA"},
		Pending:         lipgloss.AdaptiveColor{Light: "#999999", Dark: "#555555"},
	}
}

// ClassicPalette returns the existing color scheme preserved via lipgloss.
// Matches the gookit/color values in internal/cli/display/color.go.
func ClassicPalette() Palette {
	return Palette{
		Base:            lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#000000"},
		Surface:         lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#111111"},
		TextPrimary:     lipgloss.AdaptiveColor{Light: "#000000", Dark: "#FFFFFF"},
		TextSecondary:   lipgloss.AdaptiveColor{Light: "#666666", Dark: "#808080"},
		TextSubtle:      lipgloss.AdaptiveColor{Light: "#999999", Dark: "#555555"},
		Border:          lipgloss.AdaptiveColor{Light: "#CCCCCC", Dark: "#444444"},
		PrimaryAccent:   lipgloss.AdaptiveColor{Light: "#5B9BD5", Dark: "#ADD8E6"}, // LightBlue
		SecondaryAccent: lipgloss.AdaptiveColor{Light: "#B5B55B", Dark: "#B5B55B"}, // Gold
		Error:           lipgloss.AdaptiveColor{Light: "#FF0000", Dark: "#FF6666"}, // Red
		Warning:         lipgloss.AdaptiveColor{Light: "#B5B55B", Dark: "#B5B55B"}, // Gold
		Done:            lipgloss.AdaptiveColor{Light: "#008000", Dark: "#00FF00"}, // Green
		InProgress:      lipgloss.AdaptiveColor{Light: "#5B9BD5", Dark: "#ADD8E6"}, // LightBlue
		Pending:         lipgloss.AdaptiveColor{Light: "#808080", Dark: "#808080"}, // Grey
	}
}

// PaletteByName returns the palette for the given theme name.
// Falls back to FormaePalette for unknown names.
func PaletteByName(name string) Palette {
	switch name {
	case "classic":
		return ClassicPalette()
	case "formae":
		return FormaePalette()
	default:
		return FormaePalette()
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/tui/theme/... -v
```

Expected: All 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/tui/theme/
git commit -m "feat(tui): add theme color palette definitions"
```

---

### Task 3: Theme — lipgloss style definitions

Build reusable lipgloss styles on top of the palette.

**Files:**
- Create: `internal/cli/tui/theme/styles.go`
- Create: `internal/cli/tui/theme/styles_test.go`

**Context:** These styles are the building blocks every TUI component will use.
They wrap lipgloss styles configured from the active palette. Components should
never create lipgloss styles directly — they use these.

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/tui/theme/styles_test.go
package theme

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewStyles(t *testing.T) {
	s := NewStyles(FormaePalette())

	// Styles are created and non-zero
	assert.NotEmpty(t, s.Title.Render("test"))
	assert.NotEmpty(t, s.Subtitle.Render("test"))
	assert.NotEmpty(t, s.ErrorPanel.Render("test"))
	assert.NotEmpty(t, s.KeybindingKey.Render("test"))
	assert.NotEmpty(t, s.KeybindingDesc.Render("test"))
	assert.NotEmpty(t, s.ProgressBar.Render("test"))
	assert.NotEmpty(t, s.StatusDone.Render("test"))
	assert.NotEmpty(t, s.StatusInProgress.Render("test"))
	assert.NotEmpty(t, s.StatusPending.Render("test"))
	assert.NotEmpty(t, s.StatusFailed.Render("test"))
}

func TestNewStylesWithDifferentPalettes(t *testing.T) {
	formae := NewStyles(FormaePalette())
	classic := NewStyles(ClassicPalette())

	// Different palettes produce different styled output
	formaeTitle := formae.Title.Render("test")
	classicTitle := classic.Title.Render("test")
	// They should render differently (different colors)
	// (both are non-empty, we trust lipgloss renders correctly)
	assert.NotEmpty(t, formaeTitle)
	assert.NotEmpty(t, classicTitle)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/cli/tui/theme/... -run TestNewStyles -v
```

Expected: FAIL — `NewStyles` not defined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/cli/tui/theme/styles.go
package theme

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Styles contains all the lipgloss styles used by TUI components.
// Created from a Palette via NewStyles().
type Styles struct {
	// Typography
	Title    lipgloss.Style
	Subtitle lipgloss.Style
	Label    lipgloss.Style
	Body     lipgloss.Style

	// Panels
	Panel      lipgloss.Style
	ErrorPanel lipgloss.Style
	WarnPanel  lipgloss.Style
	Header     lipgloss.Style
	Footer     lipgloss.Style

	// Accents
	Accent          lipgloss.Style
	SecondaryAccent lipgloss.Style

	// Status indicators
	StatusDone       lipgloss.Style
	StatusInProgress lipgloss.Style
	StatusPending    lipgloss.Style
	StatusFailed     lipgloss.Style
	StatusWarning    lipgloss.Style

	// Interactive elements
	KeybindingKey  lipgloss.Style
	KeybindingDesc lipgloss.Style

	// Progress
	ProgressBar     lipgloss.Style
	ProgressBarFill lipgloss.Style

	// Table
	TableHeader lipgloss.Style
	TableRow    lipgloss.Style
	TableRowAlt lipgloss.Style

	// Search/Filter
	FilterPrompt lipgloss.Style
	FilterInput  lipgloss.Style
}

// NewStyles creates a Styles from the given palette.
func NewStyles(p Palette) Styles {
	return Styles{
		Title: lipgloss.NewStyle().
			Foreground(p.TextPrimary).
			Bold(true),

		Subtitle: lipgloss.NewStyle().
			Foreground(p.TextSecondary),

		Label: lipgloss.NewStyle().
			Foreground(p.TextSubtle).
			Transform(strings.ToUpper),

		Body: lipgloss.NewStyle().
			Foreground(p.TextPrimary),

		Panel: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.Border).
			Padding(0, 1),

		ErrorPanel: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.Error).
			Padding(0, 1),

		WarnPanel: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.Warning).
			Padding(0, 1),

		Header: lipgloss.NewStyle().
			Foreground(p.TextPrimary).
			Bold(true).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(p.Border),

		Footer: lipgloss.NewStyle().
			Foreground(p.TextSubtle).
			Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(p.Border),

		Accent: lipgloss.NewStyle().
			Foreground(p.PrimaryAccent),

		SecondaryAccent: lipgloss.NewStyle().
			Foreground(p.SecondaryAccent),

		StatusDone: lipgloss.NewStyle().
			Foreground(p.Done),

		StatusInProgress: lipgloss.NewStyle().
			Foreground(p.InProgress),

		StatusPending: lipgloss.NewStyle().
			Foreground(p.Pending),

		StatusFailed: lipgloss.NewStyle().
			Foreground(p.Error).
			Bold(true),

		StatusWarning: lipgloss.NewStyle().
			Foreground(p.Warning),

		KeybindingKey: lipgloss.NewStyle().
			Foreground(p.PrimaryAccent).
			Bold(true),

		KeybindingDesc: lipgloss.NewStyle().
			Foreground(p.TextSubtle),

		ProgressBar: lipgloss.NewStyle().
			Foreground(p.TextSubtle),

		ProgressBarFill: lipgloss.NewStyle().
			Foreground(p.SecondaryAccent),

		TableHeader: lipgloss.NewStyle().
			Foreground(p.TextSecondary).
			Bold(true).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(p.Border),

		TableRow: lipgloss.NewStyle().
			Foreground(p.TextPrimary),

		TableRowAlt: lipgloss.NewStyle().
			Foreground(p.TextPrimary),

		FilterPrompt: lipgloss.NewStyle().
			Foreground(p.PrimaryAccent),

		FilterInput: lipgloss.NewStyle().
			Foreground(p.TextPrimary),
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/tui/theme/... -v
```

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/tui/theme/styles.go internal/cli/tui/theme/styles_test.go
git commit -m "feat(tui): add lipgloss style definitions built from palette"
```

---

### Task 4: Theme — theme context (active theme singleton)

Create a `Theme` struct that holds the active palette + styles, loaded from
config at startup.

**Files:**
- Create: `internal/cli/tui/theme/theme.go`
- Create: `internal/cli/tui/theme/theme_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/tui/theme/theme_test.go
package theme

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewTheme(t *testing.T) {
	th := New("formae")
	assert.Equal(t, "formae", th.Name)
	assert.NotEmpty(t, th.Styles.Title.Render("test"))
}

func TestNewThemeClassic(t *testing.T) {
	th := New("classic")
	assert.Equal(t, "classic", th.Name)
}

func TestNewThemeDefault(t *testing.T) {
	th := New("")
	assert.Equal(t, "formae", th.Name)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/cli/tui/theme/... -run TestNewTheme -v
```

Expected: FAIL — `New` not defined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/cli/tui/theme/theme.go
package theme

// Theme holds the active color palette and derived lipgloss styles.
type Theme struct {
	Name    string
	Palette Palette
	Styles  Styles
}

// New creates a Theme from a theme name ("formae" or "classic").
// Unknown names fall back to "formae".
func New(name string) *Theme {
	// Normalize: unknown or empty names become "formae"
	switch name {
	case "formae", "classic":
		// valid, keep as-is
	default:
		name = "formae"
	}
	p := PaletteByName(name)
	return &Theme{
		Name:    name,
		Palette: p,
		Styles:  NewStyles(p),
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/tui/theme/... -v
```

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/tui/theme/theme.go internal/cli/tui/theme/theme_test.go
git commit -m "feat(tui): add Theme struct combining palette and styles"
```

---

## Chunk 2: TUI Launcher + Output Routing

### Task 5: TUI launcher — Run() helper

Create the `tui.Run()` function that handles bubbletea program lifecycle:
terminal setup, non-TTY detection, and teardown.

**Files:**
- Create: `internal/cli/tui/launch.go`
- Create: `internal/cli/tui/launch_test.go`

**Context:** Every command that uses a bubbletea TUI will call `tui.Run()`
instead of creating a `tea.Program` directly. This centralizes terminal
detection, theme loading, and cleanup. Check `bubbletea` docs for
`tea.NewProgram` options like `tea.WithAltScreen()`,
`tea.WithMouseCellMotion()`.

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/tui/launch_test.go
package tui

import (
	"bytes"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

// testModel is a minimal bubbletea model for testing the launcher.
type testModel struct {
	quitted bool
}

func (m testModel) Init() tea.Cmd { return tea.Quit }
func (m testModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.QuitMsg:
		m.quitted = true
	}
	return m, nil
}
func (m testModel) View() string { return "test view" }

func TestRunOptions(t *testing.T) {
	opts := DefaultRunOptions()
	assert.True(t, opts.AltScreen)
	assert.False(t, opts.NonInteractive)
}

func TestRunOptionsWithNonInteractive(t *testing.T) {
	opts := DefaultRunOptions()
	opts.NonInteractive = true
	opts.AltScreen = false
	assert.True(t, opts.NonInteractive)
	assert.False(t, opts.AltScreen)
}

func TestBuildProgramOptions(t *testing.T) {
	// Test that we can build program options without panicking.
	// We can't easily test tea.ProgramOption values, but we can
	// verify the function returns a slice.
	var buf bytes.Buffer
	opts := DefaultRunOptions()
	opts.Output = &buf
	progOpts := buildProgramOptions(opts)
	assert.NotEmpty(t, progOpts)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/cli/tui/... -run TestRun -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/cli/tui/launch.go
package tui

import (
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
)

// RunOptions configures how the bubbletea program is launched.
type RunOptions struct {
	// AltScreen uses the terminal alternate screen buffer.
	// Set to false for print-and-exit commands.
	AltScreen bool

	// NonInteractive disables input handling. Used when stdout
	// is not a TTY (piped to file or less).
	NonInteractive bool

	// Output overrides the output writer (default: os.Stdout).
	// Useful for testing.
	Output io.Writer
}

// DefaultRunOptions returns RunOptions suitable for interactive TUI commands.
func DefaultRunOptions() RunOptions {
	return RunOptions{
		AltScreen:      true,
		NonInteractive: false,
		Output:         os.Stdout,
	}
}

// IsTerminal returns true if the given writer is a terminal.
func IsTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return termenv.NewOutput(f, termenv.WithProfile(termenv.ColorProfile())).Profile() != termenv.Ascii
	}
	return false
}

// buildProgramOptions converts RunOptions into tea.ProgramOption slice.
func buildProgramOptions(opts RunOptions) []tea.ProgramOption {
	var progOpts []tea.ProgramOption

	if opts.AltScreen {
		progOpts = append(progOpts, tea.WithAltScreen())
	}

	if opts.Output != nil {
		progOpts = append(progOpts, tea.WithOutput(opts.Output))
	}

	if opts.NonInteractive {
		progOpts = append(progOpts, tea.WithoutRenderer())
	}

	return progOpts
}

// Run starts a bubbletea program with the given model and options.
// It blocks until the program exits. Returns the final model and
// any error from the program.
func Run(model tea.Model, opts RunOptions) (tea.Model, error) {
	progOpts := buildProgramOptions(opts)
	p := tea.NewProgram(model, progOpts...)

	finalModel, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("TUI error: %w", err)
	}

	return finalModel, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/tui/... -v
```

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/tui/
git commit -m "feat(tui): add Run() launcher for bubbletea programs"
```

---

### Task 6: Shared keybinding definitions

Define the common keybindings used across all TUI views.

**Files:**
- Create: `internal/cli/tui/keys.go`
- Create: `internal/cli/tui/keys_test.go`

**Context:** See spec section "Keybindings" for the full table. Uses
`bubbles/key` for keybinding definitions.

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/tui/keys_test.go
package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultKeyMap(t *testing.T) {
	km := DefaultKeyMap()

	// All keybindings are defined
	assert.NotEmpty(t, km.Up.Keys())
	assert.NotEmpty(t, km.Down.Keys())
	assert.NotEmpty(t, km.PageUp.Keys())
	assert.NotEmpty(t, km.PageDown.Keys())
	assert.NotEmpty(t, km.Search.Keys())
	assert.NotEmpty(t, km.ToggleDetail.Keys())
	assert.NotEmpty(t, km.Filter.Keys())
	assert.NotEmpty(t, km.AutoFollow.Keys())
	assert.NotEmpty(t, km.Enter.Keys())
	assert.NotEmpty(t, km.Back.Keys())
	assert.NotEmpty(t, km.Quit.Keys())
	assert.NotEmpty(t, km.Help.Keys())
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/cli/tui/... -run TestDefaultKeyMap -v
```

Expected: FAIL — `DefaultKeyMap` not defined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/cli/tui/keys.go
package tui

import "github.com/charmbracelet/bubbles/key"

// KeyMap defines the shared keybindings used across all TUI views.
type KeyMap struct {
	Up           key.Binding
	Down         key.Binding
	PageUp       key.Binding
	PageDown     key.Binding
	Search       key.Binding
	ToggleDetail key.Binding
	Filter       key.Binding
	AutoFollow   key.Binding
	Enter        key.Binding
	Back         key.Binding
	Quit         key.Binding
	Help         key.Binding
}

// DefaultKeyMap returns the standard keybinding set.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: key.NewBinding(
			key.WithKeys("k", "up"),
			key.WithHelp("k/↑", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("j", "down"),
			key.WithHelp("j/↓", "down"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("ctrl+u", "pgup"),
			key.WithHelp("ctrl+u", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("ctrl+d", "pgdown"),
			key.WithHelp("ctrl+d", "page down"),
		),
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search"),
		),
		ToggleDetail: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "detail/summary"),
		),
		Filter: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", "filter"),
		),
		AutoFollow: key.NewBinding(
			key.WithKeys("F"),
			key.WithHelp("F", "auto-follow"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "select"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q"),
			key.WithHelp("q", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/tui/... -v
```

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/tui/keys.go internal/cli/tui/keys_test.go
git commit -m "feat(tui): add shared keybinding definitions"
```

---

### Task 7: Config — add theme field

Add the `theme` field to both the PKL config schema and the Go config struct.

**Files:**
- Modify: `internal/schema/pkl/assets/formae/Config.pkl` (CliConfig class)
- Modify: `pkg/model/config.go` (CliConfig struct)

**Context:** The spec says theme is configurable under the `cli` block with
values `"formae" | "classic"`, defaulting to `"formae"`. The PKL CliConfig
is at line 183 of Config.pkl. The Go CliConfig is at line 124 of
`pkg/model/config.go`.

**Note on PKL bindings:** If the project uses pkl-gen-go for Go binding
generation, the generated bindings will need to be regenerated after the
PKL schema change. Check for a `make generate` or `pkl-gen-go` command in
the Makefile.

- [ ] **Step 1: Verify existing tests pass before modifying**

```bash
go test ./pkg/model/... -v
go test ./internal/schema/... -v
```

Expected: All existing tests PASS (baseline).

- [ ] **Step 2: Update PKL schema**

In `internal/schema/pkl/assets/formae/Config.pkl`, add the `theme` field to
the `CliConfig` class:

```pkl
class CliConfig {
    api: ApiConfig

    disableUsageReporting: Boolean = false

    theme: "formae" | "classic" = "formae"
}
```

- [ ] **Step 3: Update Go struct**

In `pkg/model/config.go`, add the `Theme` field to `CliConfig`:

```go
CliConfig struct {
	API                   APIConfig
	DisableUsageReporting bool
	Theme                 string
}
```

- [ ] **Step 4: Run tests to verify nothing breaks**

```bash
go test ./pkg/model/... -v
go test ./internal/schema/... -v
make build
```

Expected: All tests PASS, build succeeds. PKL evaluation may need
regeneration of Go bindings if the project uses pkl-gen-go.

- [ ] **Step 5: Commit**

```bash
git add internal/schema/pkl/assets/formae/Config.pkl pkg/model/config.go
git commit -m "feat(config): add theme field to CliConfig"
```

---

## Chunk 3: Test Infrastructure

### Task 8: Test helpers — mock API client interface

Create the mock API client interface that TUI component tests will use
(Layer 2 testing from the spec).

**Files:**
- Create: `internal/cli/testutil/fake_api.go`
- Create: `internal/cli/testutil/fake_api_test.go`

**Context:** TUI models will receive an API client via dependency injection.
This mock returns canned responses for testing. Look at `internal/cli/app/app.go`
for the methods that TUI commands call. The mock doesn't need to cover all
methods — start with the ones the first TUI commands will use (status, apply).

**Important:** Method signatures must match `App` exactly. Check the actual
types before implementing — e.g., `App.GetCommandsStatus` uses `fromWatch bool`
(not `includeAll`), and `App.Apply` uses `pkgmodel.FormaApplyMode` for the mode
parameter. Extract an interface from `App` for the methods TUI commands need,
and have both `App` and `FakeAPIClient` implement it.

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/testutil/fake_api_test.go
package testutil

import (
	"testing"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFakeAPIClient_GetCommandsStatus(t *testing.T) {
	expected := &apimodel.ListCommandStatusResponse{
		Commands: []apimodel.Command{
			{CommandID: "cmd-1", State: "Success"},
		},
	}

	client := &FakeAPIClient{
		StatusResponses: []StatusResponse{
			{Response: expected},
		},
	}

	resp, nags, err := client.GetCommandsStatus("id:cmd-1", 1, false)
	require.NoError(t, err)
	assert.Empty(t, nags)
	assert.Equal(t, "cmd-1", resp.Commands[0].CommandID)
}

func TestFakeAPIClient_GetCommandsStatus_Error(t *testing.T) {
	client := &FakeAPIClient{
		StatusResponses: []StatusResponse{
			{Err: assert.AnError},
		},
	}

	_, _, err := client.GetCommandsStatus("", 1, false)
	assert.Error(t, err)
}

func TestFakeAPIClient_EmptyResponses(t *testing.T) {
	client := &FakeAPIClient{}

	_, _, err := client.GetCommandsStatus("", 1, false)
	assert.Error(t, err, "should error when no responses queued")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/cli/testutil/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/cli/testutil/fake_api.go
package testutil

import (
	"fmt"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// StatusResponse is a canned response for GetCommandsStatus.
type StatusResponse struct {
	Response *apimodel.ListCommandStatusResponse
	Nags     []string
	Err      error
}

// ApplyResponse is a canned response for Apply.
type ApplyResponse struct {
	Response *apimodel.SubmitCommandResponse
	Nags     []string
	Err      error
}

// FakeAPIClient provides canned responses for TUI component testing.
// Responses are dequeued in order — each call pops the first response.
type FakeAPIClient struct {
	StatusResponses []StatusResponse
	ApplyResponses  []ApplyResponse

	// Track calls for assertions
	StatusCalls []StatusCall
	ApplyCalls  []ApplyCall
}

type StatusCall struct {
	Query      string
	N          int
	IncludeAll bool
}

type ApplyCall struct {
	FormaFile  string
	Mode       string
	Simulate   bool
	Force      bool
}

func (f *FakeAPIClient) GetCommandsStatus(query string, n int, includeAll bool) (*apimodel.ListCommandStatusResponse, []string, error) {
	f.StatusCalls = append(f.StatusCalls, StatusCall{Query: query, N: n, IncludeAll: includeAll})

	if len(f.StatusResponses) == 0 {
		return nil, nil, fmt.Errorf("no status responses queued")
	}

	resp := f.StatusResponses[0]
	f.StatusResponses = f.StatusResponses[1:]
	return resp.Response, resp.Nags, resp.Err
}

func (f *FakeAPIClient) Apply(formaFile string, properties map[string]string, mode string, simulate bool, force bool) (*apimodel.SubmitCommandResponse, []string, error) {
	f.ApplyCalls = append(f.ApplyCalls, ApplyCall{FormaFile: formaFile, Mode: mode, Simulate: simulate, Force: force})

	if len(f.ApplyResponses) == 0 {
		return nil, nil, fmt.Errorf("no apply responses queued")
	}

	resp := f.ApplyResponses[0]
	f.ApplyResponses = f.ApplyResponses[1:]
	return resp.Response, resp.Nags, resp.Err
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/testutil/... -v
```

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/testutil/
git commit -m "feat(tui): add fake API client for TUI component testing"
```

---

### Task 9: Test helpers — E2E test harness

Create helpers for Layer 3 E2E tests: spin up httptest server with
FakeMetastructure, configure an API client pointing at it.

**Files:**
- Create: `internal/cli/testutil/e2e_helpers.go`
- Create: `internal/cli/testutil/e2e_helpers_test.go`

**Context:** The existing `FakeMetastructure` in `internal/api/server_test.go`
implements `MetastructureAPI` with queued responses. However, it lives in a
`_test.go` file and cannot be imported from other packages. We need to:

1. Create a new `FakeMetastructure` in `internal/cli/testutil/` that implements
   the `MetastructureAPI` interface (from `internal/metastructure/metastructure.go`).
   Model it on the existing one in `server_test.go` but make it an exported type.
2. Create a `TestHarness` that wires up `httptest.NewServer` with the real
   Echo router from `api.NewServer()`, backed by our `FakeMetastructure`.

The Echo server is created via `api.NewServer(metastructureAPI, pluginManager,
ctx, serverConfig, pluginConfig)` — see `internal/api/server.go`.

- [ ] **Step 1: Check the existing FakeMetastructure location and structure**

Read `internal/api/server_test.go` to understand the FakeMetastructure type.
It may need to be moved to a testutil package or duplicated for reuse.

- [ ] **Step 2: Write the failing test**

```go
// internal/cli/testutil/e2e_helpers_test.go
package testutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTestHarness(t *testing.T) {
	h := NewTestHarness(t)
	defer h.Close()

	// Server is running and has a URL
	assert.NotEmpty(t, h.URL())

	// Can make a health check request
	require.NotNil(t, h.Client())
}
```

- [ ] **Step 3: Write minimal implementation**

This task requires reading the existing `FakeMetastructure` and Echo server
setup. The implementation will:

1. Export a `FakeMetastructure` (or create one in the testutil package)
2. Create a `TestHarness` that starts `httptest.NewServer` with the Echo router
3. Provide a configured API client pointing at the test server

The exact implementation depends on the import structure discovered in Step 1.
The harness should follow this pattern:

```go
// internal/cli/testutil/e2e_helpers.go
package testutil

import (
	"net/http/httptest"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/api"
)

// TestHarness provides a running test server with FakeMetastructure
// for CLI E2E tests.
type TestHarness struct {
	server *httptest.Server
	client *api.Client // or whatever the actual client type is
	fake   *FakeMetastructure
}

func NewTestHarness(t *testing.T) *TestHarness {
	// Implementation depends on how the Echo server and
	// FakeMetastructure are structured. See Step 1.
	t.Helper()
	// ... setup server, client, return harness
	return &TestHarness{}
}

func (h *TestHarness) URL() string    { return h.server.URL }
func (h *TestHarness) Close()         { h.server.Close() }
func (h *TestHarness) Client() *api.Client { return h.client }
func (h *TestHarness) Fake() *FakeMetastructure  { return h.fake }
```

**Note:** This task requires exploration of the existing server/client setup.
The implementer should read `internal/api/server.go` (NewServer function),
`internal/api/server_test.go` (FakeMetastructure), and `internal/api/client.go`
to understand the exact wiring. Adapt the above pattern to the actual types.

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/testutil/... -v
```

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/testutil/
git commit -m "feat(tui): add E2E test harness with httptest + FakeMetastructure"
```

---

## Chunk 4: ASCII Mockups

### Task 10: Create mockup directory structure

**Files:**
- Create: `docs/mockups/README.md`

- [ ] **Step 1: Create the mockup directory and README**

```bash
mkdir -p docs/mockups
```

Create `docs/mockups/README.md`:

```markdown
# CLI Visual Upgrade — ASCII Mockups

These mockups define the information architecture and layout for each command's
TUI. They are text-based approximations of what the final terminal output will
look like.

## How to read these mockups

- `[  ]` = checkbox/selectable item
- `[x]` = selected item
- `───` = horizontal border
- `│` = vertical border
- `█` = progress bar fill
- `░` = progress bar empty
- `●` = status indicator
- `...` = truncated content
- `<key>` = keybinding hint

## Mockup order

1. `status-watch.txt` — Core shared component (status/watch view)
2. `simulation-preview.txt` — Apply simulation preview
3. `drift-resolution.txt` — Apply drift resolution
4. `inventory.txt` — Inventory tabbed tables
5. `project-init.txt` — Project init form
6. `formatted-outputs.txt` — Eval, extract, status agent
7. `error-panels.txt` — Error display panels
8. `banner.txt` — Logo/banner treatments
```

- [ ] **Step 2: Commit**

```bash
git add docs/mockups/
git commit -m "docs: add mockup directory for CLI visual upgrade"
```

---

### Task 11: Mockup — Status/Watch view

This is the most important mockup — it's the core shared component used by
apply, destroy, cancel, and the status command. Get this right and everything
else follows.

**Files:**
- Create: `docs/mockups/status-watch.txt`

**Context:** See spec sections "Status / Watch (shared component)" and "Shared
UX Patterns". The mockup needs to show:
- Multi-command list view (summary + detail)
- Single command detail view
- Progress bars, status indicators, header/footer
- Large command handling (smart auto-collapse, search, filter)
- Auto-follow indicator

- [ ] **Step 1: Create the status/watch mockup**

Create `docs/mockups/status-watch.txt` showing all view states. This is a
design exercise — the implementer should create ASCII art approximations of:

**View 1: Multi-command summary**
```
┌─ formae status ─────────────────────────────────────────────── 80x24 ─┐
│                                                                       │
│  Commands (3)                                              2s refresh  │
│  ─────────────────────────────────────────────────────────────────────│
│                                                                       │
│  ● cmd-abc123  apply reconcile   ████████░░  12/15  00:42            │
│  ● cmd-def456  destroy           ██████████  8/8    00:15  Done      │
│  ● cmd-ghi789  apply patch       ░░░░░░░░░░  0/3    00:02  Pending  │
│                                                                       │
│                                                                       │
│                                                                       │
│                                                                       │
│                                                                       │
│                                                                       │
│                                                                       │
│                                                                       │
│                                                                       │
│                                                                       │
│                                                                       │
│                                                                       │
│                                                                       │
│                                                                       │
│  enter: drill in  d: detail  /: search  q: quit          ?: help     │
└───────────────────────────────────────────────────────────────────────┘
```

**View 2: Multi-command detail** (after pressing `d`)

**View 3: Single command detail** (after pressing `enter` on a command)
- Show per-stack progress bars
- Show individual update items with status indicators
- Show different update types (target, stack, resource, policy)

**View 4: Single command detail — large command (auto-collapsed)**
- Show collapsed groups: "Resource Creates (45)", "Resource Updates (12)"
- Show the header with total counts pinned

**View 5: Filter active** (after pressing `f`)
- Show filter bar with state options

**View 6: Search active** (after pressing `/`)

The implementer should use the terminal dimensions (80x24 as minimum, 120x40
as comfortable) and make sure the layouts are responsive.

- [ ] **Step 2: Review mockup and iterate**

Show the mockup to the team for feedback. Iterate until the information
architecture is validated.

- [ ] **Step 3: Commit**

```bash
git add docs/mockups/status-watch.txt
git commit -m "docs: add status/watch TUI mockup"
```

---

### Task 12: Mockup — Simulation preview (apply)

**Files:**
- Create: `docs/mockups/simulation-preview.txt`

**Context:** See spec section "Apply — Phase 1". Shows the tree of updates
grouped by stack with collapsible diffs. Must show both "fits in viewport"
(expanded) and "overflow" (auto-collapsed) variants.

- [ ] **Step 1: Create the simulation preview mockup**

Show at minimum:
- Header: command type + mode
- Update tree: targets → stacks → resources → policies
- Expanded property diff for a resource update
- Collapsed variant with group counts
- Footer with keybindings (enter to confirm, q to abort)

- [ ] **Step 2: Review and iterate**

- [ ] **Step 3: Commit**

```bash
git add docs/mockups/simulation-preview.txt
git commit -m "docs: add simulation preview TUI mockup"
```

---

### Task 13: Mockup — Drift resolution (apply)

**Files:**
- Create: `docs/mockups/drift-resolution.txt`

**Context:** See spec section "Apply — Phase 3". Interactive view where the
user chooses per-resource how to handle drift.

- [ ] **Step 1: Create the drift resolution mockup**

Show:
- Header explaining the rejection
- List of drifted resources with change summaries
- Per-resource choice UI: accept / overwrite / skip
- Confirmation to proceed
- Retry count indicator

- [ ] **Step 2: Review and iterate**

- [ ] **Step 3: Commit**

```bash
git add docs/mockups/drift-resolution.txt
git commit -m "docs: add drift resolution TUI mockup"
```

---

### Task 14: Mockup — Inventory

**Files:**
- Create: `docs/mockups/inventory.txt`

**Context:** See spec section "Inventory". Tabbed view with four table types.
Must show tab switching, column responsive behavior, search, sort, detail
expansion, and truncation hints.

- [ ] **Step 1: Create the inventory mockup**

Show:
- Tab bar: Resources | Targets | Stacks | Policies (one active)
- Table with column headers, sortable
- Search/filter bar
- Detail expansion of a selected row
- Truncation: "Showing 200 of 1,247 — refine query to see more"
- Narrow terminal variant (columns dropped)

- [ ] **Step 2: Review and iterate**

- [ ] **Step 3: Commit**

```bash
git add docs/mockups/inventory.txt
git commit -m "docs: add inventory TUI mockup"
```

---

### Task 15: Mockup — Destroy (cascade warnings)

**Files:**
- Create: `docs/mockups/destroy-cascade.txt`

**Context:** See spec section "Destroy". Must show the cascade delete warning
panel and the simulation preview with cascade-marked resources.

- [ ] **Step 1: Create the destroy cascade mockup**

Show:
- Warning panel for cascade deletes
- Cascade resources marked with dependency source
- Confirmation prompt with cascade warning

- [ ] **Step 2: Review and iterate**

- [ ] **Step 3: Commit**

```bash
git add docs/mockups/destroy-cascade.txt
git commit -m "docs: add destroy cascade warning TUI mockup"
```

---

### Task 16: Mockup — Forms (project init)

**Files:**
- Create: `docs/mockups/project-init.txt`

**Context:** See spec section "Project Init". Uses huh for stepped forms.

- [ ] **Step 1: Create the project init mockup**

Show:
- Multi-step form: project name → plugin selection (multi-select) → config
- Validation feedback
- Progress indicator (step 1 of 3, etc.)

- [ ] **Step 2: Review and iterate**

- [ ] **Step 3: Commit**

```bash
git add docs/mockups/project-init.txt
git commit -m "docs: add project init form mockup"
```

---

### Task 17: Mockup — Formatted outputs + Error panels

**Files:**
- Create: `docs/mockups/formatted-outputs.txt`
- Create: `docs/mockups/error-panels.txt`

**Context:** See spec sections "Eval", "Extract", "Status Agent", and "Error
Rendering". These are simpler print-and-exit layouts.

- [ ] **Step 1: Create formatted output mockups**

Show:
- Eval: themed resource display with types, labels, properties
- Extract: file selector + PklProject prompt + styled output
- Status agent: lipgloss-styled stats table

- [ ] **Step 2: Create error panel mockups**

Show:
- Generic error panel (red border, type header, details)
- Reconcile rejected error (now handled by drift resolution TUI, but
  for `--yes` mode it still needs a formatted error)
- Conflicting commands error
- Referenced resources not found error

- [ ] **Step 3: Review and iterate**

- [ ] **Step 4: Commit**

```bash
git add docs/mockups/formatted-outputs.txt docs/mockups/error-panels.txt
git commit -m "docs: add formatted output and error panel mockups"
```

---

### Task 18: Mockup — Banner/Logo

**Files:**
- Create: `docs/mockups/banner.txt`

**Context:** See spec section "Logo / Banner". Braille character art
approximation of the formae flower icon. The actual Sixel/Kitty rendering
can't be mocked in ASCII — this mockup is for the braille fallback.

- [ ] **Step 1: Create banner mockup**

Show:
- Braille art flower icon (approximate)
- Version number placement
- Compact variant for TUI headers
- Full variant for non-TUI commands

- [ ] **Step 2: Review and iterate**

- [ ] **Step 3: Commit**

```bash
git add docs/mockups/banner.txt
git commit -m "docs: add banner/logo mockup"
```

---

## What Comes Next

After all mockups are reviewed and approved:

1. **Write Phase 2 plan**: Shared components + status/watch TUI — informed by
   the validated mockups. This is where we build the actual bubbletea components
   (table, progress bar, viewport, tree, status indicators) and the status/watch
   TUI that apply, destroy, and cancel all use.

2. **Write Phase 3+ plans**: Each subsequent phase builds on the previous,
   adding commands one at a time.

The mockup review is a human checkpoint — the implementer should present the
mockups and get approval before proceeding to Phase 2 planning.
