// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

// ----------------------------------------------------------------------------
// opaque + writeOnly + hidden secret values.
//
// These tests reproduce the runtime shape of a field declared in PKL as BOTH
// `hidden` AND `@FieldHint{writeOnly=true}` (for example a SecretsManager
// `secretString` or a KeyVault `value`). At runtime such a field is:
//
//   - PRESENT in the resource Properties (the wrapped value envelope), but
//   - ABSENT from Schema.Fields and Schema.WriteOnly() (hidden filters it out).
//
// The asymmetry is reproduced programmatically by setting the resource's inline
// Schema with Fields that EXCLUDE the value field, while Properties INCLUDE the
// wrapped value envelope. resource_update_factory.go uses newResource.Schema to
// drive GeneratePatch, so the inline schema is the faithful lever.
//
// The tests observe behavior: they log the raw evidence (patch, plugin ops,
// stored props) and assert only the stable parts.
// ----------------------------------------------------------------------------

// secretSchemaAsymmetric returns the runtime schema as it would appear for a
// `hidden` + writeOnly secretString: the value field "SecretString" is NOT in
// Fields, and therefore NOT in WriteOnly() either. Only "Name"/"Description"
// (the surfaced, non-hidden fields) are present.
func secretSchemaAsymmetric() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Description"},
		Hints:      map[string]pkgmodel.FieldHint{
			// Note: SecretString deliberately absent from both Fields and Hints,
			// mirroring the `hidden` filter in formae.pkl hints().
		},
	}
}

// wrappedOpaque builds the opaque (Update strategy) value envelope.
func wrappedOpaque(v string) string {
	return `{"$value":"` + v + `","$visibility":"Opaque","$strategy":"Update"}`
}

// wrappedOpaqueSetOnce builds the opaque + SetOnce value envelope.
func wrappedOpaqueSetOnce(v string) string {
	return `{"$value":"` + v + `","$visibility":"Opaque","$strategy":"SetOnce"}`
}

// capturedUpdate records what the plugin's Update saw.
type capturedUpdate struct {
	patchDocument *string
	desiredProps  json.RawMessage
	priorProps    json.RawMessage
}

