// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package extract

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	"github.com/platform-engineering-labs/formae/internal/schema"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestMain(m *testing.M) {
	tuitest.PinRendering()
	os.Exit(m.Run())
}

// TestRenderExtractSummary_Golden verifies the themed summary output for 2 resources.
func TestRenderExtractSummary_Golden(t *testing.T) {
	th := theme.New("formae")
	resources := []extractedResource{
		{Label: "my-bucket", Type: "AWS::S3::Bucket", Stack: "production"},
		{Label: "logs", Type: "AWS::S3::Bucket", Stack: "production"},
	}
	out := renderExtractSummary(th, "./extracted.pkl", resources)
	tuitest.RequireGolden(t, []byte(out))
}

// makeForma returns a *pkgmodel.Forma with one S3 bucket resource.
func makeForma() *pkgmodel.Forma {
	return &pkgmodel.Forma{
		Resources: []pkgmodel.Resource{
			{Label: "my-bucket", Type: "AWS::S3::Bucket", Stack: "production"},
		},
	}
}

// TestRunExtract_NonTTY_ExistingFile_NoYes verifies D8: non-TTY + existing file + no --yes
// must return an error and must NOT overwrite the file.
func TestRunExtract_NonTTY_ExistingFile_NoYes(t *testing.T) {
	// Create a temp file that "already exists".
	dir := t.TempDir()
	target := filepath.Join(dir, "out.pkl")
	require.NoError(t, os.WriteFile(target, []byte("original"), 0o644))

	// Stub isInteractive to return false (non-TTY).
	origIsInteractive := isInteractive
	isInteractive = func() bool { return false }
	defer func() { isInteractive = origIsInteractive }()

	// Stub runConfirm to verify it is never called.
	confirmCalled := false
	origRunConfirm := runConfirm
	runConfirm = func(_ *theme.Theme, _, _ string) (bool, error) {
		confirmCalled = true
		return true, nil
	}
	defer func() { runConfirm = origRunConfirm }()

	// Stub extractFn so we get resources without a real agent.
	origExtractFn := extractFn
	extractFn = func(_ *app.App, _ string) (*pkgmodel.Forma, []string, error) {
		return makeForma(), nil, nil
	}
	defer func() { extractFn = origExtractFn }()

	// Stub generateFn so we don't actually write anything.
	origGenerateFn := generateFn
	generateFn = func(_ *app.App, _ *pkgmodel.Forma, _, _ string, _ schema.SchemaLocation) (schema.GenerateSourcesResult, error) {
		panic("generateFn must not be called")
	}
	defer func() { generateFn = origGenerateFn }()

	opts := &ExtractOptions{
		TargetPath:   target,
		Query:        "type:AWS::S3::Bucket",
		Yes:          false,
		OutputSchema: "pkl",
	}

	err := runExtractCore(nil, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interactive input requires a TTY")
	assert.False(t, confirmCalled, "confirm must not be called in non-TTY mode")

	// The original file must NOT be overwritten.
	content, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, "original", string(content))
}

// TestRunExtract_NonTTY_WithYes verifies that non-TTY + --yes skips the overwrite confirm.
func TestRunExtract_NonTTY_WithYes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.pkl")
	require.NoError(t, os.WriteFile(target, []byte("original"), 0o644))

	// Stub isInteractive to return false (non-TTY).
	origIsInteractive := isInteractive
	isInteractive = func() bool { return false }
	defer func() { isInteractive = origIsInteractive }()

	// Stub runConfirm to verify it is never called.
	confirmCalled := false
	origRunConfirm := runConfirm
	runConfirm = func(_ *theme.Theme, _, _ string) (bool, error) {
		confirmCalled = true
		return true, nil
	}
	defer func() { runConfirm = origRunConfirm }()

	// Stub extractFn so we get resources without a real agent.
	origExtractFn := extractFn
	extractFn = func(_ *app.App, _ string) (*pkgmodel.Forma, []string, error) {
		return makeForma(), nil, nil
	}
	defer func() { extractFn = origExtractFn }()

	// Stub generateFn — just return a result, confirm is what we're testing.
	origGenerateFn := generateFn
	generateFn = func(_ *app.App, _ *pkgmodel.Forma, targetPath, _ string, _ schema.SchemaLocation) (schema.GenerateSourcesResult, error) {
		return schema.GenerateSourcesResult{TargetPath: targetPath, ResourceCount: 1}, nil
	}
	defer func() { generateFn = origGenerateFn }()

	opts := &ExtractOptions{
		TargetPath:   target,
		Query:        "type:AWS::S3::Bucket",
		Yes:          true,
		OutputSchema: "pkl",
	}

	err := runExtractCore(nil, opts)
	require.NoError(t, err, "non-TTY + --yes should succeed past the confirm gate")
	assert.False(t, confirmCalled, "confirm must not be called when --yes is set")
}
