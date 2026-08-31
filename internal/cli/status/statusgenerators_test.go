// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package status

import (
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generatorCommand is a command whose only work is on generators.
func generatorCommand(updates ...apimodel.GeneratorUpdate) apimodel.Command {
	return apimodel.Command{
		CommandID:        "cmd-gen001",
		Command:          "apply",
		Mode:             "reconcile",
		State:            "Success",
		StartTs:          fixedNow.Add(-30 * time.Second),
		EndTs:            fixedNow,
		GeneratorUpdates: updates,
	}
}

// TestDetailedList_GeneratorReachesATerminalState covers a generator operation
// showing up in the non-TUI detailed status output with its outcome.
func TestDetailedList_GeneratorReachesATerminalState(t *testing.T) {
	th := theme.New("formae")
	resp := &apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{
		generatorCommand(apimodel.GeneratorUpdate{
			GeneratorLabel: "db-password",
			GeneratorType:  "password",
			StackLabel:     "db",
			Operation:      apimodel.OperationCreate,
			State:          "Success",
			Duration:       1200,
		}),
	}}

	plain := stripANSI(renderStatusListAt(th, resp, true /*detailed*/, 120, fixedNow))

	assert.Contains(t, plain, "Generators")
	assert.Contains(t, plain, "LABEL")
	assert.Contains(t, plain, "STACK")
	assert.Contains(t, plain, "db-password")
	assert.Contains(t, plain, "create")
	assert.Contains(t, plain, "Success")
	assert.Contains(t, plain, "00:01", "the operation's duration")
}

// TestDetailedList_GeneratorFailureCarriesItsReason covers a failed generator
// operation reporting why.
func TestDetailedList_GeneratorFailureCarriesItsReason(t *testing.T) {
	th := theme.New("formae")
	resp := &apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{
		generatorCommand(apimodel.GeneratorUpdate{
			GeneratorLabel: "db-password",
			GeneratorType:  "password",
			StackLabel:     "db",
			Operation:      apimodel.OperationUpdate,
			State:          "Failed",
			Duration:       500,
			ErrorMessage:   "the generation could not be recorded",
		}),
	}}

	plain := stripANSI(renderStatusListAt(th, resp, true /*detailed*/, 120, fixedNow))

	assert.Contains(t, plain, "Failed")
	assert.Contains(t, plain, "the generation could not be recorded")
}

// TestDetailedList_ADrawReportsNoOutcomeOfItsOwn covers the one operation whose
// outcome no store holds: a draw writes no generator row, so the status read
// has nothing to project and must not claim one.
func TestDetailedList_ADrawReportsNoOutcomeOfItsOwn(t *testing.T) {
	th := theme.New("formae")
	resp := &apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{
		generatorCommand(apimodel.GeneratorUpdate{
			GeneratorLabel: "db-password",
			GeneratorType:  "password",
			StackLabel:     "db",
			Operation:      apimodel.OperationDraw,
			// The plan's state, which a draw never advances past.
			State: "NotStarted",
		}),
	}}

	plain := stripANSI(renderStatusListAt(th, resp, true /*detailed*/, 120, fixedNow))

	assert.Contains(t, plain, "draw")
	assert.NotContains(t, plain, "NotStarted")
	assert.Contains(t, plain, drawOutcomeNote)
}

// TestDetailedList_NothingRotated covers the positive statement: a command that
// touched generators but drew nothing says so, rather than leaving a reader to
// infer it from an absence.
func TestDetailedList_NothingRotated(t *testing.T) {
	th := theme.New("formae")
	resp := &apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{
		generatorCommand(apimodel.GeneratorUpdate{
			GeneratorLabel: "db-password",
			GeneratorType:  "password",
			StackLabel:     "db",
			Operation:      apimodel.OperationUpdate,
			State:          "Success",
			Duration:       300,
		}),
	}}

	plain := stripANSI(renderStatusListAt(th, resp, true /*detailed*/, 120, fixedNow))

	assert.Contains(t, plain, noRotationNote)
	assert.NotContains(t, plain, drawOutcomeNote)
}

// TestDetailedList_SomethingRotatedSaysSoInstead covers that the two notes are
// mutually exclusive: a command that did draw never claims nothing rotated.
func TestDetailedList_SomethingRotatedSaysSoInstead(t *testing.T) {
	th := theme.New("formae")
	resp := &apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{
		generatorCommand(
			apimodel.GeneratorUpdate{
				GeneratorLabel: "db-password", GeneratorType: "password", StackLabel: "db",
				Operation: apimodel.OperationUpdate, State: "Success", Duration: 300,
			},
			apimodel.GeneratorUpdate{
				GeneratorLabel: "db-password", GeneratorType: "password", StackLabel: "db",
				Operation: apimodel.OperationDraw, State: "NotStarted",
			},
		),
	}}

	plain := stripANSI(renderStatusListAt(th, resp, true /*detailed*/, 120, fixedNow))

	assert.NotContains(t, plain, noRotationNote)
	assert.Contains(t, plain, drawOutcomeNote)
}

// TestDetailedList_NoGeneratorsNoSection covers that a command with no
// generator work at all gains no generator section and no rotation note: the
// statement is for commands where generators are demonstrably in scope.
func TestDetailedList_NoGeneratorsNoSection(t *testing.T) {
	th := theme.New("formae")
	resp := makeStatusFixture()
	require.Empty(t, resp.Commands[0].GeneratorUpdates)

	plain := stripANSI(renderStatusListAt(th, resp, true /*detailed*/, 120, fixedNow))

	assert.NotContains(t, plain, "Generators")
	assert.NotContains(t, plain, noRotationNote)
	assert.NotContains(t, plain, drawOutcomeNote)
}

// TestDetailedList_GeneratorSurfaceWithholdsAConfigsOpaqueValue covers that no
// opaque value in a generator's projected config reaches the status output. The
// assertion decodes the byte arrays a json.RawMessage renders as, so it reads
// the text the config carries rather than passing over it.
func TestDetailedList_GeneratorSurfaceWithholdsAConfigsOpaqueValue(t *testing.T) {
	const drawn = "hunter2-the-drawn-value"
	th := theme.New("formae")
	resp := &apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{
		generatorCommand(apimodel.GeneratorUpdate{
			GeneratorLabel: "db-password",
			GeneratorType:  "password",
			StackLabel:     "db",
			Operation:      apimodel.OperationCreate,
			State:          "Success",
			GeneratorConfig: []byte(`{"Type":"password","Seed":` +
				`{"$visibility":"Opaque","$value":"` + drawn + `"}}`),
			OldGeneratorConfig: []byte(`{"Type":"password","Seed":` +
				`{"$visibility":"Opaque","$value":"` + drawn + `"}}`),
		}),
	}}

	out := renderStatusListAt(th, resp, true /*detailed*/, 120, fixedNow)
	assert.NotContains(t, tuitest.DecodeByteArrays(stripANSI(out)), drawn)
}
