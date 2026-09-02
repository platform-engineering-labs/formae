// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// genNow is the fixed clock every generator-tab test reads its relative
// instants against.
var genNow = time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)

// ---------------------------------------------------------------------------
// generatorRow: cell builders
// ---------------------------------------------------------------------------

// TestGeneratorRow_AllSixColumns covers a rotating generator with destinations:
// every column carries a value, and LastRotated carries the derived instant
// rather than a zero.
func TestGeneratorRow_AllSixColumns(t *testing.T) {
	g := apimodel.GeneratorInventoryItem{
		Label:         "db-password",
		Type:          "password",
		Stack:         "db",
		Config:        json.RawMessage(`{"Type":"password","Length":32}`),
		EverySeconds:  86400,
		LastRotatedAt: genNow.Add(-90 * 24 * time.Hour),
		GenerationID:  "2ABcDeFgHiJkLmNoPqRsTuVwXyZ",
		Destinations: []apimodel.GeneratorDestination{
			{ResourceLabel: "worker", StackLabel: "app"},
			{ResourceLabel: "primary", StackLabel: "db"},
		},
	}
	got := generatorRow(g, genNow)

	require.Len(t, got.cells, 6)
	assert.Equal(t, "db-password", got.cells[0], "Label")
	assert.Equal(t, "db", got.cells[1], "Stack")
	assert.Equal(t, "password", got.cells[2], "Type")
	assert.Equal(t, "1d", got.cells[3], "Every")
	assert.Equal(t, "Dec 2 12:00Z", got.cells[4], "LastRotated")
	assert.Equal(t, "2", got.cells[5], "Destinations")

	assert.NotEqual(t, "", got.cells[4])
	assert.NotEqual(t, "never", got.cells[4])
}

// TestGeneratorRow_NeverRotated covers a generator that declares a cadence but
// whose last rotation has never committed. It reads as "never", which is
// distinguishable from a generator that rotated a long time ago.
func TestGeneratorRow_NeverRotated(t *testing.T) {
	never := apimodel.GeneratorInventoryItem{
		Label: "fresh", Type: "password", Stack: "db",
		Config: json.RawMessage(`{}`), EverySeconds: 3600,
	}
	longAgo := apimodel.GeneratorInventoryItem{
		Label: "stale", Type: "password", Stack: "db",
		Config: json.RawMessage(`{}`), EverySeconds: 3600,
		LastRotatedAt: genNow.Add(-365 * 24 * time.Hour),
	}

	neverRow := generatorRow(never, genNow)
	longAgoRow := generatorRow(longAgo, genNow)

	assert.Equal(t, "never", neverRow.cells[4])
	assert.NotEqual(t, neverRow.cells[4], longAgoRow.cells[4],
		"a generator that never rotated must not read the same as one that rotated long ago")
	assert.Equal(t, "Mar 2 12:00Z", longAgoRow.cells[4])
}

// TestGeneratorRow_NoCadence covers a generator that declares no rotation:
// nothing advances it on its own, so it reports no interval and no rotation
// history rather than claiming it has never rotated.
func TestGeneratorRow_NoCadence(t *testing.T) {
	g := apimodel.GeneratorInventoryItem{
		Label: "static", Type: "password", Stack: "db",
		Config: json.RawMessage(`{}`),
	}
	got := generatorRow(g, genNow)
	assert.Equal(t, "-", got.cells[3], "Every")
	assert.Equal(t, "-", got.cells[4], "LastRotated")
	assert.Equal(t, "0", got.cells[5], "Destinations")
}

