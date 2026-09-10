// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Schedule edits must reach persistence without scheduling a credential draw
// or writing any existing destination, including both outputs of a key pair.
func TestGeneratorRotationReconcile_PreservesCredentials(t *testing.T) {
	for _, kind := range []string{"password", "keypair"} {
		for _, mode := range []pkgmodel.FormaApplyMode{pkgmodel.FormaApplyModeReconcile, pkgmodel.FormaApplyModePatch} {
			for _, tc := range []struct {
				name    string
				before  int
				after   int
				changed bool
			}{
				{"add", 0, 3600, true},
				{"change", 900, 7776000, true},
				{"remove", 900, 0, true},
				{"unchanged", 900, 900, false},
				{"absent", 0, 0, false},
			} {
				t.Run(kind+"/"+string(mode)+"/"+tc.name, func(t *testing.T) {
					ds := rotationTestDatastore(t)
					t.Cleanup(ds.Close)
					stack := pkgmodel.Stack{Label: "secrets"}
					_, err := ds.CreateStack(&stack, "cmd-stack")
					require.NoError(t, err)
					_, err = ds.CreateTarget(&pkgmodel.Target{Label: "rotation-target", Namespace: "AWS", Config: json.RawMessage(`{}`)})
					require.NoError(t, err)

					makeGenerator := func(seconds int) pkgmodel.Generator {
						var rotation *pkgmodel.RotationSpec
						if seconds != 0 {
							rotation = &pkgmodel.RotationSpec{EverySeconds: seconds}
						}
						if kind == "keypair" {
							return &pkgmodel.KeyPairGenerator{Label: "credential", Stack: stack.Label, StackID: stack.ID, Bits: 2048, Rotation: rotation}
						}
						return &pkgmodel.PasswordGenerator{Label: "credential", Stack: stack.Label, StackID: stack.ID, Length: 32, Uppercase: true, Lowercase: true, Digits: true, Rotation: rotation}
					}
					stored := makeGenerator(tc.before)
					_, err = ds.CreateGenerator(stored, "cmd-generator")
					require.NoError(t, err)
					identity, err := ds.GetGeneratorIdentity("credential", stack.Label)
					require.NoError(t, err)
					drawnUnder, err := json.Marshal(stored)
					require.NoError(t, err)
					require.NoError(t, ds.AdvanceGeneration(identity.ID, "generation-1", "cmd-draw", drawnUnder))
					identity, err = ds.GetGeneratorIdentity("credential", stack.Label)
					require.NoError(t, err)

					var resources []pkgmodel.Resource
					for _, output := range pkgmodel.GeneratorOutputNames(stored) {
						props := fmt.Sprintf(`{"SecretString":{"$gen":true,"$generator":%q,"$output":%q,"$visibility":"Opaque","$hashed":true,"$value":%q,"$resolvedFrom":%q}}`,
							identity.ID, output, provenance.DigestOfString("existing-"+output), provenance.DigestOfString("generation-1"))
						id := storeRotationResource(t, ds, stack.Label, "secret-"+output, "AWS::SecretsManager::Secret", props)
						resource, err := ds.LoadResourceById(id)
						require.NoError(t, err)
						require.NotNil(t, resource)
						resources = append(resources, *resource)
					}
					desired, err := json.Marshal(makeGenerator(tc.after))
					require.NoError(t, err)
					plan := func() *pkgmodel.Forma {
						return &pkgmodel.Forma{Stacks: []pkgmodel.Stack{stack}, Resources: append([]pkgmodel.Resource(nil), resources...), Generators: []json.RawMessage{desired}}
					}
					command, err := FormaCommandFromForma(plan(), &config.FormaCommandConfig{Mode: mode, Simulate: true}, pkgmodel.CommandApply, ds, "test", "", "", resource_update.FormaCommandSourceUser, time.Minute)
					require.NoError(t, err)
					assert.Equal(t, tc.changed, command.HasChanges())
					assert.Empty(t, command.DrawGeneratorUpdates, "changing a schedule must not draw a credential")
					assert.Empty(t, command.ResourceUpdates, "existing destination values must remain untouched")
					if !tc.changed {
						assert.Empty(t, command.GeneratorUpdates)
						return
					}
					require.Len(t, command.GeneratorUpdates, 1, "rotation-only changes must produce a persisted update")
					update := command.GeneratorUpdates[0]
					require.Equal(t, generator_update.GeneratorOperationUpdate, update.Operation)
					assert.Equal(t, identity.ID, update.Generator.GetID())
					update.Generator.SetStackID(stack.ID)
					_, err = ds.UpdateGenerator(update.Generator, "cmd-schedule")
					require.NoError(t, err)
					after, err := ds.GetGeneratorIdentity("credential", stack.Label)
					require.NoError(t, err)
					assert.Equal(t, identity, after, "persisting a schedule must preserve generation identity and its drawing spec")
					infos, err := ds.GetGeneratorsWithRotation()
					require.NoError(t, err)
					if tc.after == 0 {
						assert.Empty(t, infos, "removing rotation must remove the generator from the scheduler")
					} else {
						require.Len(t, infos, 1)
						assert.Equal(t, tc.after, infos[0].IntervalSeconds)
					}
					command, err = FormaCommandFromForma(plan(), &config.FormaCommandConfig{Mode: mode, Simulate: true}, pkgmodel.CommandApply, ds, "test", "", "", resource_update.FormaCommandSourceUser, time.Minute)
					require.NoError(t, err)
					assert.False(t, command.HasChanges(), "reapplying the persisted schedule must be a no-op")
					assert.Empty(t, command.DrawGeneratorUpdates)
				})
			}
		}
	}
}
