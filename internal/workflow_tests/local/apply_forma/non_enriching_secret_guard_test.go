// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// hexDigestPattern matches a bare SHA-256 hex digest — the shape a hashed
// opaque value takes once convertResourceForPluginRead strips the $hashed
// envelope down to a plain value for plugin-format context (PriorProperties).
var hexDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// assertNoDigestOrHashedMarker walks props and fails the test if any value
// looks like a hashed secret: a bare 64-hex digest, or an object still
// carrying the $hashed:true marker. context names what was inspected, for a
// legible failure message.
func assertNoDigestOrHashedMarker(t *testing.T, props json.RawMessage, context string) {
	t.Helper()
	if len(props) == 0 {
		return
	}
	var parsed any
	require.NoError(t, json.Unmarshal(props, &parsed), "%s: must be valid JSON", context)

	var walk func(v any, path string)
	walk = func(v any, path string) {
		switch val := v.(type) {
		case map[string]any:
			if h, ok := val["$hashed"].(bool); ok && h {
				t.Errorf("%s: found a $hashed marker at %s — a plugin update write-input must never carry a hashed secret", context, path)
			}
			for k, cv := range val {
				walk(cv, path+"."+k)
			}
		case string:
			if hexDigestPattern.MatchString(val) {
				t.Errorf("%s: found a 64-hex digest-shaped value at %s: %s — a plugin update write-input must never carry a hashed secret", context, path, val)
			}
		case []any:
			for i, cv := range val {
				walk(cv, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(parsed, "$")
}

// applyPatchDocumentForTest applies a simple RFC6902-shaped ("op"/"path"/"value",
// add/replace/remove) patch document to doc, mimicking a plugin that
// reconstructs its write body from PriorProperties + PatchDocument rather than
// using DesiredProperties directly. Only handles the flat top-level paths this
// package's patches produce.
func applyPatchDocumentForTest(t *testing.T, doc json.RawMessage, patchDoc *string) json.RawMessage {
	t.Helper()
	if patchDoc == nil || *patchDoc == "" {
		return doc
	}
	var ops []map[string]any
	require.NoError(t, json.Unmarshal([]byte(*patchDoc), &ops))

	result := string(doc)
	for _, op := range ops {
		opType, _ := op["op"].(string)
		path, _ := op["path"].(string)
		sjsonPath := strings.ReplaceAll(strings.TrimPrefix(path, "/"), "/", ".")
		var err error
		switch opType {
		case "add", "replace":
			result, err = sjson.Set(result, sjsonPath, op["value"])
		case "remove":
			result, err = sjson.Delete(result, sjsonPath)
		}
		require.NoError(t, err)
	}
	return json.RawMessage(result)
}

// nonEnrichingSecretRead is a plugin Read override for FakeAWS's
// SecretsManager::Secret that mimics a NON-enriching (writeOnly) secret: real
// FakeAWS (and the "enriching" tests in secret_hashing_test.go) simulate a
// secret store whose GetSecretValue-equivalent returns the bare secret on
// every Read, refreshing the stored $hashed envelope back to plaintext before
// the plugin-boundary guard (resolver.guardNoHashedValues) ever sees
// it again. Plenty of real secret fields never come back on Read at all (e.g.
// a writeOnly credential) — this override models that: it returns only
// Name, never SecretString, so the stored envelope stays hashed at rest
// exactly as it was after create. This is what exercises the path where:
// resource_updater.go's delete()/update() converting DesiredState/PriorState
// with the GUARDED converter even though those conversions carry prior/
// existing state, not a new value being written.
func nonEnrichingSecretRead(name string) func(r *resource.ReadRequest) (*resource.ReadResult, error) {
	return func(r *resource.ReadRequest) (*resource.ReadResult, error) {
		props, _ := json.Marshal(map[string]string{"Name": name})
		return &resource.ReadResult{
			ResourceType: r.ResourceType,
			Properties:   string(props),
		}, nil
	}
}

// TestNonEnrichingSecretGuard_DestroySucceeds is the RED->GREEN case for the
// delete() half of the operation-aware guard fix: destroying a resource whose
// opaque secret is hashed at rest, and whose plugin Read never re-enriches it
// (a non-enriching/writeOnly secret), must succeed. Before the fix, the
// pre-delete synchronize() Read merges in only what the plugin actually
// returns (Name), leaving the stored $hashed envelope untouched in
// DesiredState.Properties; delete() then ran that through the GUARDED
// convertResourceForPlugin and failed with "hashed values are terminal" even
// though a delete never writes the secret value anywhere.
func TestNonEnrichingSecretGuard_DestroySucceeds(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const plaintextSecret = "non-enriching-super-secret"
		const name = "my-secret"

		overrides := &plugin.ResourcePluginOverrides{
			Read: nonEnrichingSecretRead(name),
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{{
				Label:      "my-secret",
				Type:       "FakeAWS::SecretsManager::Secret",
				Stack:      stack,
				Target:     "test-target",
				Schema:     secretSchema(),
				Properties: json.RawMessage(`{"Name":"` + name + `","SecretString":"` + plaintextSecret + `"}`),
			}},
			Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		createCmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, createCmd)
		require.Equal(t, forma_command.CommandStateSuccess, createCmd.State)

		// Sanity: the secret is genuinely hashed at rest before we destroy it.
		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, resources, 1)
		assert.Contains(t, string(resources[0].Properties), hashedMarker,
			"precondition: the secret must be hashed at rest before destroy")

		_, err = m.DestroyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		destroyCmd := findCommandByType(cmds, pkgmodel.CommandDestroy)
		require.NotNil(t, destroyCmd, "destroy command should exist")
		require.Equal(t, forma_command.CommandStateSuccess, destroyCmd.State,
			"destroying a resource with a non-enriching hashed secret must succeed, not fail at the plugin-boundary guard")
		require.Len(t, destroyCmd.ResourceUpdates, 1)
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, destroyCmd.ResourceUpdates[0].State)

		resourcesAfter, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		assert.Empty(t, resourcesAfter, "resource should be gone after a successful destroy")
	})
}

