//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestResolutionDeltaPartialSameStackDependencies(t *testing.T) {
	for _, kind := range []string{"resource", "generator"} {
		t.Run(kind, func(t *testing.T) {
			m, _, f, _ := scopedFixture(t)
			stack, err := m.Datastore.GetStackByLabel("a")
			require.NoError(t, err)
			a := f.Resources[0]
			a.Ksuid = "a"
			a.Schema.Fields = []string{"name", "extra"}
			if kind == "resource" {
				b := a
				b.Ksuid = "same-stack-b"
				b.Label = "b"
				b.Properties = []byte(`{"name":"eligible B"}`)
				_, err = m.Datastore.StoreResource(&b, "seed-b")
				require.NoError(t, err)
				storeDesired(t, m.Datastore, b, resource_update.OperationCreate, forma_command.CommandStateSuccess)
				a.Properties = []byte(`{"name":{"$ref":"formae://same-stack-b#/name","$value":"eligible B"},"extra":"recorded acceptance"}`)
			} else {
				g := &pkgmodel.PasswordGenerator{ID: util.NewID(), Label: "local", Stack: "a", StackID: stack.ID, Length: 24, Lowercase: true}
				_, err = m.Datastore.CreateGenerator(g, "seed-g")
				require.NoError(t, err)
				id, err := m.Datastore.GetGeneratorIdentity("local", "a")
				require.NoError(t, err)
				a.Properties = []byte(`{"name":{"$gen":true,"$generator":"` + id.ID + `","$output":"value","$visibility":"Opaque"},"extra":"recorded acceptance"}`)
			}
			command := &forma_command.FormaCommand{ID: util.NewID(), Setup: &forma_command.SetupBoundary{Version: 1}, Resolution: &pkgmodel.DriftReview{ObservationID: "observed", ReviewID: "reviewed", Decisions: []pkgmodel.DriftDecision{{ResourceID: "a", Action: "absorb"}}}, StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceUser, State: forma_command.CommandStateSuccess, Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: "a"}}, ResourceUpdates: []resource_update.ResourceUpdate{{DesiredState: a, StackLabel: "a", Operation: resource_update.OperationAccept, Source: resource_update.FormaCommandSourceUser, State: resource_update.ResourceUpdateStateSuccess}}}
			require.NoError(t, m.Datastore.StoreFormaCommand(command, command.ID))
			// Later unaccepted movement in both the contribution and its dependency
			// must not become authored content of the command's delta.
			changed := a
			changed.Properties = []byte(`{"name":"unaccepted A","extra":"unaccepted"}`)
			_, err = m.Datastore.StoreResource(&changed, "later")
			require.NoError(t, err)
			if kind == "resource" {
				b, err := m.Datastore.LoadResourceById("same-stack-b")
				require.NoError(t, err)
				b.Properties = []byte(`{"name":"unaccepted B"}`)
				_, err = m.Datastore.StoreResource(b, "later-b")
				require.NoError(t, err)
			}
			delta, err := m.ExtractCommandDesiredDelta(command.ID)
			require.NoError(t, err)
			require.True(t, delta.Partial)
			require.Empty(t, delta.Forma.Extraction.CompleteStacks)
			require.Len(t, delta.Forma.Resources, 1)
			require.Empty(t, delta.Forma.Generators)
			var props map[string]any
			require.NoError(t, json.Unmarshal(delta.Forma.Resources[0].Properties, &props))
			require.Equal(t, "recorded acceptance", props["extra"])
			envelope := props["name"].(map[string]any)
			require.Equal(t, "a", envelope["$stack"])
			require.NotContains(t, string(delta.Forma.Resources[0].Properties), "unaccepted")
			if kind == "resource" {
				require.Equal(t, true, envelope["$res"])
				require.Equal(t, "b", envelope["$label"])
			} else {
				require.Equal(t, true, envelope["$gen"])
				require.Equal(t, "local", envelope["$label"])
				require.Len(t, delta.Forma.Extraction.ReferenceGenerators, 1)
			}
			// Complete rendering must still reject this deliberately incomplete
			// selected stack; only the command-delta adapter relaxes that premise.
			incomplete := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "a"}}, Resources: []pkgmodel.Resource{a}, Extraction: &pkgmodel.ExtractionContext{}}
			scope := newPlanningDatastore(m.Datastore, incomplete)
			for attempt := 0; attempt < 16; attempt++ {
				_, err = scope.certify(func() error { return translateDesiredReferences(scope, ownPlanningValue(incomplete)) })
				if errors.Is(err, errPlanningScopeExpanded) {
					continue
				}
				break
			}
			require.Error(t, err)
			if kind == "resource" {
				require.ErrorContains(t, err, "no eligible desired declaration")
			} else {
				require.ErrorContains(t, err, "omitted from selected stack")
			}
			complete, err := m.ExtractDesiredStacks("stack:a")
			require.NoError(t, err)
			require.Len(t, complete.Extraction.CompleteStacks, 1)
			if kind == "resource" {
				require.Len(t, complete.Resources, 2)
			} else {
				require.Len(t, complete.Generators, 1)
				require.Empty(t, complete.Extraction.ReferenceGenerators)
			}
		})
	}
}
