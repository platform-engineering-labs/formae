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

	"github.com/platform-engineering-labs/formae/internal/cli/app"
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
	u := &schema.SchemaVersionUpgrade{ProjectDir: "/tmp/proj", Current: "0.85.0", Target: "0.88.0"}

	// Save and restore the package seams.
	origInteractive, origConfirm, origUpgrade := isInteractive, runConfirm, upgradeFn
	t.Cleanup(func() { isInteractive, runConfirm, upgradeFn = origInteractive, origConfirm, origUpgrade })

	setup := func(interactive bool, confirmAnswer bool) (confirmCalls, upgradeCalls *int) {
		cc, uc := 0, 0
		isInteractive = func() bool { return interactive }
		runConfirm = func(_ *theme.Theme, _ string, _ string) (bool, error) { cc++; return confirmAnswer, nil }
		upgradeFn = func(_ *app.App, outputSchema, projectDir, version string) ([]string, error) {
			uc++
			assert.Equal(t, "pkl", outputSchema)
			assert.Equal(t, u.ProjectDir, projectDir)
			assert.Equal(t, u.Target, version)
			return nil, nil
		}
		return &cc, &uc
	}

	t.Run("--yes applies without prompting", func(t *testing.T) {
		cc, uc := setup(true, false)
		opts := &ExtractOptions{OutputSchema: "pkl", Yes: true}
		require.NoError(t, handleSchemaVersionUpgrade(nil, th, warnStyle, opts, "out.pkl", u))
		assert.Equal(t, 0, *cc, "should not prompt with --yes")
		assert.Equal(t, 1, *uc, "should apply with --yes")
	})

	t.Run("interactive + confirmed applies", func(t *testing.T) {
		cc, uc := setup(true, true)
		opts := &ExtractOptions{OutputSchema: "pkl"}
		require.NoError(t, handleSchemaVersionUpgrade(nil, th, warnStyle, opts, "out.pkl", u))
		assert.Equal(t, 1, *cc)
		assert.Equal(t, 1, *uc)
	})

	t.Run("interactive + declined nags, no write", func(t *testing.T) {
		cc, uc := setup(true, false)
		opts := &ExtractOptions{OutputSchema: "pkl"}
		require.NoError(t, handleSchemaVersionUpgrade(nil, th, warnStyle, opts, "out.pkl", u))
		assert.Equal(t, 1, *cc)
		assert.Equal(t, 0, *uc, "declined must not write")
	})

	t.Run("non-interactive without --yes nags, no prompt, no write", func(t *testing.T) {
		cc, uc := setup(false, false)
		opts := &ExtractOptions{OutputSchema: "pkl"}
		require.NoError(t, handleSchemaVersionUpgrade(nil, th, warnStyle, opts, "out.pkl", u))
		assert.Equal(t, 0, *cc, "must not prompt without a TTY")
		assert.Equal(t, 0, *uc, "must not write without consent")
	})
}
