// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

// ----------------------------------------------------------------------------
// opaque + writeOnly + hidden secret values WITH A SIBLING-FIELD CHANGE.
//
// The companion hidden/sync files change only the secret (or nothing). These
// tests change a SIBLING field (`Description`, a plain mutable string) while the
// SECRET stays the SAME. A sibling diff forces the comparison gate to return
// hasChanges=true, so GeneratePatch runs even though the secret is unchanged,
// which exercises how an unchanged opaque secret is treated in that situation.
//
// Each test:
//   1. apply create with secret=V and Description="A"; dump stored Properties.
//   2. re-apply with secret=V (unchanged, or V2 for the rotate case) but
//      Description="B" (changed).
//   3. capture the PatchDocument / DesiredProperties / PriorProperties the
//      plugin's Update saw, plus stored Properties before/after.
//
// A 64-hex SHA-256 hash leaking into the patch is detected with hashRe.
//
// The tests observe behavior: they log the raw evidence and assert only the
// stable parts.
// ----------------------------------------------------------------------------

// hashRe matches a bare SHA-256 hex digest (the at-rest opaque $value form).
var hashRe = regexp.MustCompile(`[0-9a-f]{64}`)

// secretSchemaSurfaced returns the schema where the secret field IS surfaced:
// it is in Schema.Fields AND marked writeOnly (so it is in Schema.WriteOnly()).
func secretSchemaSurfaced() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Description", "SecretString"},
		Hints: map[string]pkgmodel.FieldHint{
			"SecretString": {WriteOnly: true},
		},
	}
}

// secretSchemaSurfacedNonWriteOnly returns the schema where the secret field IS
// surfaced (present in Schema.Fields, non-hidden) but is NOT marked writeOnly:
// there is no WriteOnly hint, so it is ABSENT from Schema.WriteOnly(). This
// isolates the `opaque` (visibility) axis from the `writeOnly` axis to test
// whether the churn/corruption is intrinsic to opaque hashing in GeneratePatch.
func secretSchemaSurfacedNonWriteOnly() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Description", "SecretString"},
		Hints:      map[string]pkgmodel.FieldHint{},
	}
}

// secretSchemaHidden returns the schema where the secret field is HIDDEN: it is
// ABSENT from Fields and therefore from WriteOnly(). Description IS surfaced.
func secretSchemaHidden() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Description"},
		Hints:      map[string]pkgmodel.FieldHint{},
	}
}

// siblingForma builds a one-resource forma with the given schema/properties.
func siblingForma(schema pkgmodel.Schema, nativeID, props string) *pkgmodel.Forma {
	r := pkgmodel.Resource{
		Label:      "the-secret",
		Type:       "FakeAWS::SecretsManager::Secret",
		Stack:      "secrets-stack",
		Target:     "secrets-target",
		Schema:     schema,
		Properties: json.RawMessage(props),
		Managed:    true,
	}
	if nativeID != "" {
		r.NativeID = nativeID
	}
	return &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "secrets-stack"}},
		Resources: []pkgmodel.Resource{r},
		Targets: []pkgmodel.Target{{
			Label:     "secrets-target",
			Namespace: "secrets-namespace",
			Config:    json.RawMessage(`{}`),
		}},
	}
}

// siblingOverrides returns plugin overrides recording every Update. The Read
// callback returns BOTH surfaced fields (Name, Description) so a Description
// change is detectable, but never returns the writeOnly secret (realistic).
func siblingOverrides(mu *sync.Mutex, updates *[]capturedUpdate, description *string) *plugin.ResourcePluginOverrides {
	return &plugin.ResourcePluginOverrides{
		Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
			return &resource.CreateResult{
				ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					NativeID:           "secret-native-id",
					ResourceProperties: json.RawMessage(`{"Name":"my-secret","Description":"A"}`),
				},
			}, nil
		},
		Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
			mu.Lock()
			*updates = append(*updates, capturedUpdate{req.PatchDocument, req.DesiredProperties, req.PriorProperties})
			mu.Unlock()
			return &resource.UpdateResult{
				ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationUpdate,
					OperationStatus:    resource.OperationStatusSuccess,
					NativeID:           req.NativeID,
					ResourceProperties: json.RawMessage(`{"Name":"my-secret","Description":"B"}`),
				},
			}, nil
		},
		Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
			mu.Lock()
			d := *description
			mu.Unlock()
			return &resource.ReadResult{
				ResourceType: req.ResourceType,
				Properties:   `{"Name":"my-secret","Description":"` + d + `"}`,
			}, nil
		},
	}
}