// TestGeneratorRow_DestinationsAcrossStacks covers that the count spans stacks
// and the detail names each destination with the stack that holds it.
func TestGeneratorRow_DestinationsAcrossStacks(t *testing.T) {
	g := apimodel.GeneratorInventoryItem{
		Label: "db-password", Type: "password", Stack: "db",
		Config: json.RawMessage(`{}`),
		Destinations: []apimodel.GeneratorDestination{
			{ResourceLabel: "worker", StackLabel: "app"},
			{ResourceLabel: "primary", StackLabel: "db"},
			{ResourceLabel: "replica", StackLabel: "db"},
		},
	}
	got := generatorRow(g, genNow)
	assert.Equal(t, "3", got.cells[5])

	require.NotNil(t, got.detail)
	lines := got.detail(80)
	assert.Contains(t, lines, "Destinations (3):")
	assert.Contains(t, lines, "  - worker (stack app)")
	assert.Contains(t, lines, "  - primary (stack db)")
	assert.Contains(t, lines, "  - replica (stack db)")
}

// TestGeneratorRow_Detail_NoDestinations covers a generator nothing binds.
func TestGeneratorRow_Detail_NoDestinations(t *testing.T) {
	g := apimodel.GeneratorInventoryItem{
		Label: "unbound", Type: "password", Stack: "db",
		Config: json.RawMessage(`{}`),
	}
	lines := generatorRow(g, genNow).detail(80)
	assert.Contains(t, lines, "Destinations (0):")
	assert.Contains(t, lines, "  none")
}

// ---------------------------------------------------------------------------
// generatorRow: the generation identity is shown
// ---------------------------------------------------------------------------

// TestGeneratorRow_Detail_ShowsTheGenerationIdentity covers that the identity
// of the generation the generator currently holds is readable. It names which
// generation is live without saying anything about its value.
func TestGeneratorRow_Detail_ShowsTheGenerationIdentity(t *testing.T) {
	g := apimodel.GeneratorInventoryItem{
		Label: "db-password", Type: "password", Stack: "db",
		Config:       json.RawMessage(`{}`),
		GenerationID: "2ABcDeFgHiJkLmNoPqRsTuVwXyZ",
	}
	lines := generatorRow(g, genNow).detail(80)
	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, "2ABcDeFgHiJkLmNoPqRsTuVwXyZ")
	assert.Contains(t, joined, "Generation:")
}

// TestGeneratorRow_Detail_NoGenerationYet covers a generator that has not yet
// had a value drawn.
func TestGeneratorRow_Detail_NoGenerationYet(t *testing.T) {
	g := apimodel.GeneratorInventoryItem{
		Label: "fresh", Type: "password", Stack: "db",
		Config: json.RawMessage(`{}`),
	}
	lines := generatorRow(g, genNow).detail(80)
	assert.Contains(t, strings.Join(lines, "\n"), "Generation:   none drawn yet")
}

// ---------------------------------------------------------------------------
// Redaction
// ---------------------------------------------------------------------------

// TestGeneratorRow_WithholdsADrawnValue covers that no drawn value reaches the
// rendered surface. The assertion decodes the byte arrays a json.RawMessage
// renders as, so it reads the text a config carries rather than passing over it.
func TestGeneratorRow_WithholdsADrawnValue(t *testing.T) {
	const drawn = "hunter2-the-drawn-value"
	g := apimodel.GeneratorInventoryItem{
		Label: "db-password", Type: "password", Stack: "db",
		// A spec that carries an opaque envelope: the value must render as the
		// mask in every column and in the detail panel.
		Config: json.RawMessage(`{"Type":"password","Seed":` +
			`{"$visibility":"Opaque","$value":"` + drawn + `"}}`),
		GenerationID:  "2ABcDeFgHiJkLmNoPqRsTuVwXyZ",
		EverySeconds:  86400,
		LastRotatedAt: genNow.Add(-time.Hour),
		Destinations:  []apimodel.GeneratorDestination{{ResourceLabel: "primary", StackLabel: "db"}},
	}
	got := generatorRow(g, genNow)

	surface := strings.Join(got.cells, "\n") + "\n" +
		strings.Join(got.detail(80), "\n") + "\n" +
		got.title + "\n" +
		fmt.Sprintf("%v", got.cells)

	assert.NotContains(t, tuitest.DecodeByteArrays(surface), drawn,
		"a drawn value must never reach a rendered surface")
	assert.Contains(t, surface, opaqueMask)

	// The identity is not a value, and is shown.
	assert.Contains(t, tuitest.DecodeByteArrays(surface), "2ABcDeFgHiJkLmNoPqRsTuVwXyZ")
}

