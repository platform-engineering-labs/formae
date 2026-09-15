//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package apply

import (
	"encoding/json"
	"fmt"
	formae "github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/querier"
	_ "github.com/platform-engineering-labs/formae/internal/schema/pkl"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResolutionMachineStructuredReviewAndErrors(t *testing.T) {
	old := applyFn
	oldChoice := chooseDrift
	t.Cleanup(func() { applyFn = old; chooseDrift = oldChoice })
	chooseDrift = func(_ *theme.Theme, _ apimodel.FormaReconcileRejectedError) ([]pkgmodel.DriftDecision, error) {
		t.Fatal("machine must not prompt")
		return nil, nil
	}
	for _, kind := range []string{"rejection", "review", "real", "failed-create"} {
		t.Run(kind, func(t *testing.T) {
			opts := &ApplyOptions{OutputConsumer: printer.ConsumerMachine, OutputSchema: "json", Simulate: kind != "real"}
			applyFn = func(_ *app.App, o *ApplyOptions, sim bool) (*apimodel.SubmitCommandResponse, []string, error) {
				require.Empty(t, o.Message)
				require.False(t, o.Force)
				switch kind {
				case "rejection":
					return nil, nil, &apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]{ErrorType: apimodel.ReconcileRejected, Data: resolutionRejection("obs")}
				case "failed-create":
					return nil, nil, &apimodel.ErrorResponse[apimodel.DriftResolutionError]{ErrorType: apimodel.DriftResolutionRejected, Data: apimodel.DriftResolutionError{Code: "desired-intent-unavailable", CommandID: "failed-create", ResourceID: "missing"}}
				default:
					return &apimodel.SubmitCommandResponse{CommandID: "recorded", Review: &pkgmodel.DriftReview{ReviewID: "review"}}, nil, nil
				}
			}
			out := captureStdout(t, func() {
				err := runApplyForMachines(newTestApp(), opts)
				if kind == "rejection" || kind == "failed-create" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			})
			var wire map[string]any
			require.NoError(t, json.Unmarshal([]byte(out), &wire))
			if kind == "review" {
				require.Equal(t, "review", wire["Review"].(map[string]any)["ReviewID"])
			}
			if kind == "real" {
				require.Equal(t, map[string]any{"CommandId": "recorded"}, wire)
			}
			if kind == "rejection" {
				require.Equal(t, "obs", wire["data"].(map[string]any)["ObservationID"])
			}
			if kind == "failed-create" {
				require.Equal(t, "failed-create", wire["data"].(map[string]any)["CommandId"])
			}
		})
	}
}

func TestResolutionReusesEvaluatedInputOnTransport(t *testing.T) {
	require.NoError(t, config.Config.EnsureDataDirectory())
	require.NoError(t, config.Config.EnsureClientID())
	dir := t.TempDir()
	core, err := filepath.Abs("../../schema/pkl/schema/forma.pkl")
	require.NoError(t, err)
	file := filepath.Join(dir, "forma.pkl")
	require.NoError(t, os.WriteFile(file, []byte("extends \""+core+"\"\n"), 0600))
	var bodies [][]byte
	var controls []string
	var messages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/stats" {
			require.NoError(t, json.NewEncoder(w).Encode(apimodel.Stats{Version: formae.Version, Capabilities: []string{"shared-drift-resolution", "command-metadata"}}))
			return
		}
		require.Equal(t, "/api/v1/commands", r.URL.Path)
		require.NoError(t, r.ParseMultipartForm(4<<20))
		f, _, err := r.FormFile("file")
		require.NoError(t, err)
		body, err := io.ReadAll(f)
		require.NoError(t, err)
		f.Close()
		bodies = append(bodies, body)
		controls = append(controls, r.FormValue("resolution"))
		messages = append(messages, r.FormValue("message"))
		_, _ = w.Write([]byte(`{"CommandId":"recorded"}`))
	}))
	defer server.Close()
	a := &app.App{Config: &pkgmodel.Config{Cli: pkgmodel.CliConfig{Connection: &pkgmodel.ClassicConnection{URL: server.URL, Port: 80}, DisableUsageReporting: true}}}
	opts := &ApplyOptions{FormaFile: file, Mode: pkgmodel.FormaApplyModeReconcile}
	_, _, err = applyFn(a, opts, true)
	require.NoError(t, err)
	require.NoError(t, os.Remove(file))
	opts.Resolution = &pkgmodel.DriftResolution{ObservationID: "obs", Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}}}
	_, _, err = applyFn(a, opts, true)
	require.NoError(t, err)
	opts.Resolution.ReviewID = "review"
	opts.Resolution.IdempotencyKey = "retry"
	opts.Message = ""
	_, _, err = applyFn(a, opts, false)
	require.NoError(t, err)
	require.Len(t, bodies, 3)
	require.Equal(t, bodies[0], bodies[1])
	require.Equal(t, bodies[1], bodies[2])
	require.Empty(t, controls[0])
	require.Contains(t, controls[2], `"ReviewID":"review"`)
	require.Contains(t, controls[2], `"IdempotencyKey":"retry"`)
	require.Equal(t, []string{"", "", ""}, messages)
}

