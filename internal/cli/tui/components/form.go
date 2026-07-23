// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// HuhTheme maps the formae theme onto a huh form theme so all prompts and
// forms share the CLI's visual identity.
func HuhTheme(th *theme.Theme) *huh.Theme {
	t := huh.ThemeBase()
	p := th.Palette

	t.Focused.Base = t.Focused.Base.BorderForeground(p.Border)
	t.Focused.Title = t.Focused.Title.Foreground(p.TextPrimary).Bold(true)
	t.Focused.Description = t.Focused.Description.Foreground(p.TextSecondary)
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(p.Error)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(p.Error)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(p.SecondaryAccent)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(p.SecondaryAccent)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(p.PrimaryAccent)
	t.Focused.SelectedPrefix = t.Focused.SelectedPrefix.Foreground(p.PrimaryAccent)
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(p.TextPrimary)
	t.Focused.FocusedButton = t.Focused.FocusedButton.
		Foreground(p.TextPrimary).Background(p.SecondaryAccent).Bold(true)
	t.Focused.BlurredButton = t.Focused.BlurredButton.
		Foreground(p.TextSecondary).Background(p.Surface)
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(p.SecondaryAccent)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(p.TextSubtle)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(p.SecondaryAccent)

	t.Blurred = t.Focused
	t.Blurred.Base = t.Blurred.Base.BorderStyle(lipgloss.HiddenBorder())

	t.Help.ShortKey = t.Help.ShortKey.Foreground(p.PrimaryAccent)
	t.Help.ShortDesc = t.Help.ShortDesc.Foreground(p.TextSubtle)
	t.Help.FullKey = t.Help.FullKey.Foreground(p.PrimaryAccent)
	t.Help.FullDesc = t.Help.FullDesc.Foreground(p.TextSubtle)

	return t
}

// NewConfirm returns a themed yes/no confirmation form bound to value.
// The highlighted answer follows value's initial state (callers choose the
// default by initializing the bool); enter submits it, y/n answer directly.
// When embedding the form in a larger TUI instead of calling Run, set
// SubmitCmd/CancelCmd or watch form.State for completion.
func NewConfirm(th *theme.Theme, title, description string, value *bool) *huh.Form {
	confirm := huh.NewConfirm().
		Title(title).
		Affirmative("Yes").
		Negative("No").
		Value(value)
	if description != "" {
		confirm = confirm.Description(description)
	}
	return huh.NewForm(huh.NewGroup(confirm)).WithTheme(HuhTheme(th))
}

// RunConfirm runs a confirmation prompt and returns the user's choice.
// The error is non-nil when the user aborts (esc / ctrl+c).
func RunConfirm(th *theme.Theme, title, description string) (bool, error) {
	var ok bool
	if err := NewConfirm(th, title, description, &ok).Run(); err != nil {
		return false, err
	}
	return ok, nil
}

// NewThemedForm wraps huh.NewForm, applying the formae theme.
func NewThemedForm(th *theme.Theme, groups ...*huh.Group) *huh.Form {
	return huh.NewForm(groups...).WithTheme(HuhTheme(th))
}
