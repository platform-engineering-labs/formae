// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package theme

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRichPrimaryAccentMatchesPbBlue documents that rich's dark primary
// accent was changed to match pb's blue (#60A5FA), replacing the cyan it
// had inherited from quiet (#81D1DB). quiet keeps its own dark value to
// prove the two themes are isolated from each other.
func TestRichPrimaryAccentMatchesPbBlue(t *testing.T) {
	assert.Equal(t, "#60A5FA", string(New("rich").Palette.PrimaryAccent.Dark))
	assert.Equal(t, "#81D1DB", string(New("quiet").Palette.PrimaryAccent.Dark))
}

// TestRichDoneIsGreen documents that rich's "done" color was changed to
// green, matching pb's Done and rich's own op_create, replacing the
// near-black/white value it had inherited from quiet. quiet and colorblind
// keep their own values (brightness-based and blue, respectively) to prove
// the themes are isolated from each other.
func TestRichDoneIsGreen(t *testing.T) {
	assert.Equal(t, "#4ADE80", New("rich").Palette.Done.Dark)
	assert.Equal(t, "#16A34A", New("rich").Palette.Done.Light)
	assert.Equal(t, "#E8E8E8", New("quiet").Palette.Done.Dark, "quiet's brightness-based done must be untouched")
}
