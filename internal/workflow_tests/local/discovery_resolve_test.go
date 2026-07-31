// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package workflow_tests_local

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/discovery"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// discoveryListCapture records the TargetConfig the plugin receives on each
// List call so the assertion can inspect what reached the plugin boundary.
type discoveryListCapture struct {
	mu      sync.Mutex
	configs []json.RawMessage
}

func (c *discoveryListCapture) add(cfg json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make(json.RawMessage, len(cfg))
	copy(cp, cfg)
	c.configs = append(c.configs, cp)
}

func (c *discoveryListCapture) all() []json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]json.RawMessage, len(c.configs))
	copy(out, c.configs)
	return out
}

// TestDiscoveryResolve_TargetConfigOpaqueRefIsResolvedBeforeList asserts that
// when a discoverable target's Config carries an opaque $ref pointing to a
// managed secret's field, the discovery List call to the plugin receives the
// RESOLVED credential value — not the raw {"$ref":…} object stored at rest.
//
// Setup:
//   - Apply 1 creates a FakeAWS::SecretsManager::Secret ("my-secret") and a
//     consumer target ("discovery-target") marked Discoverable=true, whose
//     Config carries an opaque $ref to the secret's SecretString.
//   - The stored Config retains only the $ref pointer (StripOpaqueRefValues
//     drops $value for opaque refs before the row is persisted).
//   - A fake plugin is registered; its List override records every TargetConfig
//     it receives, and its Read override returns the secret's value so the
//     ResolveCache can serve it when the discovery path resolves the target.
//   - Discovery is triggered; the recorded List TargetConfig is asserted to
//     carry the resolved credential and to contain no "$ref" substring.
func TestDiscoveryResolve_TargetConfigOpaqueRefIsResolvedBeforeList(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const plaintextSecret = "discovery-target-config-secret-value"

		capture := &discoveryListCapture{}

		// The List override records TargetConfig for every call so we can later
		// assert every List invocation against this target received resolved creds.
		// The Read override serves the secret's resolved value so the ResolveCache
		// can populate it when the discovery path resolves the target's config.
		overrides := &plugin.ResourcePluginOverrides{
			List: func(req *resource.ListRequest) (*resource.ListResult, error) {
				capture.add(req.TargetConfig)
				// Return one discoverable resource so the test can confirm discovery ran.
				if req.ResourceType == "FakeAWS::S3::Bucket" {
					return &resource.ListResult{NativeIDs: []string{"discovered-bucket-1"}}, nil
				}
				return &resource.ListResult{NativeIDs: nil}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				switch req.ResourceType {
				case "FakeAWS::SecretsManager::Secret":
					// Return the secret's resolved value so reads by the ResolveCache succeed.
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   fmt.Sprintf(`{"Name":"my-secret","SecretString":%q}`, plaintextSecret),
					}, nil
				case "FakeAWS::S3::Bucket":
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   fmt.Sprintf(`{"BucketName":%q}`, req.NativeID),
					}, nil
				}
				return nil, fmt.Errorf("unexpected resource type in Read: %s", req.ResourceType)
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		// Apply 1: create the managed secret and the discoverable consumer target
		// whose Config carries an opaque $ref to the secret's SecretString.
		// Because consumer IS new in this apply a TargetUpdate fires and
		// propagateResolvedTargetConfig resolves the $ref for any resource ops.
		// The datastore strips the resolved $value before persisting
		// (StripOpaqueRefValues), so the stored config retains only the raw $ref.
		// arbitrary stable KSUID for the test secret — must match the $ref below.
		secretKsuid := "2MiD2rA1SJbLMGZgTL0hCxjkjjw"
		stack := "discovery-resolve-test-stack"

		// secretManagerSchema mirrors the FakeAWS SecretsManager::Secret schema
		// (see internal/testplugin/fakeaws/fake_aws.go): SecretString is Opaque so
		// the system treats its resolved value as a credential that must be hashed
		// at rest and resolved on-demand when consumed by another entity.
		secretManagerSchema := pkgmodel.Schema{
			Identifier: "Id",
			Fields:     []string{"Name", "Description", "SecretString", "Tags"},
			Hints: map[string]pkgmodel.FieldHint{
				"SecretString": {Opaque: true},
			},
		}

		applyOne := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{{
				Label:      "my-secret",
				Type:       "FakeAWS::SecretsManager::Secret",
				Stack:      stack,
				Target:     "provider",
				Ksuid:      secretKsuid,
				Schema:     secretManagerSchema,
				Properties: json.RawMessage(`{"Name":"my-secret","SecretString":"` + plaintextSecret + `"}`),
			}},
			Targets: []pkgmodel.Target{
				{Label: "provider", Namespace: "FakeAWS"},
				{
					Label:     "discovery-target",
					Namespace: "FakeAWS",
					// The opaque $ref to the secret's SecretString field. This is the
					// value that must be resolved before List reaches the plugin.
					Config: json.RawMessage(fmt.Sprintf(`{
						"apiKey": {"$ref": "formae://%s#/SecretString", "$visibility": "Opaque"}
					}`, secretKsuid)),
					Discoverable: true,
				},
			},
		}

		_, err = m.ApplyForma(applyOne, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "discovery-resolve-client")
		require.NoError(t, err)

		// Wait for the apply to complete before triggering discovery.
		require.Eventually(t, func() bool {
			cmds, loadErr := m.Datastore.LoadIncompleteFormaCommands()
			return loadErr == nil && len(cmds) == 0
		}, 10*time.Second, 100*time.Millisecond, "apply must reach a terminal state before discovery")

		// Confirm the stored target config retains only the $ref pointer (no $value,
		// no plaintext) — proving the setup is correct and the gap is real.
		discoveryTarget, err := m.Datastore.LoadTarget("discovery-target")
		require.NoError(t, err)
		require.NotNil(t, discoveryTarget, "discovery-target must have been persisted by apply 1")
		require.Contains(t, string(discoveryTarget.Config), "$ref",
			"stored target config must retain the $ref pointer")
		require.NotContains(t, string(discoveryTarget.Config), plaintextSecret,
			"stored target config must not persist the resolved plaintext")

		// Start the test helper actor so testutil.Send can relay messages.
		_, err = testutil.StartTestHelperActor(m.Node, make(chan any, 1))
		require.NoError(t, err)

		// Trigger discovery. The discovery path must resolve the opaque $ref in
		// discovery-target's Config before passing TargetConfig to the plugin's List.
		err = testutil.Send(m.Node, "Discovery", discovery.Discover{})
		require.NoError(t, err)

		// Wait until the discovered bucket appears in the unmanaged stack — this
		// confirms the List override was actually called.
		require.Eventually(t, func() bool {
			resources, loadErr := m.Datastore.LoadResourcesByStack("$unmanaged")
			return loadErr == nil && len(resources) >= 1
		}, 10*time.Second, 100*time.Millisecond, "discovery must persist the discovered resource")

		// Core assertion: every List call against discovery-target must have
		// received the RESOLVED credential — not the raw $ref object.
		configs := capture.all()
		require.NotEmpty(t, configs, "List override must have been called at least once")

		for i, cfg := range configs {
			cfgStr := string(cfg)
			assert.NotContains(t, cfgStr, `"$ref"`,
				"List call %d: TargetConfig must not contain a raw $ref — it must be resolved before reaching the plugin", i)
			assert.Contains(t, cfgStr, plaintextSecret,
				"List call %d: TargetConfig must contain the resolved credential value", i)
		}
	})
}
