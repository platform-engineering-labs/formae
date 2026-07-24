// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

// mergeThemeFiles returns base overlaid with child: any field the child sets
// wins; fields the child leaves nil/empty are taken from base. It does not
// mutate its inputs. Name and Extends come from the child.
func mergeThemeFiles(base, child *themeFile) *themeFile {
	out := *base
	out.Name = child.Name
	out.Extends = child.Extends

	pickC := func(c, b *colorValue) *colorValue {
		if c != nil {
			return c
		}
		return b
	}
	pickS := func(c, b *string) *string {
		if c != nil {
			return c
		}
		return b
	}

	cp, bp := child.Palette, base.Palette
	out.Palette = paletteFile{
		Base: pickC(cp.Base, bp.Base), Surface: pickC(cp.Surface, bp.Surface),
		TextPrimary: pickC(cp.TextPrimary, bp.TextPrimary), TextSecondary: pickC(cp.TextSecondary, bp.TextSecondary),
		TextSubtle: pickC(cp.TextSubtle, bp.TextSubtle), Border: pickC(cp.Border, bp.Border),
		Selection: pickC(cp.Selection, bp.Selection), PrimaryAccent: pickC(cp.PrimaryAccent, bp.PrimaryAccent),
		SecondaryAccent: pickC(cp.SecondaryAccent, bp.SecondaryAccent), Error: pickC(cp.Error, bp.Error),
		ErrorSubtle: pickC(cp.ErrorSubtle, bp.ErrorSubtle), ErrorBright: pickC(cp.ErrorBright, bp.ErrorBright),
		Warning: pickC(cp.Warning, bp.Warning), Done: pickC(cp.Done, bp.Done),
		InProgress: pickC(cp.InProgress, bp.InProgress), Pending: pickC(cp.Pending, bp.Pending),
		OpCreate: pickC(cp.OpCreate, bp.OpCreate), OpUpdate: pickC(cp.OpUpdate, bp.OpUpdate),
		OpDelete: pickC(cp.OpDelete, bp.OpDelete), OpReplace: pickC(cp.OpReplace, bp.OpReplace),
		OpDetach: pickC(cp.OpDetach, bp.OpDetach), OpKeep: pickC(cp.OpKeep, bp.OpKeep),
	}

	cg, bg := child.Glyphs, base.Glyphs
	out.Glyphs = glyphsFile{
		OpCreate: pickS(cg.OpCreate, bg.OpCreate), OpUpdate: pickS(cg.OpUpdate, bg.OpUpdate),
		OpDelete: pickS(cg.OpDelete, bg.OpDelete), OpReplace: pickS(cg.OpReplace, bg.OpReplace),
		OpDetach: pickS(cg.OpDetach, bg.OpDetach), OpKeep: pickS(cg.OpKeep, bg.OpKeep),
		StatusDone: pickS(cg.StatusDone, bg.StatusDone), StatusFailed: pickS(cg.StatusFailed, bg.StatusFailed),
		StatusPending: pickS(cg.StatusPending, bg.StatusPending), StatusInProgress: pickS(cg.StatusInProgress, bg.StatusInProgress),
		StatusSkipped: pickS(cg.StatusSkipped, bg.StatusSkipped), StatusCanceled: pickS(cg.StatusCanceled, bg.StatusCanceled),
		AckDone: pickS(cg.AckDone, bg.AckDone), AckSkip: pickS(cg.AckSkip, bg.AckSkip),
		AckWarn: pickS(cg.AckWarn, bg.AckWarn), AckFail: pickS(cg.AckFail, bg.AckFail),
		TreeBranch: pickS(cg.TreeBranch, bg.TreeBranch), TreeLast: pickS(cg.TreeLast, bg.TreeLast),
		ExpandOpen: pickS(cg.ExpandOpen, bg.ExpandOpen), ExpandClosed: pickS(cg.ExpandClosed, bg.ExpandClosed),
		CheckboxOn:  pickS(cg.CheckboxOn, bg.CheckboxOn),
		CheckboxOff: pickS(cg.CheckboxOff, bg.CheckboxOff), Transition: pickS(cg.Transition, bg.Transition),
	}

	out.Prog = progressFile{
		FillDone:       pickS(child.Prog.FillDone, base.Prog.FillDone),
		FillInProgress: pickS(child.Prog.FillInProgress, base.Prog.FillInProgress),
		FillPending:    pickS(child.Prog.FillPending, base.Prog.FillPending),
		Animation:      pickS(child.Prog.Animation, base.Prog.Animation),
	}

	spin := base.Spin
	if child.Spin.Frames != nil {
		spin.Frames = child.Spin.Frames
	}
	spin.Preset = pickS(child.Spin.Preset, base.Spin.Preset)
	if child.Spin.IntervalMs != nil {
		spin.IntervalMs = child.Spin.IntervalMs
	}
	spin.StaticFrame = pickS(child.Spin.StaticFrame, base.Spin.StaticFrame)
	out.Spin = spin

	out.ConfBar = confBarFile{Color: pickS(child.ConfBar.Color, base.ConfBar.Color)}
	out.Header = headerFile{Highlight: pickS(child.Header.Highlight, base.Header.Highlight)}
	return &out
}
