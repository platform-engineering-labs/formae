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
