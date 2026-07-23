// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"fmt"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/lipgloss"
)

// themeFile is the on-disk TOML shape. Palette colors are pointers so an unset
// key stays nil through an extends merge (Task 5).
type themeFile struct {
	Name    string       `toml:"name"`
	Extends string       `toml:"extends"`
	Palette paletteFile  `toml:"palette"`
	Glyphs  glyphsFile   `toml:"glyphs"`
	Prog    progressFile `toml:"progress"`
	Spin    spinnerFile  `toml:"spinner"`
	ConfBar confBarFile  `toml:"confirmation_bar"`
}

type paletteFile struct {
	Base            *colorValue `toml:"base"`
	Surface         *colorValue `toml:"surface"`
	TextPrimary     *colorValue `toml:"text_primary"`
	TextSecondary   *colorValue `toml:"text_secondary"`
	TextSubtle      *colorValue `toml:"text_subtle"`
	Border          *colorValue `toml:"border"`
	Selection       *colorValue `toml:"selection"`
	PrimaryAccent   *colorValue `toml:"primary_accent"`
	SecondaryAccent *colorValue `toml:"secondary_accent"`
	Error           *colorValue `toml:"error"`
	ErrorSubtle     *colorValue `toml:"error_subtle"`
	ErrorBright     *colorValue `toml:"error_bright"`
	Warning         *colorValue `toml:"warning"`
	Done            *colorValue `toml:"done"`
	InProgress      *colorValue `toml:"in_progress"`
	Pending         *colorValue `toml:"pending"`
	OpCreate        *colorValue `toml:"op_create"`
	OpUpdate        *colorValue `toml:"op_update"`
	OpDelete        *colorValue `toml:"op_delete"`
	OpReplace       *colorValue `toml:"op_replace"`
	OpDetach        *colorValue `toml:"op_detach"`
	OpKeep          *colorValue `toml:"op_keep"`
}

type glyphsFile struct {
	OpCreate  *string `toml:"op_create"`
	OpUpdate  *string `toml:"op_update"`
	OpDelete  *string `toml:"op_delete"`
	OpReplace *string `toml:"op_replace"`
	OpDetach  *string `toml:"op_detach"`
	OpKeep    *string `toml:"op_keep"`

	StatusDone       *string `toml:"status_done"`
	StatusFailed     *string `toml:"status_failed"`
	StatusPending    *string `toml:"status_pending"`
	StatusInProgress *string `toml:"status_inprogress"`
	StatusSkipped    *string `toml:"status_skipped"`
	StatusCanceled   *string `toml:"status_canceled"`

	AckDone *string `toml:"ack_done"`
	AckSkip *string `toml:"ack_skip"`
	AckWarn *string `toml:"ack_warn"`
	AckFail *string `toml:"ack_fail"`

	TreeBranch   *string `toml:"tree_branch"`
	TreeLast     *string `toml:"tree_last"`
	ExpandOpen   *string `toml:"expand_open"`
	ExpandClosed *string `toml:"expand_closed"`
	SortMarker   *string `toml:"sort_marker"`
	CheckboxOn   *string `toml:"checkbox_on"`
	CheckboxOff  *string `toml:"checkbox_off"`
	Transition   *string `toml:"transition"`
}

type progressFile struct {
	FillDone       *string `toml:"fill_done"`
	FillInProgress *string `toml:"fill_inprogress"`
	FillPending    *string `toml:"fill_pending"`
	Animation      *string `toml:"animation"`
}

type spinnerFile struct {
	Frames      []string `toml:"frames"`
	Preset      *string  `toml:"preset"`
	IntervalMs  *int     `toml:"interval_ms"`
	StaticFrame *string  `toml:"static_frame"`
}

type confBarFile struct {
	Color *string `toml:"color"`
}

// parseThemeFile decodes a theme TOML document.
func parseThemeFile(data []byte) (*themeFile, error) {
	var f themeFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse theme: %w", err)
	}
	return &f, nil
}

// toTheme converts a fully-populated (post-merge) themeFile into a *Theme.
// Callers must ensure required fields are set (the loader guarantees this by
// merging onto the quiet root).
func (f *themeFile) toTheme() *Theme {
	adapt := func(c *colorValue) lipgloss.AdaptiveColor {
		if c == nil {
			return lipgloss.AdaptiveColor{}
		}
		return c.adaptive()
	}
	str := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	intv := func(i *int) int {
		if i == nil {
			return 0
		}
		return *i
	}

	p := f.Palette
	pal := Palette{
		Base:            adapt(p.Base),
		Surface:         adapt(p.Surface),
		TextPrimary:     adapt(p.TextPrimary),
		TextSecondary:   adapt(p.TextSecondary),
		TextSubtle:      adapt(p.TextSubtle),
		Border:          adapt(p.Border),
		Selection:       adapt(p.Selection),
		PrimaryAccent:   adapt(p.PrimaryAccent),
		SecondaryAccent: adapt(p.SecondaryAccent),
		Error:           adapt(p.Error),
		ErrorSubtle:     adapt(p.ErrorSubtle),
		ErrorBright:     adapt(p.ErrorBright),
		Warning:         adapt(p.Warning),
		Done:            adapt(p.Done),
		InProgress:      adapt(p.InProgress),
		Pending:         adapt(p.Pending),
		OpCreate:        adapt(p.OpCreate),
		OpUpdate:        adapt(p.OpUpdate),
		OpDelete:        adapt(p.OpDelete),
		OpReplace:       adapt(p.OpReplace),
		OpDetach:        adapt(p.OpDetach),
		OpKeep:          adapt(p.OpKeep),
	}
	g := f.Glyphs
	glyphs := Glyphs{
		OpCreate: str(g.OpCreate), OpUpdate: str(g.OpUpdate), OpDelete: str(g.OpDelete),
		OpReplace: str(g.OpReplace), OpDetach: str(g.OpDetach), OpKeep: str(g.OpKeep),
		StatusDone: str(g.StatusDone), StatusFailed: str(g.StatusFailed),
		StatusPending: str(g.StatusPending), StatusInProgress: str(g.StatusInProgress),
		StatusSkipped: str(g.StatusSkipped), StatusCanceled: str(g.StatusCanceled),
		AckDone: str(g.AckDone), AckSkip: str(g.AckSkip), AckWarn: str(g.AckWarn), AckFail: str(g.AckFail),
		TreeBranch: str(g.TreeBranch), TreeLast: str(g.TreeLast),
		ExpandOpen: str(g.ExpandOpen), ExpandClosed: str(g.ExpandClosed),
		SortMarker: str(g.SortMarker), CheckboxOn: str(g.CheckboxOn),
		CheckboxOff: str(g.CheckboxOff), Transition: str(g.Transition),
	}
	prog := Progress{
		FillDone: str(f.Prog.FillDone), FillInProgress: str(f.Prog.FillInProgress),
		FillPending: str(f.Prog.FillPending), Animation: str(f.Prog.Animation),
	}
	spin := Spinner{
		Frames: f.Spin.Frames, Preset: str(f.Spin.Preset),
		IntervalMs: intv(f.Spin.IntervalMs), StaticFrame: str(f.Spin.StaticFrame),
	}
	return &Theme{
		Name:            f.Name,
		Palette:         pal,
		Styles:          NewStyles(pal),
		Glyphs:          glyphs,
		Progress:        prog,
		Spinner:         spin,
		ConfirmationBar: ConfirmationBar{Color: str(f.ConfBar.Color)},
	}
}
