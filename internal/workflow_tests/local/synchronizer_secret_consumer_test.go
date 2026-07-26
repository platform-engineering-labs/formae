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

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestSynchronizer_SecretConsumerOutOfBandDrift characterizes the multi-resource
// secret-drift scenario: a secret-bearing resource S (opaque "secret" field) and a
// consumer R whose "consumes" field resolves S's opaque secret via a $res
// resolvable. It drives apply -> OOB-drift+sync -> reconcile -> absorb and logs the
// exact observed datastore/plugin state at each step. It asserts nothing strong
// beyond "the commands ran"; the point is the logged evidence, not a green bar.
func TestSynchronizer_SecretConsumerOutOfBandDrift(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const sLabel = "the-secret"
		const rLabel = "the-consumer"
		const sNative = "s-native"
		const rNative = "r-native"

		var mu sync.Mutex
		sCurrentSecret := "v1" // live value S's Read reports; flipped for OOB drift
		// captured plugin write-inputs, most-recent-wins per label
		createReceived := map[string]json.RawMessage{}
		updateReceived := map[string]json.RawMessage{}

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				mu.Lock()
				createReceived[req.Label] = append(json.RawMessage{}, req.Properties...)
				mu.Unlock()
				nid := rNative
				if req.Label == sLabel {
					nid = sNative
				}
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          req.Label,
					NativeID:           nid,
					ResourceProperties: req.Properties,
				}}, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				mu.Lock()
				updateReceived[req.Label] = append(json.RawMessage{}, req.DesiredProperties...)
				mu.Unlock()
				nid := rNative
				if req.Label == sLabel {
					nid = sNative
				}
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationUpdate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          req.Label,
					NativeID:           nid,
					ResourceProperties: req.DesiredProperties,
				}}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				mu.Lock()
				cur := sCurrentSecret
				mu.Unlock()
				switch req.NativeID {
				case sNative:
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   fmt.Sprintf(`{"name":%q,"secret":%q}`, sLabel, cur),
					}, nil
				case rNative:
					// The consumer's cloud-native view: it holds the resolved
					// plaintext secret. Report the current live secret so R does not
					// perpetually drift, mirroring what the cloud would return.
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   fmt.Sprintf(`{"name":%q,"consumes":%q}`, rLabel, cur),
					}, nil
				default:
					return &resource.ReadResult{ResourceType: req.ResourceType, Properties: `{}`}, nil
				}
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false // manual sync control
		cfg.Agent.Retry.MaxRetries = 0
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		sKsuid := util.NewID()

		sSchema := pkgmodel.Schema{
			Identifier: "name",
			Fields:     []string{"name", "secret"},
			Hints:      map[string]pkgmodel.FieldHint{"secret": {Opaque: true}},
		}
		rSchema := pkgmodel.Schema{
			Identifier: "name",
			Fields:     []string{"name", "consumes"},
		}

		// R.consumes resolves S.secret via a $res resolvable (whole-field form,
		// as in res_test.go). The system rewrites this to a $ref + $value at apply.
		rProps := func() json.RawMessage {
			env := map[string]any{
				"name": rLabel,
				"consumes": map[string]any{
					"$res":      true,
					"$label":    sLabel,
					"$type":     "FakeAWS::Resource",
					"$stack":    stack,
					"$property": "secret",
				},
			}
			b, _ := json.Marshal(env)
			return b
		}

		buildForma := func(secretVal string) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{{Label: stack}},
				Resources: []pkgmodel.Resource{
					{
						Label:      sLabel,
						Type:       "FakeAWS::Resource",
						Stack:      stack,
						Target:     "test-target",
						Ksuid:      sKsuid,
						Schema:     sSchema,
						Properties: json.RawMessage(fmt.Sprintf(`{"name":%q,"secret":%q}`, sLabel, secretVal)),
					},
					{
						Label:      rLabel,
						Type:       "FakeAWS::Resource",
						Stack:      stack,
						Target:     "test-target",
						Schema:     rSchema,
						Properties: rProps(),
					},
				},
				Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			}
		}

		dumpState := func(label string) {
			t.Logf("=================== %s ===================", label)
			resources, err := m.Datastore.LoadResourcesByStack(stack)
			require.NoError(t, err)
			for _, r := range resources {
				t.Logf("  [datastore] %s/%s: props=%s", r.Type, r.Label, string(r.Properties))
			}
			mu.Lock()
			for lbl, p := range createReceived {
				t.Logf("  [plugin Create received] %s: %s", lbl, string(p))
			}
			for lbl, p := range updateReceived {
				t.Logf("  [plugin Update received] %s: %s", lbl, string(p))
			}
			mu.Unlock()
		}

		dumpCommand := func(cmd *forma_command.FormaCommand, label string) {
			if cmd == nil {
				t.Logf("  [%s] <nil command>", label)
				return
			}
			t.Logf("  [%s] command=%s state=%s updates=%d",
				label, cmd.Command, cmd.State, len(cmd.ResourceUpdates))
			for _, ru := range cmd.ResourceUpdates {
				consumes := gjson.GetBytes(ru.DesiredState.Properties, "consumes")
				t.Logf("     - %s op=%s state=%s failure=%q | desired.props=%s | desired.consumes=%s",
					ru.DesiredState.Label, ru.Operation, ru.State, ru.FailureReason,
					string(ru.DesiredState.Properties), consumes.Raw)
			}
		}

		// field loads resource `label` and returns the gjson.Result at `path`.
		field := func(label, path string) gjson.Result {
			resources, err := m.Datastore.LoadResourcesByStack(stack)
			require.NoError(t, err)
			for _, r := range resources {
				if r.Label == label {
					return gjson.GetBytes(r.Properties, path)
				}
			}
			return gjson.Result{}
		}
		received := func(kind, label string) string {
			mu.Lock()
			defer mu.Unlock()
			if kind == "create" {
				return string(createReceived[label])
			}
			return string(updateReceived[label])
		}

		// nthApplyCmd returns the Nth (1-based) CommandApply in insertion order.
		nthApplyCmd := func(n int) *forma_command.FormaCommand {
			cmds, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			count := 0
			for _, c := range cmds {
				if c.Command == pkgmodel.CommandApply {
					count++
					if count == n {
						return c
					}
				}
			}
			return nil
		}

		// ───────────────────────── STEP 1: APPLY ─────────────────────────
		_, err = m.ApplyForma(buildForma("v1"), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForApplyComplete(t, m)
		t.Log("########## STEP 1: initial apply (S.secret=v1, R.consumes=$res->S.secret) ##########")
		dumpCommand(nthApplyCmd(1), "apply#1")
		dumpState("after STEP 1 apply")

		hashV1 := pkgmodel.ComputeValueHash("v1")
		hashV2 := pkgmodel.ComputeValueHash("v2")

		// STEP 1 characterization: S.secret hashed(v1); R.consumes RESOLVED to
		// S's opaque value, stored hashed(v1) with the inherited Opaque envelope
		// and a $ref back to S; the R plugin Create received the LIVE plaintext v1.
		require.Equal(t, hashV1, field(sLabel, "secret.$value").String(), "S.secret must be hashed(v1) at rest")
		require.True(t, field(sLabel, "secret.$hashed").Bool())
		require.Equal(t, hashV1, field(rLabel, "consumes.$value").String(), "R.consumes must be stored hashed(v1)")
		require.True(t, field(rLabel, "consumes.$hashed").Bool(), "R.consumes carries inherited Opaque $hashed")
		require.Contains(t, field(rLabel, "consumes.$ref").String(), "#/secret", "R.consumes keeps a $ref to S.secret")
		require.Equal(t, "v1", gjson.Get(received("create", rLabel), "consumes").String(),
			"R plugin Create must receive the LIVE plaintext v1 (guard did not fire; apply did NOT error)")

		// ─────────────── STEP 2: OOB drift on S + sync ────────────────────
		mu.Lock()
		sCurrentSecret = "v2" // S's live secret changed out of band
		mu.Unlock()
		t.Log("########## STEP 2: OOB drift S.secret v1->v2, then ForceSync ##########")
		require.NoError(t, m.ForceSync())
		require.Eventually(t, func() bool {
			resources, err := m.Datastore.LoadResourcesByStack(stack)
			if err != nil {
				return false
			}
			for _, r := range resources {
				if r.Label == sLabel {
					h := gjson.GetBytes(r.Properties, "secret.$value").String()
					return h == pkgmodel.ComputeValueHash("v2")
				}
			}
			return false
		}, 10*time.Second, 100*time.Millisecond, "sync should ingest S.secret->hashed(v2)")
		dumpState("after STEP 2 sync")

		// STEP 2: sync ingested S.secret -> hashed(v2). The consumer R's "consumes"
		// field is an inherited-Opaque resolvable ($ref back to S.secret). When the
		// sync Read refreshes its $value with S's new live value, the merge must drop
		// the stale $hashed marker so the persist transformer re-hashes it at rest.
		// The field must therefore be stored as {"$hashed":true,"$value":sha256("v2")}
		// — the resolved secret hashed, NEVER the literal cleartext. This is the
		// integrity/no-leak guarantee at the heart of PLA-355.
		require.Equal(t, hashV2, field(sLabel, "secret.$value").String(), "S.secret must be ingested as hashed(v2)")
		require.True(t, field(rLabel, "consumes.$hashed").Bool(), "R.consumes stays flagged $hashed:true after sync")
		require.Equal(t, hashV2, field(rLabel, "consumes.$value").String(),
			"R.consumes.$value must be re-hashed to sha256(v2) on sync (no plaintext at rest)")
		require.NotContains(t, field(rLabel, "consumes").Raw, "\"v2\"",
			"the cleartext secret 'v2' must never appear in R's stored consumes envelope")

		// ─────── STEP 3: reconcile SAME forma (desired S=v1) ──────────────
		t.Log("########## STEP 3: reconcile re-apply SAME forma (desired S.secret=v1) ##########")
		_, err3 := m.ApplyForma(buildForma("v1"), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		// Plain reconcile with the OLD desired (v1) is rejected because the OOB drift
		// (S.secret is now v2 at rest) makes the stack "modified since the last
		// reconcile". This is legitimate drift protection — desired v1 genuinely
		// differs from the ingested current v2 — and is unrelated to the PLA-355 fix.
		require.Error(t, err3, "reconcile with stale desired (v1) after OOB drift to v2 must be rejected")
		if err3 != nil {
			t.Logf("STEP 3 reconcile ApplyForma RETURNED ERROR (submission-time rejection): %v", err3)
		} else {
			t.Log("STEP 3 reconcile ApplyForma accepted; waiting for completion")
			waitForApplyComplete(t, m)
			dumpCommand(nthApplyCmd(2), "apply#2 (reconcile desired v1)")
		}
		dumpState("after STEP 3 reconcile")

		// ─────── STEP 4a: absorb — desired S=v2, plain reconcile ──────────
		// Flip S's live value back to v2 as well so the absorbed desired matches
		// the live cloud state (the realistic "accept the OOB value" flow).
		mu.Lock()
		sCurrentSecret = "v2"
		updateReceived = map[string]json.RawMessage{} // isolate this apply's writes
		mu.Unlock()
		t.Log("########## STEP 4a: absorb via PLAIN reconcile — desired S.secret=v2 ##########")
		_, err4 := m.ApplyForma(buildForma("v2"), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		// With R.consumes correctly hashed(v2) at rest, the absorbed desired (v2,
		// matching the live/ingested value) is now accepted — R is no longer
		// perpetually "unabsorbed", so the guard clears WITHOUT Force. Before the
		// PLA-355 fix the corrupted (plaintext) R.consumes made even this plain
		// absorb reject; that over-rejection is now gone.
		require.NoError(t, err4, "plain-reconcile absorb (desired=v2) is accepted once R.consumes is not corrupt")
		waitForApplyComplete(t, m)
		dumpState("after STEP 4a plain absorb")
		// The absorb must keep both fields hashed at rest and never leak cleartext.
		require.Equal(t, hashV2, field(sLabel, "secret.$value").String(),
			"after plain absorb S.secret stays hashed(v2)")
		require.Equal(t, hashV2, field(rLabel, "consumes.$value").String(),
			"after plain absorb R.consumes stays hashed(v2) at rest")
		require.NotContains(t, field(rLabel, "consumes").Raw, "\"v2\"",
			"the cleartext secret 'v2' must never appear in R's stored consumes envelope")

		// Final: report R.consumes' stored value and whether it re-resolved to v2.
		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		for _, r := range resources {
			if r.Label == rLabel {
				t.Logf("FINAL R.consumes stored = %s", gjson.GetBytes(r.Properties, "consumes").Raw)
			}
		}
		mu.Lock()
		t.Logf("FINAL plugin Create received for R = %s", string(createReceived[rLabel]))
		t.Logf("FINAL plugin Update received for R = %s", string(updateReceived[rLabel]))
		t.Logf("FINAL plugin Update received for S = %s", string(updateReceived[sLabel]))
		mu.Unlock()
	})
}