// logSecretOp inspects a patch and reports whether it touches /SecretString and
// whether the value carried there is cleartext or a 64-hex hash.
func logSecretOp(t *testing.T, tag, patch string) {
	t.Helper()
	containsSecretPath := regexp.MustCompile(`/SecretString`).MatchString(patch)
	containsHash := hashRe.MatchString(patch)
	t.Logf("[%s]   -> patch touches /SecretString : %v", tag, containsSecretPath)
	t.Logf("[%s]   -> patch contains 64-hex HASH  : %v", tag, containsHash)
	if containsHash {
		t.Logf("[%s]   -> HASH FOUND IN PATCH (matches %q)", tag, hashRe.FindString(patch))
	}
}

// runSiblingScenario drives create(secret=createSecret, Description="A") then
// re-apply(secret=reapplySecret, Description="B"), logging all evidence.
func runSiblingScenario(t *testing.T, tag string, schema pkgmodel.Schema, createSecret, reapplySecret string) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var mu sync.Mutex
		var updates []capturedUpdate
		description := "A"

		overrides := siblingOverrides(&mu, &updates, &description)
		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		// (1) create: secret=createSecret, Description="A".
		createProps := `{"Name":"my-secret","Description":"A","SecretString":` + createSecret + `}`
		t.Logf("[%s] CREATE forma Properties = %s", tag, createProps)
		_, err = m.ApplyForma(siblingForma(schema, "", createProps),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)
		waitForSecretCommands(t, m.Datastore, 1)

		// Dump stored Properties (RESOURCE, at-rest) BEFORE re-apply.
		before := loadStoredProps(t, m.Datastore, tag+"-before-reapply")

		// Make the provider's subsequent Read reflect Description="A" (the
		// created state), so the only difference the next apply sees is the
		// Description change we send.
		mu.Lock()
		description = "A"
		mu.Unlock()

		// (2) re-apply: secret=reapplySecret, Description="B" (sibling changed).
		reapplyProps := `{"Name":"my-secret","Description":"B","SecretString":` + reapplySecret + `}`
		t.Logf("[%s] RE-APPLY forma Properties = %s", tag, reapplyProps)
		resp, err := m.ApplyForma(siblingForma(schema, "secret-native-id", reapplyProps),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)
		if resp != nil {
			t.Logf("[%s] RE-APPLY response: ChangesRequired=%v CommandID=%s",
				tag, resp.Simulation.ChangesRequired, resp.CommandID)
		}

		time.Sleep(1 * time.Second)

		mu.Lock()
		defer mu.Unlock()
		t.Logf("[%s] === EVIDENCE ===", tag)
		t.Logf("[%s] stored Properties BEFORE re-apply = %s", tag, before)
		t.Logf("[%s] plugin Update calls = %d", tag, len(updates))
		for i, u := range updates {
			pd := "<nil>"
			if u.patchDocument != nil {
				pd = *u.patchDocument
			}
			t.Logf("[%s] update[%d] PatchDocument     = %s", tag, i, pd)
			t.Logf("[%s] update[%d] DesiredProperties = %s", tag, i, string(u.desiredProps))
			t.Logf("[%s] update[%d] PriorProperties   = %s", tag, i, string(u.priorProps))
			if u.patchDocument != nil {
				logSecretOp(t, tag, *u.patchDocument)
			}
		}
		after := loadStoredProps(t, m.Datastore, tag+"-after-reapply")
		t.Logf("[%s] stored Properties AFTER re-apply  = %s", tag, after)

		cmdsAfter, _ := m.Datastore.LoadFormaCommands()
		t.Logf("[%s] total forma commands stored = %d", tag, len(cmdsAfter))
		for i, c := range cmdsAfter {
			if len(c.ResourceUpdates) > 0 {
				t.Logf("[%s] cmd[%d] op=%s storedPatch=%s", tag, i,
					c.ResourceUpdates[0].Operation,
					string(c.ResourceUpdates[0].DesiredState.PatchDocument))
			}
		}
	})
}

// -------------------------------------------------------------------------
// Case 1 (S-Update): SURFACED, opaque, strategy=Update, writeOnly.
// Sibling Description changes; secret unchanged. Observes whether a spurious
// /SecretString op appears, and whether its value is cleartext or a hash.
// -------------------------------------------------------------------------

