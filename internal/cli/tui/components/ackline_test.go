// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package components

import (
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	"github.com/stretchr/testify/assert"
)

func TestAckLine_Golden(t *testing.T) {
	th := theme.New("formae")
	out := strings.Join([]string{
		AckLine(th, AckDone, "switched to staging"),
		AckLine(th, AckSkip, "cloudflare already up to date"),
		AckLine(th, AckWarn, "restart the agent when ready: formae agent start"),
		AckLine(th, AckFail, "failed to install grafana"),
	}, "\n")
	tuitest.RequireGolden(t, []byte(out))
}

func TestAckLine_MarkerGlyphs(t *testing.T) {
	th := theme.New("formae")
	assert.Contains(t, AckLine(th, AckDone, "x"), "✓")
	assert.Contains(t, AckLine(th, AckSkip, "x"), "·")
	assert.Contains(t, AckLine(th, AckWarn, "x"), "!")
	assert.Contains(t, AckLine(th, AckFail, "x"), "✗")
}