func TestRecordedGuidanceLiteralQueries(t *testing.T) {
	ds, err := dssqlite.NewDatastoreSQLite(t.Context(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: filepath.Join(t.TempDir(), "query.db")}}, "test")
	require.NoError(t, err)
	defer ds.Close()
	_, err = ds.CreateStack(&pkgmodel.Stack{Label: `production + "blue"`}, "seed")
	require.NoError(t, err)
	for i, label := range []string{`name with spaces + "quotes" \\ and : colons`, `name with stars*`, `name with starsOTHER`} {
		r := &pkgmodel.Resource{Ksuid: fmt.Sprintf("r%d", i), NativeID: fmt.Sprintf("native%d", i), Stack: `production + "blue"`, Type: "Test::Resource", Label: label, Properties: []byte(`{}`)}
		_, err := ds.StoreResource(r, "seed")
		require.NoError(t, err)
	}
	for i, label := range []string{`name with spaces + "quotes" \\ and : colons`, `name with stars*`} {
		query := resourceQuery(`production + "blue"`, "Test::Resource", label, fmt.Sprintf("r%d", i))
		if i == 1 {
			require.Contains(t, query, "exact query unavailable")
			require.Contains(t, query, label)
			continue
		}
		resources, err := querier.NewBlugeQuerier(ds).QueryResources(query)
		require.NoError(t, err)
		require.Len(t, resources, 1)
		require.Equal(t, label, resources[0].Label)
	}
}

// The human --yes route must bind the review returned by its own simulation.
// Exercise real evaluation and multipart transport, starting without a ReviewID.
func TestLegacyResolutionBindsSimulatedReviewOnTransport(t *testing.T) {
	oldInteractive := isInteractive
	t.Cleanup(func() { isInteractive = oldInteractive })
	isInteractive = func() bool { return false }
	require.NoError(t, config.Config.EnsureDataDirectory())
	require.NoError(t, config.Config.EnsureClientID())
	for _, tc := range []struct {
		name, key, message                   string
		simulate, missingReview, emptyReview bool
	}{
		{name: "decisions-only"},
		{name: "preserves-key-and-message", key: "caller-retry-key", message: "Resolve production drift"},
		{name: "simulate-only", simulate: true},
		{name: "missing-review", missingReview: true},
		{name: "empty-review", emptyReview: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, err := filepath.Abs("../../schema/pkl/schema/forma.pkl")
			require.NoError(t, err)
			file := filepath.Join(t.TempDir(), "forma.pkl")
			require.NoError(t, os.WriteFile(file, []byte("extends \""+core+"\"\n"), 0600))
			var bodies [][]byte
			var controls []pkgmodel.DriftResolution
			var messages, modes []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/stats" {
					require.NoError(t, json.NewEncoder(w).Encode(apimodel.Stats{Version: formae.Version, Capabilities: []string{"shared-drift-resolution", "command-metadata"}}))
					return
				}
				require.Equal(t, "/api/v1/commands", r.URL.Path)
				require.NoError(t, r.ParseMultipartForm(4<<20))
				f, _, err := r.FormFile("file")
				require.NoError(t, err)
				body, err := io.ReadAll(f)
				require.NoError(t, err)
				f.Close()
				bodies = append(bodies, body)
				var control pkgmodel.DriftResolution
				require.NoError(t, json.Unmarshal([]byte(r.FormValue("resolution")), &control))
				controls = append(controls, control)
				messages = append(messages, r.FormValue("message"))
				modes = append(modes, r.FormValue("simulate"))
				response := &apimodel.SubmitCommandResponse{CommandID: "recorded", Simulation: apimodel.Simulation{ChangesRequired: true}}
				if r.FormValue("simulate") == "true" {
					require.NoError(t, os.Remove(file)) // a second evaluation would fail
					if !tc.missingReview {
						response.Review = &pkgmodel.DriftReview{ReviewID: "returned-review"}
						if tc.emptyReview {
							response.Review.ReviewID = ""
						}
					}
				}
				require.NoError(t, json.NewEncoder(w).Encode(response))
			}))
			defer server.Close()
			a := &app.App{Config: &pkgmodel.Config{Cli: pkgmodel.CliConfig{Connection: &pkgmodel.ClassicConnection{URL: server.URL, Port: 80}, DisableUsageReporting: true}}}
			opts := &ApplyOptions{FormaFile: file, Mode: pkgmodel.FormaApplyModeReconcile, Yes: true, Simulate: tc.simulate, Message: tc.message, MessageExplicit: true, Resolution: &pkgmodel.DriftResolution{ObservationID: "observation", Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}}, IdempotencyKey: tc.key}}
			captureStdout(t, func() {
				err := runApplyLegacy(a, opts)
				if tc.missingReview || tc.emptyReview {
					require.ErrorContains(t, err, "no final resolution review")
				} else {
					require.NoError(t, err)
				}
			})
			require.Empty(t, controls[0].ReviewID)
			require.Equal(t, tc.key, controls[0].IdempotencyKey)
			if tc.simulate || tc.missingReview || tc.emptyReview {
				require.Equal(t, []string{"true"}, modes, "must not submit without final review or during simulation")
				return
			}
			require.Equal(t, []string{"true", "false"}, modes)
			require.Equal(t, bodies[0], bodies[1])
			require.Equal(t, []string{tc.message, tc.message}, messages)
			require.Equal(t, "observation", controls[1].ObservationID)
			require.Equal(t, []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}}, controls[1].Decisions)
			require.Equal(t, "returned-review", controls[1].ReviewID)
			require.NotEmpty(t, controls[1].IdempotencyKey)
			require.Equal(t, opts.Resolution.IdempotencyKey, controls[1].IdempotencyKey)
			if tc.key != "" {
				require.Equal(t, tc.key, controls[1].IdempotencyKey)
			}
		})
	}
}