// TestNonEnrichingSecretGuard_UpdateNonSecretFieldSucceeds is the RED->GREEN
// case for the update() half of the fix: reconciling a change to a NON-secret
// field (secret value resubmitted unchanged) on a resource whose opaque
// secret is hashed at rest, and whose plugin Read never re-enriches it, must
// succeed. Before the fix, the pre-update synchronize() Read only merges in
// what the plugin returns for PriorState (Name), leaving PriorState's stored
// $hashed envelope untouched; update() then ran PriorState through the
// GUARDED convertResourceForPlugin (-> PriorProperties, diff context, never
// written) and failed with "hashed values are terminal".
func TestNonEnrichingSecretGuard_UpdateNonSecretFieldSucceeds(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const plaintextSecret = "non-enriching-super-secret"
		const name = "my-secret"

		var received json.RawMessage
		overrides := &plugin.ResourcePluginOverrides{
			Read: nonEnrichingSecretRead(name),
			Update: func(r *resource.UpdateRequest) (*resource.UpdateResult, error) {
				received = r.DesiredProperties

				// PriorProperties must never carry a hashed digest in
				// place of the live secret — directly, or reconstructable by
				// applying PatchDocument on top of it (how a plugin that builds
				// its write body from prior+patch, rather than DesiredProperties,
				// would assemble the value it actually sends to the cloud).
				assertNoDigestOrHashedMarker(t, r.PriorProperties, "PriorProperties")
				reconstructed := applyPatchDocumentForTest(t, r.PriorProperties, r.PatchDocument)
				assertNoDigestOrHashedMarker(t, reconstructed, "PriorProperties+PatchDocument reconstruction")

				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						RequestID:          "update-1",
						NativeID:           "5678",
						ResourceProperties: r.DesiredProperties,
					},
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		schema := secretSchema()
		stack := "test-stack-" + util.NewID()
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{{
				Label:      "my-secret",
				Type:       "FakeAWS::SecretsManager::Secret",
				Stack:      stack,
				Target:     "test-target",
				Schema:     schema,
				Properties: json.RawMessage(`{"Name":"` + name + `","SecretString":"` + plaintextSecret + `"}`),
			}},
			Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		createCmd := findCommandByType(cmds, pkgmodel.CommandApply)
		require.NotNil(t, createCmd)
		require.Equal(t, forma_command.CommandStateSuccess, createCmd.State)

		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, resources, 1)
		assert.Contains(t, string(resources[0].Properties), hashedMarker,
			"precondition: the secret must be hashed at rest before update")

		// Full-property reconcile apply changing only the non-secret Description
		// field; SecretString is resubmitted unchanged, and Name (what the
		// non-enriching Read actually reports) is unchanged too, so this is a
		// genuine "same underlying secret, different field" update rather than
		// drift on the field the Read override tracks.
		formaUpdate := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{{
				Label:      "my-secret",
				Type:       "FakeAWS::SecretsManager::Secret",
				Stack:      stack,
				Target:     "test-target",
				Schema:     schema,
				Properties: json.RawMessage(`{"Name":"` + name + `","Description":"updated-description","SecretString":"` + plaintextSecret + `"}`),
			}},
			Targets: []pkgmodel.Target{},
		}
		_, err = m.ApplyForma(formaUpdate, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		var updateCmd *forma_command.FormaCommand
		applyCount := 0
		for _, c := range cmds {
			if c.Command == pkgmodel.CommandApply {
				applyCount++
				if applyCount == 2 {
					updateCmd = c
				}
			}
		}
		require.NotNil(t, updateCmd, "the second (update) apply command should exist")
		require.Equal(t, forma_command.CommandStateSuccess, updateCmd.State,
			"updating a non-secret field on a resource with a non-enriching hashed secret must succeed, "+
				"not be rejected by the plugin-boundary guard (guardNoHashedValues)")
		require.Len(t, updateCmd.ResourceUpdates, 1)
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, updateCmd.ResourceUpdates[0].State)

		require.NotNil(t, received, "plugin Update override should have been called")
		assert.NotContains(t, string(received), "$hashed",
			"the plugin must never receive a hashed value in place of the live secret")

		resourcesAfter, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, resourcesAfter, 1)
		var props map[string]any
		require.NoError(t, json.Unmarshal(resourcesAfter[0].Properties, &props))
		assert.Equal(t, "updated-description", props["Description"], "the non-secret field change must have applied")
		secretString, ok := props["SecretString"].(map[string]any)
		require.True(t, ok, "SecretString must remain an Opaque envelope")
		assert.Equal(t, true, secretString["$hashed"])
	})
}
