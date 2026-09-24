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
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// Persisted provider outputs may change between builds while canonical source
// continues to omit them. Absorb must retain the live outputs without hiding
// conflicting authored edits or turning acceptance into a provider write.
// The initial create witnesses old outputs; a later build writes only inputs,
// so changed output echoes still require a drift decision even when every
// canonical authored input already matches the live build.
func TestResolutionAbsorbChangedPersistedProviderDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, edit, conflict string
		removeHint           bool
	}{
		{name: "canonical-omission"},
		{name: "ordinary-field-omission", removeHint: true, conflict: "/ImageDigest"},
		{name: "explicit-output-conflict", edit: "ImageDigest", conflict: "/ImageDigest"},
		{name: "dockerfile-conflict", edit: "Dockerfile", conflict: "/Dockerfile"},
		{name: "version-uri-conflict", edit: "VersionUri", conflict: "/VersionUri"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := newSQLiteTestDatastore(t)
			m := &Metastructure{Datastore: ds, Cfg: &pkgmodel.Config{}}
			_, err := ds.CreateStack(&pkgmodel.Stack{Label: "service"}, "seed")
			require.NoError(t, err)
			stack, err := ds.GetStackByLabel("service")
			require.NoError(t, err)
			_, err = ds.CreateTarget(&pkgmodel.Target{Label: "test", Namespace: "Test", Config: json.RawMessage(`{}`)})
			require.NoError(t, err)
			imageSchema := pkgmodel.Schema{Portable: true, Fields: []string{"BuildArgs", "Dockerfile", "VersionUri", "ImageDigest", "ImageRef", "BuildConfigHash", "ImageUri"}, Hints: map[string]pkgmodel.FieldHint{}}
			for _, field := range []string{"ImageDigest", "ImageRef", "BuildConfigHash", "ImageUri"} {
				imageSchema.Hints[field] = pkgmodel.FieldHint{HasProviderDefault: true}
			}
			if tc.removeHint {
				delete(imageSchema.Hints, "ImageDigest")
			}
			initial := []pkgmodel.Resource{
				{Ksuid: "image", NativeID: "image", Managed: true, Label: "image", Type: "Test::ImageBuild", Stack: stack.Label, Target: "test", Schema: imageSchema, Properties: json.RawMessage(`{"BuildArgs":{"BASE":"stable"},"Dockerfile":"FROM old","VersionUri":"source:old","ImageDigest":"old-digest","ImageRef":"repo:old","BuildConfigHash":"old-hash","ImageUri":"registry/old"}`)},
				{Ksuid: "task", NativeID: "task", Managed: true, Label: "task", Type: "Test::TaskDefinition", Stack: stack.Label, Target: "test", Schema: pkgmodel.Schema{Portable: true, Fields: []string{"Version"}}, Properties: json.RawMessage(`{"Version":"old"}`)},
				{Ksuid: "service", NativeID: "service", Managed: true, Label: "service", Type: "Test::Service", Stack: stack.Label, Target: "test", Schema: pkgmodel.Schema{Portable: true, Fields: []string{"Version"}}, Properties: json.RawMessage(`{"Version":"old"}`)},
			}
			seed := &forma_command.FormaCommand{ID: "seed", StartTs: time.Now().Add(-time.Hour), ModifiedTs: time.Now().Add(-time.Hour), Command: pkgmodel.CommandApply, Source: forma_command.SourceUser, State: forma_command.CommandStateSuccess, Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}}}
			for _, r := range initial {
				seed.ResourceUpdates = append(seed.ResourceUpdates, resource_update.ResourceUpdate{DesiredState: r, StackLabel: stack.Label, Operation: resource_update.OperationCreate, Source: resource_update.FormaCommandSourceUser, State: resource_update.ResourceUpdateStateSuccess})
			}
			require.NoError(t, ds.StoreFormaCommand(seed, seed.ID))
			for i := range initial {
				_, err = ds.StoreResource(&initial[i], seed.ID)
				require.NoError(t, err)
			}
			live := initial[0]
			live.Properties = json.RawMessage(`{"BuildArgs":{"BASE":"stable","BUILD_HASH":"new"},"Dockerfile":"FROM new","VersionUri":"source:new","ImageDigest":"new-digest","ImageRef":"repo:new","BuildConfigHash":"new-hash","ImageUri":"registry/new"}`)
			live.PatchDocument = json.RawMessage(`[{"op":"replace","path":"/Dockerfile","value":"FROM new"},{"op":"replace","path":"/VersionUri","value":"source:new"},{"op":"add","path":"/BuildArgs/BUILD_HASH","value":"new"}]`)
			patch := &forma_command.FormaCommand{ID: "build", StartTs: time.Now(), ModifiedTs: time.Now(), Command: pkgmodel.CommandApply, Source: forma_command.SourceUser, State: forma_command.CommandStateSuccess, Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch}, Stacks: seed.Stacks, ResourceUpdates: []resource_update.ResourceUpdate{{DesiredState: live, StackLabel: stack.Label, Operation: resource_update.OperationUpdate, Source: resource_update.FormaCommandSourceUser, State: resource_update.ResourceUpdateStateSuccess}}}
			require.NoError(t, ds.StoreFormaCommand(patch, patch.ID))
			_, err = ds.StoreResource(&live, patch.ID)
			require.NoError(t, err)
			canonical, err := m.ExtractDesiredStacks("stack:service")
			require.NoError(t, err)
			require.JSONEq(t, string(initial[0].Properties), string(resourceByLabel(t, canonical, "image").Properties))
			properties := map[string]any{"BuildArgs": map[string]string{"BASE": "stable", "BUILD_HASH": "new"}, "Dockerfile": "FROM new", "VersionUri": "source:new"}
			if tc.edit != "" {
				properties[tc.edit] = "conflicting-authored-value"
			}
			resourceByLabel(t, canonical, "image").Properties, err = json.Marshal(properties)
			require.NoError(t, err)
			for _, label := range []string{"task", "service"} {
				resourceByLabel(t, canonical, label).Properties = json.RawMessage(`{"Version":"new"}`)
			}
			frozen := writeFrozenFormaJSON(t, canonical)
			observation := observeResolution(t, m, readFrozenFormaJSON(t, frozen))
			opts := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: observation.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "image", Action: "absorb"}}}}
			preview, err := m.ApplyForma(readFrozenFormaJSON(t, frozen), opts, "client", "subject", "")
			if tc.conflict != "" {
				var conflict apimodel.DriftResolutionError
				require.ErrorAs(t, err, &conflict)
				require.Equal(t, "decision-edit-conflict", conflict.Code)
				require.Contains(t, conflict.Reason, tc.conflict)
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, preview.Review.ReviewID)
			opts.Simulate = false
			opts.Resolution.ReviewID = preview.Review.ReviewID
			plan, err := m.prepareGuardedApply(readFrozenFormaJSON(t, frozen), opts, "client", "subject", "")
			require.NoError(t, err)
			require.Len(t, plan.Command.ResourceUpdates, 3)
			for _, update := range plan.Command.ResourceUpdates {
				if update.DesiredState.Label == "image" {
					require.Equal(t, resource_update.OperationAccept, update.Operation)
					require.Empty(t, update.DesiredState.PatchDocument)
					require.Empty(t, update.CreateOnlyPatch)
					require.JSONEq(t, string(live.Properties), string(update.DesiredState.Properties))
				} else {
					require.Equal(t, resource_update.OperationUpdate, update.Operation)
					require.JSONEq(t, `[{"op":"replace","path":"/Version","value":"new"}]`, string(update.DesiredState.PatchDocument))
				}
			}
			require.NoError(t, admitScopedPlan(t, m, plan))
		})
	}
}
