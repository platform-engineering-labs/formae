//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestResolutionPreservesPlannerWarnings(t *testing.T) {
	m, f := resolutionFixture(t)
	_, err := m.Datastore.CreateTarget(&pkgmodel.Target{Label: "replace-me", Namespace: "test", Config: []byte(`{"region":"old"}`)})
	require.NoError(t, err)
	_, err = m.Datastore.StoreResource(&pkgmodel.Resource{Ksuid: "unmanaged-warning", Stack: "$unmanaged", Target: "replace-me", Label: "found", Type: "Test::Resource", Properties: []byte(`{}`)}, "seed")
	require.NoError(t, err)
	f.Targets = append(f.Targets, pkgmodel.Target{Label: "replace-me", Namespace: "test", Config: []byte(`{"region":"new"}`)})
	rejected := observeResolution(t, m, f)
	preview, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: rejected.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}}}}, "client", "subject", "")
	require.NoError(t, err)
	require.NotEmpty(t, preview.Simulation.Command.TargetUpdates)
	require.Contains(t, preview.Simulation.Warnings, `Target "replace-me" is being replaced. 1 unmanaged resource(s) on this target will lose visibility and must be re-discovered.`)
}

func TestResolutionObservationOrigin(t *testing.T) {
	for _, kind := range []string{"synchronizer", "patch", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			m, f := resolutionFixture(t)
			r, err := m.Datastore.LoadResourceById("a")
			require.NoError(t, err)
			source := forma_command.SourceSynchronizer
			mode := pkgmodel.FormaApplyModeReconcile
			if kind == "patch" {
				source = forma_command.SourceUser
				mode = pkgmodel.FormaApplyModePatch
			}
			r.Properties = []byte(`{"name":"later"}`)
			id := "origin-" + kind
			if kind != "unknown" {
				require.NoError(t, m.Datastore.StoreFormaCommand(&forma_command.FormaCommand{ID: id, Command: pkgmodel.CommandApply, Source: source, Config: config.FormaCommandConfig{Mode: mode}, State: forma_command.CommandStateSuccess, StartTs: time.Now(), ModifiedTs: time.Now()}, id))
			}
			_, err = m.Datastore.StoreResource(r, id)
			require.NoError(t, err)
			rejection := observeResolution(t, m, f)
			mod := rejection.ModifiedStacks["a"].ModifiedResources[0]
			raw, err := json.Marshal(mod)
			require.NoError(t, err)
			var wire map[string]any
			require.NoError(t, json.Unmarshal(raw, &wire))
			require.Equal(t, id, wire["ObservedCommandID"])
			if kind == "unknown" {
				require.NotContains(t, wire, "ObservedSource")
			} else {
				require.Equal(t, string(source), wire["ObservedSource"])
				require.Equal(t, string(mode), wire["ObservedMode"])
				require.Equal(t, "apply", wire["ObservedCommand"])
			}
		})
	}
}