// secretForma builds a one-resource forma with the asymmetric schema and the
// given properties. nativeID is set when re-applying an existing resource.
func secretForma(label, nativeID, props string) *pkgmodel.Forma {
	r := pkgmodel.Resource{
		Label:      label,
		Type:       "FakeAWS::SecretsManager::Secret",
		Stack:      "secrets-stack",
		Target:     "secrets-target",
		Schema:     secretSchemaAsymmetric(),
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

func waitForSecretCommands(t *testing.T, m interface {
	LoadFormaCommands() ([]*forma_command.FormaCommand, error)
}, n int) []*forma_command.FormaCommand {
	t.Helper()
	var cmds []*forma_command.FormaCommand
	require.Eventually(t, func() bool {
		var err error
		cmds, err = m.LoadFormaCommands()
		if err != nil {
			return false
		}
		if len(cmds) != n {
			return false
		}
		return allCommandsSuccessful(cmds)
	}, 5*time.Second, 100*time.Millisecond)
	return cmds
}

// dumpStoredProps fetches the latest forma command's first resource update
// DesiredState.Properties and logs them.
func dumpStoredProps(t *testing.T, cmd *forma_command.FormaCommand, scenario string) string {
	t.Helper()
	if len(cmd.ResourceUpdates) == 0 {
		t.Logf("[%s] STORED PROPS: <no resource updates>", scenario)
		return ""
	}
	props := string(cmd.ResourceUpdates[0].DesiredState.Properties)
	t.Logf("[%s] STORED DesiredState.Properties = %s", scenario, props)
	t.Logf("[%s] STORED DesiredState.Schema.Fields    = %v", scenario, cmd.ResourceUpdates[0].DesiredState.Schema.Fields)
	t.Logf("[%s] STORED DesiredState.Schema.WriteOnly()= %v", scenario, cmd.ResourceUpdates[0].DesiredState.Schema.WriteOnly())
	t.Logf("[%s] STORED DesiredState.PatchDocument     = %s", scenario, string(cmd.ResourceUpdates[0].DesiredState.PatchDocument))
	return props
}

// -------------------------------------------------------------------------
// Scenario 4 (run first, cheapest): runtime schema asymmetry proof.
// -------------------------------------------------------------------------

func TestCharacterize_RuntimeSchemaAsymmetry(t *testing.T) {
	s := secretSchemaAsymmetric()
	t.Logf("Schema.Fields        = %v", s.Fields)
	t.Logf("Schema.WriteOnly()   = %v", s.WriteOnly())
	t.Logf("Schema.CreateOnly()  = %v", s.CreateOnly())

	require.NotContains(t, s.Fields, "SecretString", "value field must be ABSENT from Schema.Fields")
	require.NotContains(t, s.WriteOnly(), "SecretString", "value field must be ABSENT from Schema.WriteOnly()")
}

// -------------------------------------------------------------------------
// Scenario 1: opaque (Update) — create then UNCHANGED re-apply (churn check).
// -------------------------------------------------------------------------

func TestCharacterize_OpaqueUpdate_UnchangedReapply(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var mu sync.Mutex
		var creates int
		var updates []capturedUpdate

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				mu.Lock()
				creates++
				mu.Unlock()
				t.Logf("[s1] PLUGIN CREATE received Properties = %s", string(req.Properties))
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           "secret-native-id",
						ResourceProperties: json.RawMessage(`{"Name":"my-secret"}`),
					},
				}, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				mu.Lock()
				updates = append(updates, capturedUpdate{req.PatchDocument, req.DesiredProperties, req.PriorProperties})
				mu.Unlock()
				pd := "<nil>"
				if req.PatchDocument != nil {
					pd = *req.PatchDocument
				}
				t.Logf("[s1] PLUGIN UPDATE received PatchDocument = %s", pd)
				t.Logf("[s1] PLUGIN UPDATE received DesiredProps  = %s", string(req.DesiredProperties))
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           req.NativeID,
						ResourceProperties: json.RawMessage(`{"Name":"my-secret"}`),
					},
				}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				// WriteOnly value never returned by provider Read; only the
				// surfaced field comes back. This is what the real provider does.
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   `{"Name":"my-secret"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		props := `{"Name":"my-secret","SecretString":` + wrappedOpaque("super-secret") + `}`

		// First apply: create.
		_, err = m.ApplyForma(secretForma("the-secret", "", props),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)
		cmds := waitForSecretCommands(t, m.Datastore, 1)
		dumpStoredProps(t, cmds[0], "s1-after-create")

		// Second apply: SAME value, same opaque envelope.
		_, err = m.ApplyForma(secretForma("the-secret", "secret-native-id", props),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)

		// Give the system time to (not) process an update.
		time.Sleep(1 * time.Second)

		mu.Lock()
		defer mu.Unlock()
		t.Logf("[s1] creates=%d updates=%d", creates, len(updates))
		for i, u := range updates {
			pd := "<nil>"
			if u.patchDocument != nil {
				pd = *u.patchDocument
			}
			t.Logf("[s1] update[%d] patch=%s desired=%s", i, pd, string(u.desiredProps))
		}
		cmdsAfter, _ := m.Datastore.LoadFormaCommands()
		t.Logf("[s1] total forma commands stored = %d", len(cmdsAfter))
		t.Logf("[s1] Q1 ANSWER: did unchanged opaque re-apply churn? updates seen = %d (0 = no churn, >0 = churn)", len(updates))
	})
}

// -------------------------------------------------------------------------
// Scenario 2: opaque (Update) — ROTATION (value changes).
// -------------------------------------------------------------------------

func TestCharacterize_OpaqueUpdate_Rotation(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var mu sync.Mutex
		var updates []capturedUpdate

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				t.Logf("[s2] PLUGIN CREATE received Properties = %s", string(req.Properties))
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           "secret-native-id",
						ResourceProperties: json.RawMessage(`{"Name":"my-secret"}`),
					},
				}, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				mu.Lock()
				updates = append(updates, capturedUpdate{req.PatchDocument, req.DesiredProperties, req.PriorProperties})
				mu.Unlock()
				pd := "<nil>"
				if req.PatchDocument != nil {
					pd = *req.PatchDocument
				}
				t.Logf("[s2] PLUGIN UPDATE PatchDocument = %s", pd)
				t.Logf("[s2] PLUGIN UPDATE DesiredProps  = %s", string(req.DesiredProperties))
				t.Logf("[s2] PLUGIN UPDATE PriorProps    = %s", string(req.PriorProperties))
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           req.NativeID,
						ResourceProperties: json.RawMessage(`{"Name":"my-secret"}`),
					},
				}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   `{"Name":"my-secret"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		// Create with V.
		_, err = m.ApplyForma(
			secretForma("the-secret", "", `{"Name":"my-secret","SecretString":`+wrappedOpaque("value-V")+`}`),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)
		cmds := waitForSecretCommands(t, m.Datastore, 1)
		dumpStoredProps(t, cmds[0], "s2-after-create")

		// Rotate to V2.
		resp, err := m.ApplyForma(
			secretForma("the-secret", "secret-native-id", `{"Name":"my-secret","SecretString":`+wrappedOpaque("value-V2")+`}`),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)
		if resp != nil {
			t.Logf("[s2] rotation ApplyForma response: ChangesRequired=%v CommandID=%s",
				resp.Simulation.ChangesRequired, resp.CommandID)
		}

		time.Sleep(1 * time.Second)

		mu.Lock()
		defer mu.Unlock()
		t.Logf("[s2] updates seen by plugin = %d", len(updates))
		for i, u := range updates {
			pd := "<nil>"
			if u.patchDocument != nil {
				pd = *u.patchDocument
			}
			t.Logf("[s2] update[%d] patch=%s", i, pd)
		}
		cmdsAfter, _ := m.Datastore.LoadFormaCommands()
		for i, c := range cmdsAfter {
			if len(c.ResourceUpdates) > 0 {
				t.Logf("[s2] cmd[%d] op=%s storedPatch=%s storedProps=%s", i,
					c.ResourceUpdates[0].Operation,
					string(c.ResourceUpdates[0].DesiredState.PatchDocument),
					string(c.ResourceUpdates[0].DesiredState.Properties))
			}
		}
		t.Logf("[s2] Q2: did the rotation /SecretString op reach the plugin? inspect the patch above; if updates>0 but patch lacks /SecretString, it was STRIPPED")
	})
}

