// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package nag

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// captureStdout redirects os.Stdout for the duration of fn and returns what
// was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func TestMaybePrintNags_RendersThemedTip(t *testing.T) {
	th := theme.New("formae")
	restore := shouldNag
	shouldNag = func() bool { return true }
	defer func() { shouldNag = restore }()
	out := captureStdout(t, func() { MaybePrintNags(th, []string{"you have unmanaged resources"}) })
	assert.Contains(t, out, "Tip:")
	assert.Contains(t, out, "you have unmanaged resources")
}

func TestMaybePrintNags_SuppressedWhenCoinFlipFalse(t *testing.T) {
	th := theme.New("formae")
	restore := shouldNag
	shouldNag = func() bool { return false }
	defer func() { shouldNag = restore }()
	out := captureStdout(t, func() { MaybePrintNags(th, []string{"x"}) })
	assert.Empty(t, out)
}