func TestCharacterize_Sibling_SurfacedUpdate_SecretUnchanged(t *testing.T) {
	runSiblingScenario(t, "S-Update",
		secretSchemaSurfaced(),
		wrappedOpaque("super-secret"),
		wrappedOpaque("super-secret"),
	)
}

// -------------------------------------------------------------------------
// Case 2 (S-SetOnce): SURFACED, opaque, strategy=SetOnce, writeOnly.
// Sibling Description changes; secret "unchanged" (same V). Observes whether a
// /SecretString op appears that sets the value to the at-rest HASH.
// -------------------------------------------------------------------------

func TestCharacterize_Sibling_SurfacedSetOnce_SecretUnchanged(t *testing.T) {
	runSiblingScenario(t, "S-SetOnce",
		secretSchemaSurfaced(),
		wrappedOpaqueSetOnce("super-secret"),
		wrappedOpaqueSetOnce("super-secret"),
	)
}

// -------------------------------------------------------------------------
// Case 3 (S-SetOnce-rotate-attempt): SURFACED, opaque, SetOnce, writeOnly.
// Sibling changes AND the forma secret value DIFFERS (V2). setOnce should
// freeze the stored value. Observes whether a /SecretString op appears, and
// whether it carries the HASH or V2.
// -------------------------------------------------------------------------

func TestCharacterize_Sibling_SurfacedSetOnce_RotateAttempt(t *testing.T) {
	runSiblingScenario(t, "S-SetOnce-rotate",
		secretSchemaSurfaced(),
		wrappedOpaqueSetOnce("value-V"),
		wrappedOpaqueSetOnce("value-V2"),
	)
}

// -------------------------------------------------------------------------
// Case 4 (H-Update): HIDDEN, opaque, strategy=Update.
// Sibling changes; secret unchanged. Secret NOT in Schema.Fields/WriteOnly().
// Observes whether the /SecretString op is stripped (removeNonSchemaFields) so
// nothing reaches the plugin for the secret, while the Description op still goes.
// -------------------------------------------------------------------------

// -------------------------------------------------------------------------
// Case SNWO-Update: SURFACED, opaque, strategy=Update, NON-writeOnly.
// SecretString IS in Schema.Fields but NOT in WriteOnly(). Sibling Description
// changes; secret value unchanged (V). Observes whether a spurious /SecretString
// op appears and whether its value is the cleartext V or a 64-hex hash, which
// isolates the effect of `opaque` from `writeOnly`.
// -------------------------------------------------------------------------

func TestCharacterize_Sibling_SurfacedUpdate_NonWriteOnly_SecretUnchanged(t *testing.T) {
	runSiblingScenario(t, "SNWO-Update",
		secretSchemaSurfacedNonWriteOnly(),
		wrappedOpaque("super-secret"),
		wrappedOpaque("super-secret"),
	)
}

// -------------------------------------------------------------------------
// Case SNWO-SetOnce: SURFACED, opaque, strategy=SetOnce, NON-writeOnly.
// SecretString IS in Schema.Fields but NOT in WriteOnly(). Sibling Description
// changes; secret "unchanged" (same V). Observes whether a /SecretString op
// appears that carries the at-rest HASH, which isolates the effect of `opaque`
// from `writeOnly`.
// -------------------------------------------------------------------------

func TestCharacterize_Sibling_SurfacedSetOnce_NonWriteOnly_SecretUnchanged(t *testing.T) {
	runSiblingScenario(t, "SNWO-SetOnce",
		secretSchemaSurfacedNonWriteOnly(),
		wrappedOpaqueSetOnce("super-secret"),
		wrappedOpaqueSetOnce("super-secret"),
	)
}

func TestCharacterize_Sibling_HiddenUpdate_SecretUnchanged(t *testing.T) {
	runSiblingScenario(t, "H-Update",
		secretSchemaHidden(),
		wrappedOpaque("super-secret"),
		wrappedOpaque("super-secret"),
	)
}

// -------------------------------------------------------------------------
// Case 5 (H-SetOnce): HIDDEN, opaque, strategy=SetOnce.
// Sibling changes; secret unchanged. Secret NOT in Schema.Fields/WriteOnly().
// Q: same as H-Update -- confirm the /SecretString op is stripped.
// -------------------------------------------------------------------------

func TestCharacterize_Sibling_HiddenSetOnce_SecretUnchanged(t *testing.T) {
	runSiblingScenario(t, "H-SetOnce",
		secretSchemaHidden(),
		wrappedOpaqueSetOnce("super-secret"),
		wrappedOpaqueSetOnce("super-secret"),
	)
}