// -------------------------------------------------------------------------
// Scenario 3: opaque + setOnce — create then CHANGED re-apply (frozen?).
// -------------------------------------------------------------------------

func TestCharacterize_OpaqueSetOnce_ChangedReapply(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var mu sync.Mutex
		var updates []capturedUpdate

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				t.Logf("[s3] PLUGIN CREATE received Properties = %s", string(req.Properties))
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           "secret-native-id",
						ResourceProperties: json.RawMessage(`{"Name":"my-secret"}`),
					},
				}, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				mu.Lock()
				updates = append(updates, capturedUpdate{req.PatchDocument, req.DesiredProperties, req.PriorProperties})
				mu.Unlock()
				pd := "<nil>"
				if req.PatchDocument != nil {
					pd = *req.PatchDocument
				}
				t.Logf("[s3] PLUGIN UPDATE PatchDocument = %s", pd)
				t.Logf("[s3] PLUGIN UPDATE DesiredProps  = %s", string(req.DesiredProperties))
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           req.NativeID,
						ResourceProperties: json.RawMessage(`{"Name":"my-secret"}`),
					},
				}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   `{"Name":"my-secret"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		// Create with V (setOnce).
		_, err = m.ApplyForma(
			secretForma("the-secret", "", `{"Name":"my-secret","SecretString":`+wrappedOpaqueSetOnce("value-V")+`}`),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)
		cmds := waitForSecretCommands(t, m.Datastore, 1)
		dumpStoredProps(t, cmds[0], "s3-after-create")

		// Attempt to change to V2 — setOnce should freeze.
		_, err = m.ApplyForma(
			secretForma("the-secret", "secret-native-id", `{"Name":"my-secret","SecretString":`+wrappedOpaqueSetOnce("value-V2")+`}`),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)

		time.Sleep(1 * time.Second)

		mu.Lock()
		defer mu.Unlock()
		t.Logf("[s3] updates seen by plugin = %d (expect 0 if setOnce freezes the value)", len(updates))
		for i, u := range updates {
			pd := "<nil>"
			if u.patchDocument != nil {
				pd = *u.patchDocument
			}
			t.Logf("[s3] update[%d] patch=%s desired=%s", i, pd, string(u.desiredProps))
		}
		cmdsAfter, _ := m.Datastore.LoadFormaCommands()
		t.Logf("[s3] total forma commands stored = %d", len(cmdsAfter))
		for i, c := range cmdsAfter {
			if len(c.ResourceUpdates) > 0 {
				t.Logf("[s3] cmd[%d] op=%s storedProps=%s", i,
					c.ResourceUpdates[0].Operation,
					string(c.ResourceUpdates[0].DesiredState.Properties))
			}
		}
		t.Logf("[s3] Q3: is setOnce change frozen? (no plugin update + stored value unchanged = frozen)")
	})
}

// -------------------------------------------------------------------------
// CONTRAST: SURFACED (non-hidden) writeOnly value field. Here SecretString IS
// in Schema.Fields AND in Schema.WriteOnly(), which shows the behavioral
// difference for rotation versus the hidden shape above.
// -------------------------------------------------------------------------

func TestCharacterize_OpaqueUpdate_Rotation_SurfacedWriteOnly_Contrast(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var mu sync.Mutex
		var updates []capturedUpdate

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           "secret-native-id",
						ResourceProperties: json.RawMessage(`{"Name":"my-secret"}`),
					},
				}, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				mu.Lock()
				updates = append(updates, capturedUpdate{req.PatchDocument, req.DesiredProperties, req.PriorProperties})
				mu.Unlock()
				pd := "<nil>"
				if req.PatchDocument != nil {
					pd = *req.PatchDocument
				}
				t.Logf("[contrast] PLUGIN UPDATE PatchDocument = %s", pd)
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           req.NativeID,
						ResourceProperties: json.RawMessage(`{"Name":"my-secret"}`),
					},
				}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   `{"Name":"my-secret"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		// Surfaced writeOnly schema: SecretString IS a field and IS writeOnly.
		surfaced := pkgmodel.Schema{
			Identifier: "Name",
			Fields:     []string{"Name", "Description", "SecretString"},
			Hints: map[string]pkgmodel.FieldHint{
				"SecretString": {WriteOnly: true},
			},
		}
		buildForma := func(nativeID, props string) *pkgmodel.Forma {
			r := pkgmodel.Resource{
				Label:      "the-secret",
				Type:       "FakeAWS::SecretsManager::Secret",
				Stack:      "secrets-stack",
				Target:     "secrets-target",
				Schema:     surfaced,
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
					Label: "secrets-target", Namespace: "secrets-namespace", Config: json.RawMessage(`{}`),
				}},
			}
		}

		_, err = m.ApplyForma(
			buildForma("", `{"Name":"my-secret","SecretString":`+wrappedOpaque("value-V")+`}`),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)
		waitForSecretCommands(t, m.Datastore, 1)

		_, err = m.ApplyForma(
			buildForma("secret-native-id", `{"Name":"my-secret","SecretString":`+wrappedOpaque("value-V2")+`}`),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)

		time.Sleep(1 * time.Second)

		mu.Lock()
		defer mu.Unlock()
		t.Logf("[contrast] updates seen by plugin = %d", len(updates))
		for i, u := range updates {
			pd := "<nil>"
			if u.patchDocument != nil {
				pd = *u.patchDocument
			}
			t.Logf("[contrast] update[%d] patch=%s", i, pd)
		}
		t.Logf("[contrast] CONTRAST: with SURFACED writeOnly the /SecretString op should survive (it is in Schema.Fields)")
	})
}
