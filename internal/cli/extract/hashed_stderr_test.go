// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package extract

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/schema"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// captureStreams redirects os.Stdout and os.Stderr to pipes, calls fn, then
// returns the captured stdout and stderr as strings.
func captureStreams(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	// Capture stdout.
	rOut, wOut, err := os.Pipe()
	require.NoError(t, err)
	origStdout := os.Stdout
	os.Stdout = wOut

	// Capture stderr.
	rErr, wErr, err := os.Pipe()
	require.NoError(t, err)
	origStderr := os.Stderr
	os.Stderr = wErr

	fn()

	// Restore originals before reading so reads don't block.
	os.Stdout = origStdout
	wOut.Close()
	os.Stderr = origStderr
	wErr.Close()

	var bufOut, bufErr strings.Builder
	bufOutBytes := make([]byte, 4096)
	for {
		n, readErr := rOut.Read(bufOutBytes)
		if n > 0 {
			bufOut.Write(bufOutBytes[:n])
		}
		if readErr != nil {
			break
		}
	}
	rOut.Close()

	bufErrBytes := make([]byte, 4096)
	for {
		n, readErr := rErr.Read(bufErrBytes)
		if n > 0 {
			bufErr.Write(bufErrBytes[:n])
		}
		if readErr != nil {
			break
		}
	}
	rErr.Close()

	return bufOut.String(), bufErr.String()
}

// TestRunExtractCore_HashedSecretCount_PrintsStderr verifies that when
// generateFn returns a HashedSecretCount > 0, runExtractCore writes exactly
// one summary line to stderr (not stdout) with the correct count.
func TestRunExtractCore_HashedSecretCount_PrintsStderr(t *testing.T) {
	const hashedCount = 3
	dir := t.TempDir()
	target := filepath.Join(dir, "out.pkl")

	origIsInteractive := isInteractive
	isInteractive = func() bool { return false }
	defer func() { isInteractive = origIsInteractive }()

	origExtractFn := extractFn
	extractFn = func(_ *app.App, _ string) (*pkgmodel.Forma, []string, error) {
		return makeForma(), nil, nil
	}
	defer func() { extractFn = origExtractFn }()

	origGenerateFn := generateFn
	generateFn = func(_ *app.App, _ *pkgmodel.Forma, targetPath, _ string, _ schema.SchemaLocation) (schema.GenerateSourcesResult, error) {
		return schema.GenerateSourcesResult{
			TargetPath:        targetPath,
			ResourceCount:     1,
			HashedSecretCount: hashedCount,
		}, nil
	}
	defer func() { generateFn = origGenerateFn }()

	opts := &ExtractOptions{
		TargetPath:   target,
		Query:        "type:AWS::S3::Bucket",
		Yes:          true,
		OutputSchema: "pkl",
	}

	capturedStdout, capturedStderr := captureStreams(t, func() {
		err := runExtractCore(nil, opts)
		require.NoError(t, err)
	})

	wantLine := fmt.Sprintf("warning: %d secret value(s) are stored hashed and cannot be re-applied without re-supplying the plaintext", hashedCount)

	assert.Contains(t, capturedStderr, wantLine,
		"hashed-secret warning must appear on stderr")
	assert.NotContains(t, capturedStdout, wantLine,
		"hashed-secret warning must NOT appear on stdout")
}

// TestRunExtractCore_HashedSecretCount_Zero_NoStderr verifies that when
// HashedSecretCount == 0 no warning line is written to either stream.
func TestRunExtractCore_HashedSecretCount_Zero_NoStderr(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.pkl")

	origIsInteractive := isInteractive
	isInteractive = func() bool { return false }
	defer func() { isInteractive = origIsInteractive }()

	origExtractFn := extractFn
	extractFn = func(_ *app.App, _ string) (*pkgmodel.Forma, []string, error) {
		return makeForma(), nil, nil
	}
	defer func() { extractFn = origExtractFn }()

	origGenerateFn := generateFn
	generateFn = func(_ *app.App, _ *pkgmodel.Forma, targetPath, _ string, _ schema.SchemaLocation) (schema.GenerateSourcesResult, error) {
		return schema.GenerateSourcesResult{
			TargetPath:        targetPath,
			ResourceCount:     1,
			HashedSecretCount: 0,
		}, nil
	}
	defer func() { generateFn = origGenerateFn }()

	opts := &ExtractOptions{
		TargetPath:   target,
		Query:        "type:AWS::S3::Bucket",
		Yes:          true,
		OutputSchema: "pkl",
	}

	capturedStdout, capturedStderr := captureStreams(t, func() {
		err := runExtractCore(nil, opts)
		require.NoError(t, err)
	})

	assert.NotContains(t, capturedStdout, "hashed",
		"no hashed warning must appear on stdout when count is 0")
	assert.NotContains(t, capturedStderr, "hashed",
		"no hashed warning must appear on stderr when count is 0")
}
