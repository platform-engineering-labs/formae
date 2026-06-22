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

	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

// ----------------------------------------------------------------------------
// opaque + writeOnly secret values UNDER SYNC.
//
// A companion file (opaque_writeonly_hidden_characterization_test.go) runs with
// the Synchronizer disabled and sees no churn on unchanged re-apply. This file
// drives a real sync cycle (m.ForceSync()) between create and re-apply, where
// the provider's Read deliberately OMITS the writeOnly secret field (realistic:
// a cloud never returns secret material on Read).
//
// What these tests check: whether the Read-based sync overwrites stored
// Properties with the Read result that lacks the secret, dropping the stored
// value/hash, so the next apply sees a difference and re-adds (churns) the
// secret, possibly re-sending the at-rest hash as the value.
//
// They use the real Synchronizer actor. Sync is configured Enabled:false in the
// test harness, but the Synchronizer process is always spawned and its
// Synchronize message handler is active regardless of Enabled (only the periodic
// self-tick is gated). m.ForceSync() sends Synchronize{} to it, which runs
// synchronizeAllResources -> a CommandSync forma command -> the same
// ChangesetExecutor/ResourceUpdater/PluginOperator path a periodic sync uses.
// This is the faithful in-process sync.
// ----------------------------------------------------------------------------

// loadStoredProps returns the persisted RESOURCE Properties (the secret-safe,
// at-rest form: opaque $value is a SHA-256 hash) for the single managed secret.
func loadStoredProps(t *testing.T, m interface {
	LoadAllResources() ([]*pkgmodel.Resource, error)
}, scenario string) string {
	t.Helper()
	resources, err := m.LoadAllResources()
	require.NoError(t, err)
	for _, r := range resources {
		if r.Type == "FakeAWS::SecretsManager::Secret" {
			t.Logf("[%s] STORED RESOURCE Properties = %s", scenario, string(r.Properties))
			t.Logf("[%s] STORED RESOURCE ReadOnlyProperties = %s", scenario, string(r.ReadOnlyProperties))
			return string(r.Properties)
		}
	}
	t.Logf("[%s] STORED RESOURCE: <none found>", scenario)
	return ""
}

// driveSync triggers one real sync cycle and waits for the provider Read to be
// observed (readCount to advance), mirroring the established synchronizer_test.go
// pattern (ForceSync + wait for read + inspect stored state). The sync runs the
// real Synchronizer actor -> CommandSync -> ChangesetExecutor/ResourceUpdater ->
// PluginOperator Read path.
func driveSync(t *testing.T, m *metastructure.Metastructure, readCount *int, mu *sync.Mutex) {
	t.Helper()
	mu.Lock()
	before := *readCount
	mu.Unlock()
	require.NoError(t, m.ForceSync())
	advanced := false
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		now := *readCount
		mu.Unlock()
		if now > before {
			advanced = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !advanced {
		t.Logf("[driveSync] WARNING: provider Read was not observed during sync (before=%d)", before)
	} else {
		t.Logf("[driveSync] sync Read observed (readCount advanced from %d)", before)
	}
	// Settle so the resource persister has flushed any sync-driven write.
	time.Sleep(1 * time.Second)
}

// surfacedSecretSchema: SecretString IS a field AND IS writeOnly (the surfaced,
// non-hidden shape).
func surfacedSecretSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Description", "SecretString"},
		Hints: map[string]pkgmodel.FieldHint{
			"SecretString": {WriteOnly: true},
		},
	}
}

