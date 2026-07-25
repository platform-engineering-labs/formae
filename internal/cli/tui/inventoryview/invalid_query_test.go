// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package inventoryview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// TestFailedView_InvalidQueryShowsGuide verifies that an invalid-query error
// renders a friendly "Invalid query" panel with the specific reason and a short
// query guide (valid fields + an example), instead of the raw error value.
func TestFailedView_InvalidQueryShowsGuide(t *testing.T) {
	th := theme.New("formae")
	tm := newTabModel(th, resourceSpec())
	tm.state = tabFailed
	tm.err = &apimodel.ErrorResponse[apimodel.InvalidQueryError]{
		Data: apimodel.InvalidQueryError{Reason: "unknown field for ResourceQuery: 'stac'"},
	}
	tm = tm.setSize(goldenWidth, goldenHeight)

	out := ansi.Strip(strings.Join(tm.failedView(th), "\n"))

	assert.Contains(t, out, "Invalid query", "friendly title")
	assert.Contains(t, out, "stac", "the specific reason names the offending term")
	assert.Contains(t, out, "stack", "guide lists valid fields")
	assert.Contains(t, out, "type:AWS", "guide shows an example query")
	assert.Contains(t, out, "r: retry", "retry hint still present")
}