// ---------------------------------------------------------------------------
// fetch integration via newSpecs
// ---------------------------------------------------------------------------

// TestGeneratorsSpec_FetchDelegates covers the tab's fetch: it reaches the
// client's generator inventory and builds one row per generator with all six
// columns.
func TestGeneratorsSpec_FetchDelegates(t *testing.T) {
	g1 := apimodel.GeneratorInventoryItem{
		Label: "db-password", Type: "password", Stack: "db",
		Config:        json.RawMessage(`{"Length":32}`),
		EverySeconds:  86400,
		LastRotatedAt: genNow.Add(-90 * 24 * time.Hour),
		GenerationID:  "gen-1",
		Destinations: []apimodel.GeneratorDestination{
			{ResourceLabel: "primary", StackLabel: "db"},
			{ResourceLabel: "worker", StackLabel: "app"},
		},
	}
	g2 := apimodel.GeneratorInventoryItem{
		Label: "api-key", Type: "password", Stack: "app",
		Config: json.RawMessage(`{"Length":64}`),
	}
	c := &fakeClient{generators: []apimodel.GeneratorInventoryItem{g1, g2}}
	specs := newSpecs(func() time.Time { return genNow })

	rows, nags, err := specs[TabGenerators].fetch(c, "", true)
	require.NoError(t, err)
	assert.Empty(t, nags)
	require.Len(t, rows, 2)

	assert.Equal(t,
		[]string{"db-password", "db", "password", "1d", "Dec 2 12:00Z", "2"},
		rows[0].cells)
	assert.Equal(t,
		[]string{"api-key", "app", "password", "-", "-", "0"},
		rows[1].cells)
	assert.NotNil(t, rows[0].detail)
	assert.True(t, c.generatorsFromTUI)
}

// TestGeneratorsSpec_IgnoresTheQueryString covers that the generators tab, like
// Stacks and Policies, does not carry the query to the server: its fetch takes
// no query and the query bar drives a client-side filter instead.
func TestGeneratorsSpec_IgnoresTheQueryString(t *testing.T) {
	c := &fakeClient{generators: []apimodel.GeneratorInventoryItem{
		{Label: "db-password", Type: "password", Stack: "db", Config: json.RawMessage(`{}`)},
	}}
	specs := newSpecs(func() time.Time { return genNow })
	assert.False(t, specs[TabGenerators].serverQuery)

	rows, _, err := specs[TabGenerators].fetch(c, "no-such-generator", true)
	require.NoError(t, err)
	assert.Len(t, rows, 1, "the query never reaches the generator fetch")
}

// ---------------------------------------------------------------------------
// Non-TTY table export
// ---------------------------------------------------------------------------

// TestRenderGenerators_TableCarriesEveryColumn covers the print-and-exit path.
func TestRenderGenerators_TableCarriesEveryColumn(t *testing.T) {
	g := apimodel.GeneratorInventoryItem{
		Label: "db-password", Type: "password", Stack: "db",
		Config:        json.RawMessage(`{}`),
		EverySeconds:  86400,
		LastRotatedAt: genNow.Add(-90 * 24 * time.Hour),
		Destinations:  []apimodel.GeneratorDestination{{ResourceLabel: "primary", StackLabel: "db"}},
	}
	out := RenderGenerators(theme.New("quiet"), []apimodel.GeneratorInventoryItem{g}, genNow, 0, 160)

	for _, want := range []string{"Label", "Stack", "Type", "Every", "LastRotated", "Destinations",
		"db-password", "1d", "Dec 2 12:00Z"} {
		assert.Contains(t, out, want)
	}
	assert.Contains(t, out, "Showing 1 of 1 total generators")
}