func secretFormaWithSchema(label, nativeID, props string, schema pkgmodel.Schema) *pkgmodel.Forma {
	r := pkgmodel.Resource{
		Label:      label,
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

// -------------------------------------------------------------------------
// Config H: HIDDEN opaque writeOnly (value in Properties, NOT in Schema.Fields).
// -------------------------------------------------------------------------

func TestCharacterize_Sync_ConfigH_Hidden(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var mu sync.Mutex
		var creates int
		var readCount int
		var updates []capturedUpdate

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				mu.Lock()
				creates++
				mu.Unlock()
				t.Logf("[H] PLUGIN CREATE Properties = %s", string(req.Properties))
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
				t.Logf("[H] PLUGIN UPDATE PatchDocument = %s", pd)
				t.Logf("[H] PLUGIN UPDATE DesiredProps  = %s", string(req.DesiredProperties))
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
				mu.Lock()
				readCount++
				mu.Unlock()
				// Realistic provider Read: surfaced fields only, secret omitted.
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   `{"Name":"my-secret"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		schema := secretSchemaAsymmetric()
		props := `{"Name":"my-secret","SecretString":` + wrappedOpaque("super-secret") + `}`

		// (1) create
		_, err = m.ApplyForma(secretFormaWithSchema("the-secret", "", props, schema),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)
		waitForSecretCommands(t, m.Datastore, 1)
		t.Log("[H] === after create ===")
		afterCreate := loadStoredProps(t, m.Datastore, "H-after-create")

		// (2) one sync cycle (Read omits the secret)
		driveSync(t, m, &readCount, &mu)
		t.Log("[H] === after sync ===")
		afterSync := loadStoredProps(t, m.Datastore, "H-after-sync")
		if afterCreate == afterSync {
			t.Logf("[H] SYNC SURVIVAL: stored Properties UNCHANGED by sync (value/hash retained)")
		} else {
			t.Logf("[H] SYNC SURVIVAL: stored Properties CHANGED by sync (value possibly lost!)")
		}

		// (3) re-apply same value V
		mu.Lock()
		updatesBefore := len(updates)
		mu.Unlock()
		resp, err := m.ApplyForma(secretFormaWithSchema("the-secret", "secret-native-id", props, schema),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)
		t.Logf("[H] RE-APPLY ChangesRequired=%v CommandID=%s", resp.Simulation.ChangesRequired, resp.CommandID)

		time.Sleep(1 * time.Second)
		mu.Lock()
		newUpdates := updates[updatesBefore:]
		t.Logf("[H] RE-APPLY plugin Update ops seen = %d (0 = no churn, >0 = churn)", len(newUpdates))
		for i, u := range newUpdates {
			pd := "<nil>"
			if u.patchDocument != nil {
				pd = *u.patchDocument
			}
			t.Logf("[H] RE-APPLY update[%d] patch=%s desired=%s", i, pd, string(u.desiredProps))
		}
		mu.Unlock()
		cmds, _ := m.Datastore.LoadFormaCommands()
		applyCmds := 0
		for _, c := range cmds {
			if c.Command == pkgmodel.CommandApply {
				applyCmds++
			}
		}
		t.Logf("[H] total CommandApply forma commands = %d (1 = create only/no churn, 2 = re-apply churned)", applyCmds)
	})
}

// -------------------------------------------------------------------------
// Config S: SURFACED opaque writeOnly (value in Properties AND Schema.Fields +
// WriteOnly hint). This is the likely-recommended shape.
// -------------------------------------------------------------------------

func TestCharacterize_Sync_ConfigS_Surfaced(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var mu sync.Mutex
		var readCount int
		var updates []capturedUpdate

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				t.Logf("[S] PLUGIN CREATE Properties = %s", string(req.Properties))
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
				t.Logf("[S] PLUGIN UPDATE PatchDocument = %s", pd)
				t.Logf("[S] PLUGIN UPDATE DesiredProps  = %s", string(req.DesiredProperties))
				t.Logf("[S] PLUGIN UPDATE PriorProps    = %s", string(req.PriorProperties))
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
				mu.Lock()
				readCount++
				mu.Unlock()
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   `{"Name":"my-secret"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		schema := surfacedSecretSchema()
		props := `{"Name":"my-secret","SecretString":` + wrappedOpaque("super-secret") + `}`

		// (1) create
		_, err = m.ApplyForma(secretFormaWithSchema("the-secret", "", props, schema),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)
		waitForSecretCommands(t, m.Datastore, 1)
		t.Log("[S] === after create ===")
		afterCreate := loadStoredProps(t, m.Datastore, "S-after-create")

		// (2) one sync cycle (Read omits the secret)
		driveSync(t, m, &readCount, &mu)
		t.Log("[S] === after sync ===")
		afterSync := loadStoredProps(t, m.Datastore, "S-after-sync")
		if afterCreate == afterSync {
			t.Logf("[S] SYNC SURVIVAL: stored Properties UNCHANGED by sync (value/hash retained)")
		} else {
			t.Logf("[S] SYNC SURVIVAL: stored Properties CHANGED by sync (value possibly lost!)")
		}

		// (3) re-apply same value V
		mu.Lock()
		updatesBefore := len(updates)
		mu.Unlock()
		resp, err := m.ApplyForma(secretFormaWithSchema("the-secret", "secret-native-id", props, schema),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)
		t.Logf("[S] RE-APPLY ChangesRequired=%v CommandID=%s", resp.Simulation.ChangesRequired, resp.CommandID)

		time.Sleep(1 * time.Second)
		mu.Lock()
		newUpdates := updates[updatesBefore:]
		t.Logf("[S] RE-APPLY plugin Update ops seen = %d (0 = no churn, >0 = churn)", len(newUpdates))
		for i, u := range newUpdates {
			pd := "<nil>"
			if u.patchDocument != nil {
				pd = *u.patchDocument
			}
			t.Logf("[S] RE-APPLY update[%d] patch=%s desired=%s", i, pd, string(u.desiredProps))
		}
		mu.Unlock()
		cmds, _ := m.Datastore.LoadFormaCommands()
		applyCmds := 0
		for _, c := range cmds {
			if c.Command == pkgmodel.CommandApply {
				applyCmds++
			}
		}
		t.Logf("[S] total CommandApply forma commands = %d (1 = create only/no churn, 2 = re-apply churned)", applyCmds)

		// (4) rotate to V2 post-sync
		mu.Lock()
		rotateBefore := len(updates)
		mu.Unlock()
		props2 := `{"Name":"my-secret","SecretString":` + wrappedOpaque("rotated-secret-V2") + `}`
		resp2, err := m.ApplyForma(secretFormaWithSchema("the-secret", "secret-native-id", props2, schema),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client")
		require.NoError(t, err)
		t.Logf("[S] ROTATE-POST-SYNC ChangesRequired=%v CommandID=%s", resp2.Simulation.ChangesRequired, resp2.CommandID)
		time.Sleep(1 * time.Second)
		mu.Lock()
		rotateUpdates := updates[rotateBefore:]
		t.Logf("[S] ROTATE plugin Update ops seen = %d", len(rotateUpdates))
		for i, u := range rotateUpdates {
			pd := "<nil>"
			if u.patchDocument != nil {
				pd = *u.patchDocument
			}
			t.Logf("[S] ROTATE update[%d] patch=%s desired=%s", i, pd, string(u.desiredProps))
		}
		mu.Unlock()
	})
}
