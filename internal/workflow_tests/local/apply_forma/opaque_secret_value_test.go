// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

// Regression suite for opaque + writeOnly secret values (AWS SecretsManager
// SecretString, Azure KeyVault Secret value). Distilled from the characterization
// tests Jeroen Soeters authored on branch opaque-writeonly-characterization-tests;
// those mapped the behavior matrix, these pin the fixed behavior.
//
// The value field is now SURFACED (not hidden), so it appears in Schema.WriteOnly()
// and the suppression logic applies. End to end, through the metastructure:
//   - rotate:   a changed value reaches the plugin as cleartext
//   - no churn: an unchanged value yields no /SecretString op on a sibling edit
//   - setOnce:  a frozen value is never re-sent, and never as its stored hash (no corruption)
//   - create:   the first value reaches the plugin as cleartext

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

var sha256Re = regexp.MustCompile(`[0-9a-f]{64}`)

// opaque builds the wrapped value envelope a user writes as formae.value(v).opaque[.setOnce].
func opaque(v, strategy string) string {
	return `{"$value":"` + v + `","$visibility":"Opaque","$strategy":"` + strategy + `"}`
}

// surfacedSecretSchema is the post-fix shape: SecretString is a surfaced, writeOnly field.
func surfacedSecretSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Description", "SecretString"},
		Hints:      map[string]pkgmodel.FieldHint{"SecretString": {WriteOnly: true}},
	}
}

func secretForma(nativeID, props string) *pkgmodel.Forma {
	r := pkgmodel.Resource{
		Label:      "the-secret",
		Type:       "FakeAWS::SecretsManager::Secret",
		Stack:      "secrets-stack",
		Target:     "secrets-target",
		Schema:     surfacedSecretSchema(),
		Properties: json.RawMessage(props),
		Managed:    true,
	}
	if nativeID != "" {
		r.NativeID = nativeID
	}
	return &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "secrets-stack"}},
		Resources: []pkgmodel.Resource{r},
		Targets:   []pkgmodel.Target{{Label: "secrets-target", Namespace: "secrets-namespace", Config: json.RawMessage(`{}`)}},
	}
}

// secretRun is the evidence captured from the plugin: the Properties Create saw,
// and the PatchDocument of every Update.
type secretRun struct {
	createProps   string
	updatePatches []string
}

// applySecret creates with createProps, then (when reapplyProps != "") re-applies it.
// The provider Read returns Name + readDesc (never the writeOnly secret), so the
// only diff the re-apply sees is whatever createProps and reapplyProps differ on.
func applySecret(t *testing.T, createProps, reapplyProps, readDesc string) secretRun {
	t.Helper()
	var run secretRun
	var mu sync.Mutex

	overrides := &plugin.ResourcePluginOverrides{
		Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
			mu.Lock()
			run.createProps = string(req.Properties)
			mu.Unlock()
			return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
				Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess,
				NativeID: "secret-native-id", ResourceProperties: json.RawMessage(`{"Name":"my-secret"}`),
			}}, nil
		},
		Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
			mu.Lock()
			if req.PatchDocument != nil {
				run.updatePatches = append(run.updatePatches, *req.PatchDocument)
			}
			mu.Unlock()
			return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
				Operation: resource.OperationUpdate, OperationStatus: resource.OperationStatusSuccess,
				NativeID: req.NativeID, ResourceProperties: json.RawMessage(`{"Name":"my-secret"}`),
			}}, nil
		},
		Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
			return &resource.ReadResult{ResourceType: req.ResourceType,
				Properties: `{"Name":"my-secret","Description":"` + readDesc + `"}`}, nil
		},
	}

	m, done, err := test_helpers.NewTestMetastructure(t, overrides)
	require.NoError(t, err)
	defer done()

	reconcile := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}
	_, err = m.ApplyForma(secretForma("", createProps), reconcile, "client")
	require.NoError(t, err)
	waitForCommands(t, m, 1)

	if reapplyProps != "" {
		_, err = m.ApplyForma(secretForma("secret-native-id", reapplyProps), reconcile, "client")
		require.NoError(t, err)
		waitForCommands(t, m, 2)
		time.Sleep(500 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	return run
}

func props(desc, secret string) string {
	return `{"Name":"my-secret","Description":"` + desc + `","SecretString":` + secret + `}`
}

func joinPatches(r secretRun) string {
	out := ""
	for _, p := range r.updatePatches {
		out += p
	}
	return out
}

// Create sends the first value to the plugin as cleartext (never a hash).
func TestOpaqueSecret_Create_SendsCleartext(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		r := applySecret(t, props("A", opaque("value-V", "Update")), "", "A")
		require.Contains(t, r.createProps, "value-V", "Create must receive the cleartext value")
		require.NotRegexp(t, sha256Re, r.createProps, "Create must not receive a hash")
	})
}

// A changed value rotates: the plugin gets a /SecretString op carrying the new cleartext.
func TestOpaqueSecret_Update_Rotates(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		r := applySecret(t, props("A", opaque("value-V", "Update")), props("A", opaque("value-V2", "Update")), "A")
		patch := joinPatches(r)
		require.Contains(t, patch, "/SecretString", "a real change must reach the plugin")
		require.Contains(t, patch, "value-V2", "the new value must be sent as cleartext")
		require.NotRegexp(t, sha256Re, patch, "the patch must never carry a hash")
	})
}

// An unchanged value does not churn: a sibling edit produces no /SecretString op.
func TestOpaqueSecret_Update_NoChurnOnSiblingEdit(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		r := applySecret(t, props("A", opaque("value-V", "Update")), props("B", opaque("value-V", "Update")), "A")
		patch := joinPatches(r)
		require.Contains(t, patch, "/Description", "the sibling change must reach the plugin")
		require.NotContains(t, patch, "/SecretString", "an unchanged value must not be re-sent")
		require.NotRegexp(t, sha256Re, patch, "no hash anywhere")
	})
}

// setOnce freezes the value and never corrupts it: a sibling edit (with a changed
// value in the forma) sends the sibling op but no /SecretString, and no hash.
func TestOpaqueSecret_SetOnce_FrozenNoCorruption(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		r := applySecret(t, props("A", opaque("value-V", "SetOnce")), props("B", opaque("value-V2", "SetOnce")), "A")
		patch := joinPatches(r)
		require.Contains(t, patch, "/Description", "the sibling change must reach the plugin")
		require.NotContains(t, patch, "/SecretString", "a setOnce value must never be re-sent")
		require.NotRegexp(t, sha256Re, patch, "the value must never be sent as its stored hash")
	})
}
