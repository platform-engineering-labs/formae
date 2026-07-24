// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package extract

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/schema"
)

func TestValidateExtractOptions(t *testing.T) {
	t.Run("missing target path", func(t *testing.T) {
		opts := &ExtractOptions{
			TargetPath: "",
		}
		err := validateExtractOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "target file is required", err.Error())
	})

	t.Run("target path is a directory", func(t *testing.T) {
		dir := t.TempDir()
		opts := &ExtractOptions{
			TargetPath: dir,
			Query:      "type:AWS::S3::Bucket",
		}
		err := validateExtractOptions(opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "is a directory, not a file")
	})

	t.Run("missing query", func(t *testing.T) {
		opts := &ExtractOptions{
			TargetPath: "output.pkl",
			Query:      "",
		}
		err := validateExtractOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "query is required", err.Error())
	})

}

func TestHandleSchemaVersionUpgrade(t *testing.T) {
	th := theme.New("")
	warnStyle := lipgloss.NewStyle()

	origInteractive, origConfirm := isInteractive, runConfirm
	t.Cleanup(func() { isInteractive, runConfirm = origInteractive, origConfirm })

	// setup wires the seams and returns call counters plus an upgrade tied to
	// an Apply spy, so each case can assert whether the on-disk write happened.
	setup := func(interactive, confirmAnswer bool) (confirmCalls, applyCalls *int, u *schema.SchemaVersionUpgrade) {
		cc, ac := 0, 0
		isInteractive = func() bool { return interactive }
		runConfirm = func(_ *theme.Theme, _ string, _ string) (bool, error) { cc++; return confirmAnswer, nil }
		u = &schema.SchemaVersionUpgrade{
			ProjectDir: "/tmp/proj", Current: "0.85.0", Target: "0.88.0",
			Apply: func() ([]string, error) { ac++; return nil, nil },
		}
		return &cc, &ac, u
	}

	t.Run("--yes applies without prompting", func(t *testing.T) {
		cc, ac, u := setup(true, false)
		require.NoError(t, handleSchemaVersionUpgrade(th, warnStyle, &ExtractOptions{Yes: true}, "out.pkl", u))
		assert.Equal(t, 0, *cc, "should not prompt with --yes")
		assert.Equal(t, 1, *ac, "should apply with --yes")
	})

	t.Run("interactive + confirmed applies", func(t *testing.T) {
		cc, ac, u := setup(true, true)
		require.NoError(t, handleSchemaVersionUpgrade(th, warnStyle, &ExtractOptions{}, "out.pkl", u))
		assert.Equal(t, 1, *cc)
		assert.Equal(t, 1, *ac)
	})

	t.Run("interactive + declined nags, no write", func(t *testing.T) {
		cc, ac, u := setup(true, false)
		require.NoError(t, handleSchemaVersionUpgrade(th, warnStyle, &ExtractOptions{}, "out.pkl", u))
		assert.Equal(t, 1, *cc)
		assert.Equal(t, 0, *ac, "declined must not write")
	})

	t.Run("non-interactive without --yes nags, no prompt, no write", func(t *testing.T) {
		cc, ac, u := setup(false, false)
		require.NoError(t, handleSchemaVersionUpgrade(th, warnStyle, &ExtractOptions{}, "out.pkl", u))
		assert.Equal(t, 0, *cc, "must not prompt without a TTY")
		assert.Equal(t, 0, *ac, "must not write without consent")
	})
}