// TestSynchronizer_SecretConsumer_ResEnvelope_OutOfBandDrift is the $res sibling
// of TestSynchronizer_SecretConsumerOutOfBandDrift. It reproduces PLA-355's
// unfixed twin: a consumer R whose "consumes" field is persisted at rest as a
// STRUCTURED $res resolvable (NOT a $ref envelope) pointing at S's Opaque
// "secret" property. Such a $res envelope enters at rest via non-translating
// paths (Synchronize/Discovery/Destroy/seed) — a USER apply would rewrite $res
// -> $ref, so the $ref twin never exercises this shape.
//
// The bug: on an OOB drift of S's secret v1->v2 followed by a sync, the merge
// has no $res branch, so it recurses field-by-field and OVERWRITES the
// envelope's $value with the plugin's LIVE plaintext v2; because the $res
// envelope never carried $visibility:Opaque, the persist transformer never
// hashes it — so cleartext "v2" is written to the datastore at rest.
//
// The fix must leave R.consumes.$value == sha256("v2") at rest, exactly like
// the $ref case, and NEVER the literal cleartext.
func TestSynchronizer_SecretConsumer_ResEnvelope_OutOfBandDrift(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const sLabel = "the-secret"
		const rLabel = "the-consumer"
		const sNative = "s-native"
		const rNative = "r-native"

		var mu sync.Mutex
		sCurrentSecret := "v1" // live value S's Read reports; flipped for OOB drift
		stack := "test-stack-" + util.NewID()

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				nid := rNative
				if req.Label == sLabel {
					nid = sNative
				}
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          req.Label,
					NativeID:           nid,
					ResourceProperties: req.Properties,
				}}, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				nid := rNative
				if req.Label == sLabel {
					nid = sNative
				}
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationUpdate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          req.Label,
					NativeID:           nid,
					ResourceProperties: req.DesiredProperties,
				}}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				mu.Lock()
				cur := sCurrentSecret
				mu.Unlock()
				switch req.NativeID {
				case sNative:
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   fmt.Sprintf(`{"name":%q,"secret":%q}`, sLabel, cur),
					}, nil
				case rNative:
					// The consumer's cloud-native view holds the resolved secret. A
					// provider that round-trips the resolvable it was handed echoes back
					// a $res envelope carrying the LIVE plaintext at $value — which is
					// what the sync merge sees and (unfixed) writes to $value at rest.
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties: fmt.Sprintf(
							`{"name":%q,"consumes":{"$res":true,"$label":%q,"$type":"FakeAWS::Resource","$stack":%q,"$property":"secret","$value":%q}}`,
							rLabel, sLabel, stack, cur),
					}, nil
				default:
					return &resource.ReadResult{ResourceType: req.ResourceType, Properties: `{}`}, nil
				}
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false // manual sync control
		cfg.Agent.Retry.MaxRetries = 0
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		sKsuid := util.NewID()

		sSchema := pkgmodel.Schema{
			Identifier: "name",
			Fields:     []string{"name", "secret"},
			Hints:      map[string]pkgmodel.FieldHint{"secret": {Opaque: true}},
		}
		rSchema := pkgmodel.Schema{
			Identifier: "name",
			Fields:     []string{"name", "consumes"},
		}

		buildForma := func(secretVal string) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{{Label: stack}},
				Resources: []pkgmodel.Resource{
					{
						Label:      sLabel,
						Type:       "FakeAWS::Resource",
						Stack:      stack,
						Target:     "test-target",
						Ksuid:      sKsuid,
						Schema:     sSchema,
						Properties: json.RawMessage(fmt.Sprintf(`{"name":%q,"secret":%q}`, sLabel, secretVal)),
					},
					{
						Label:      rLabel,
						Type:       "FakeAWS::Resource",
						Stack:      stack,
						Target:     "test-target",
						Schema:     rSchema,
						Properties: json.RawMessage(fmt.Sprintf(`{"name":%q,"consumes":%q}`, rLabel, secretVal)),
					},
				},
				Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			}
		}

		field := func(label, path string) gjson.Result {
			resources, err := m.Datastore.LoadResourcesByStack(stack)
			require.NoError(t, err)
			for _, r := range resources {
				if r.Label == label {
					return gjson.GetBytes(r.Properties, path)
				}
			}
			return gjson.Result{}
		}
		load := func(label string) *pkgmodel.Resource {
			resources, err := m.Datastore.LoadResourcesByStack(stack)
			require.NoError(t, err)
			for _, r := range resources {
				if r.Label == label {
					return r
				}
			}
			return nil
		}
		dumpState := func(label string) {
			t.Logf("=================== %s ===================", label)
			resources, err := m.Datastore.LoadResourcesByStack(stack)
			require.NoError(t, err)
			for _, r := range resources {
				t.Logf("  [datastore] %s/%s: props=%s", r.Type, r.Label, string(r.Properties))
			}
		}

		hashV1 := pkgmodel.ComputeValueHash("v1")
		hashV2 := pkgmodel.ComputeValueHash("v2")

		// ───────────────────────── STEP 1: APPLY ─────────────────────────
		// Author S and R without any cross-resource reference, so that both land
		// at rest as plain managed resources with correct native IDs. R.consumes
		// starts as a plain opaque-secret-carrying field; we then rewrite it, at
		// rest, into the STRUCTURED $res envelope that a non-translating path
		// would have persisted.
		_, err = m.ApplyForma(buildForma("v1"), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		require.NoError(t, err)
		waitForApplyComplete(t, m)
		dumpState("after STEP 1 apply")

		// ─── Seed R.consumes at rest as a STRUCTURED $res envelope ──────────
		// This is the prod-observed shape (throwaway stack, PLA-355 sibling):
		//   {"$res":true,"$label":<S>,"$type":<S-type>,"$stack":<stack>,
		//    "$property":"secret","$value":<hash-of-v1>}
		// — a resolvable pointing at S's Opaque "secret" property, carrying the
		// (correctly hashed) v1 value but NO $hashed/$visibility markers.
		r := load(rLabel)
		require.NotNil(t, r, "R must exist after apply")
		resEnvelope := map[string]any{
			"$res":      true,
			"$label":    sLabel,
			"$type":     "FakeAWS::Resource",
			"$stack":    stack,
			"$property": "secret",
			"$value":    hashV1,
		}
		newProps := map[string]any{"name": rLabel, "consumes": resEnvelope}
		rawProps, mErr := json.Marshal(newProps)
		require.NoError(t, mErr)
		r.Properties = json.RawMessage(rawProps)
		_, err = m.Datastore.StoreResource(r, "seed-res-envelope")
		require.NoError(t, err)

		require.True(t, field(rLabel, "consumes.$res").Bool(), "R.consumes seeded as a $res envelope")
		require.Equal(t, hashV1, field(rLabel, "consumes.$value").String(), "R.consumes seeded hashed(v1)")
		dumpState("after seeding $res envelope")

		// ─────────────── STEP 2: OOB drift on S + sync ────────────────────
		mu.Lock()
		sCurrentSecret = "v2" // S's live secret changed out of band
		mu.Unlock()
		t.Log("########## STEP 2: OOB drift S.secret v1->v2, then ForceSync ##########")
		require.NoError(t, m.ForceSync())
		require.Eventually(t, func() bool {
			resources, err := m.Datastore.LoadResourcesByStack(stack)
			if err != nil {
				return false
			}
			for _, res := range resources {
				if res.Label == sLabel {
					return gjson.GetBytes(res.Properties, "secret.$value").String() == hashV2
				}
			}
			return false
		}, 10*time.Second, 100*time.Millisecond, "sync should ingest S.secret->hashed(v2)")
		dumpState("after STEP 2 sync")

		// The heart of the test: after the sync refreshes R.consumes' $value from
		// S's new live secret, the resolved value MUST be HASHED at rest — sha256(v2)
		// — exactly like the $ref case. The cleartext "v2" must NEVER appear in the
		// stored $res envelope.
		require.Equal(t, hashV2, field(sLabel, "secret.$value").String(), "S.secret must be ingested as hashed(v2)")
		require.Equal(t, hashV2, field(rLabel, "consumes.$value").String(),
			"R.consumes.$value must be re-hashed to sha256(v2) on sync (no plaintext at rest)")
		require.NotContains(t, field(rLabel, "consumes").Raw, "v2",
			"the cleartext secret 'v2' must never appear in R's stored $res envelope")
		// Structural integrity: the resolvable envelope survives the sync merge.
		require.True(t, field(rLabel, "consumes.$res").Bool(), "R.consumes keeps its $res resolvable structure after sync")
		require.Equal(t, sLabel, field(rLabel, "consumes.$label").String(), "R.consumes keeps its $label pointing at S")
	})
}
