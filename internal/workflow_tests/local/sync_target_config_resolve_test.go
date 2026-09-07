// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

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
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// syncReadCapture records the TargetConfig the plugin receives on each Read of
// the resource that lives on the secret-authed target, so the assertion can
// inspect what reached the plugin boundary during a sync cycle.
type syncReadCapture struct {
	mu      sync.Mutex
	configs []json.RawMessage
}

func (c *syncReadCapture) add(cfg json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make(json.RawMessage, len(cfg))
	copy(cp, cfg)
	c.configs = append(c.configs, cp)
}

func (c *syncReadCapture) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.configs = nil
}

func (c *syncReadCapture) all() []json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]json.RawMessage, len(c.configs))
	copy(out, c.configs)
	return out
}

// TestSyncResolve_TargetConfigOpaqueRefIsResolvedBeforeRead asserts that when a
// managed resource lives on a target whose Config carries an opaque $ref to a
// managed secret (reference-don't-store: no $value at rest), a background SYNC
// resolves that target's config live and the plugin Read receives the RESOLVED
// credential — not the stripped/unresolved config.
//
// This is the sync-path analogue of
// TestDiscoveryResolve_TargetConfigOpaqueRefIsResolvedBeforeList. It guards the
// gap where sync resource ops are OperationRead, which the changeset
// edge-builders did not wire to the synthetic target Resolve node — so the Read
// dispatched with unresolved credentials (with a real provider that hangs the
// Read forever; with a fake plugin it simply receives a credential-less config).
//
// Setup:
//   - Apply 1 creates a FakeAWS::SecretsManager::Secret ("my-secret") and a
//     consumer target ("consumer") whose Config carries an opaque $ref to the
//     secret's SecretString, plus a managed FakeAWS::S3::Bucket ("r1") on that
//     consumer target.
//   - The stored consumer Config retains only the $ref pointer (no $value).
//   - A fake plugin captures the TargetConfig every Read of "r1" receives, and
//     serves the secret's value so the ResolveCache can resolve the target.
//   - ForceSync is triggered; the recorded Read TargetConfig for "r1" must carry
//     the resolved credential.
func TestSyncResolve_TargetConfigOpaqueRefIsResolvedBeforeRead(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const plaintextSecret = "sync-target-config-secret-value"
		const bucketNative = "r1-native"
		const secretNative = "my-secret-native"

		capture := &syncReadCapture{}

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				nid := bucketNative
				if req.ResourceType == "FakeAWS::SecretsManager::Secret" {
					nid = secretNative
				}
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          req.Label,
					NativeID:           nid,
					ResourceProperties: req.Properties,
				}}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				switch req.ResourceType {
				case "FakeAWS::SecretsManager::Secret":
					// Serve the secret so the ResolveCache can resolve the target $ref.
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   fmt.Sprintf(`{"Name":"my-secret","SecretString":%q}`, plaintextSecret),
					}, nil
				case "FakeAWS::S3::Bucket":
					// r1 lives on the secret-authed target: record the config that
					// reached the plugin on this Read, then return its live state.
					capture.add(req.TargetConfig)
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   fmt.Sprintf(`{"BucketName":%q}`, req.NativeID),
					}, nil
				}
				return nil, fmt.Errorf("unexpected resource type in Read: %s", req.ResourceType)
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false // manual sync control
		cfg.Agent.Retry.MaxRetries = 0
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		secretKsuid := "2MiD2rA1SJbLMGZgTL0hCxjkjjw"
		stack := "sync-resolve-test-stack"

		secretManagerSchema := pkgmodel.Schema{
			Identifier: "Id",
			Fields:     []string{"Name", "Description", "SecretString", "Tags"},
			Hints: map[string]pkgmodel.FieldHint{
				"SecretString": {Opaque: true},
			},
		}
		bucketSchema := pkgmodel.Schema{
			Identifier: "BucketName",
			Fields:     []string{"BucketName"},
		}

		applyOne := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{
				{
					Label:      "my-secret",
					Type:       "FakeAWS::SecretsManager::Secret",
					Stack:      stack,
					Target:     "provider",
					Ksuid:      secretKsuid,
					Schema:     secretManagerSchema,
					Properties: json.RawMessage(`{"Name":"my-secret","SecretString":"` + plaintextSecret + `"}`),
				},
				{
					Label:      "r1",
					Type:       "FakeAWS::S3::Bucket",
					Stack:      stack,
					Target:     "consumer",
					Schema:     bucketSchema,
					Properties: json.RawMessage(`{"BucketName":"` + bucketNative + `"}`),
				},
			},
			Targets: []pkgmodel.Target{
				{Label: "provider", Namespace: "FakeAWS"},
				{
					Label:     "consumer",
					Namespace: "FakeAWS",
					Config: json.RawMessage(fmt.Sprintf(`{
						"apiKey": {"$ref": "formae://%s#/SecretString", "$visibility": "Opaque"}
					}`, secretKsuid)),
				},
			},
		}

		_, err = m.ApplyForma(applyOne, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "sync-resolve-client", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		// The stored consumer config must retain only the $ref pointer (no plaintext).
		consumerTarget, err := m.Datastore.LoadTarget("consumer")
		require.NoError(t, err)
		require.NotNil(t, consumerTarget, "consumer target must have been persisted by apply 1")
		require.Contains(t, string(consumerTarget.Config), "$ref",
			"stored consumer config must retain the $ref pointer")
		require.NotContains(t, string(consumerTarget.Config), plaintextSecret,
			"stored consumer config must not persist the resolved plaintext")

		// Isolate the sync cycle's reads from any apply-time reads.
		capture.reset()

		// Force a sync. The sync Read of r1 (on the secret-authed consumer target)
		// must resolve the target's opaque $ref config live before dispatching.
		require.NoError(t, m.ForceSync())
		require.Eventually(t, func() bool {
			return len(capture.all()) > 0
		}, 15*time.Second, 50*time.Millisecond, "sync must read r1 on the secret-authed target")

		// Core assertion: every sync Read of r1 received the RESOLVED credential —
		// not a stripped/unresolved config.
		configs := capture.all()
		require.NotEmpty(t, configs, "sync Read of r1 must have been called at least once")
		for i, c := range configs {
			cfgStr := string(c)
			assert.Contains(t, cfgStr, plaintextSecret,
				"sync Read %d: TargetConfig must contain the resolved credential (not a stripped/unresolved $ref)", i)
			assert.NotContains(t, cfgStr, `"$ref"`,
				"sync Read %d: TargetConfig must not contain a raw $ref — it must be resolved before reaching the plugin", i)
		}
	})
}
