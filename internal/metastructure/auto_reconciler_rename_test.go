// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// RFC-0041 ForceReconcile-vs-rename: when the live row's label differs from
// the snapshot's label, the synthesized reconcile resource must carry the
// live label as `Label` and the snapshot label as `Alias`. Without this the
// reconcile generator pairs by stale label, treats the renamed inventory row
// as drift to delete, and silently reverts the rename.
func TestReconcileResourceFromSnapshot_RenamedSinceSnapshot_RecordsAlias(t *testing.T) {
	snapshot := datastore.ResourceSnapshot{
		KSUID:      "K_RENAME",
		Type:       "AWS::EC2::Instance",
		Label:      "web-server",
		Target:     "test-target",
		NativeID:   "i-0abc",
		Properties: json.RawMessage(`{"InstanceType":"t2.micro"}`),
	}
	existing := &pkgmodel.Resource{
		Ksuid: "K_RENAME",
		Type:  "AWS::EC2::Instance",
		Label: "app-server", // renamed since snapshot
	}

	got := reconcileResourceFromSnapshot(snapshot, existing, "prod")

	assert.Equal(t, "K_RENAME", got.Ksuid, "ksuid preserved when live row still exists")
	assert.Equal(t, "app-server", got.Label, "must use the live label, not the stale snapshot label")
	assert.Equal(t, "web-server", got.Alias, "must record snapshot label as alias so generator pairs by alias")
	assert.Equal(t, "prod", got.Stack)
	assert.Equal(t, "i-0abc", got.NativeID, "NativeID carries through from snapshot")
}

// No-rename case: live label matches snapshot label. Alias must stay empty —
// no Alias prevents a regression where every reconcile resource picks up a
// spurious alias and confuses downstream matching.
func TestReconcileResourceFromSnapshot_NoRename_NoAlias(t *testing.T) {
	snapshot := datastore.ResourceSnapshot{
		KSUID:      "K_SAME",
		Type:       "AWS::EC2::Instance",
		Label:      "web-server",
		Properties: json.RawMessage(`{"InstanceType":"t2.micro"}`),
	}
	existing := &pkgmodel.Resource{
		Ksuid: "K_SAME",
		Type:  "AWS::EC2::Instance",
		Label: "web-server",
	}

	got := reconcileResourceFromSnapshot(snapshot, existing, "prod")

	assert.Equal(t, "web-server", got.Label)
	assert.Equal(t, "", got.Alias, "no rename → no alias")
	assert.Equal(t, "K_SAME", got.Ksuid)
}

// Deleted-since-snapshot case: live row gone (existing == nil). KSUID must be
// cleared so assignKSUIDs() resolves a fresh one — reusing a tombstoned KSUID
// would corrupt inventory. Label falls back to the snapshot label; no Alias
// (there's nothing to rename to/from).
func TestReconcileResourceFromSnapshot_DeletedSinceSnapshot_ClearsKsuid(t *testing.T) {
	snapshot := datastore.ResourceSnapshot{
		KSUID: "K_GONE",
		Type:  "AWS::EC2::Instance",
		Label: "web-server",
	}

	got := reconcileResourceFromSnapshot(snapshot, nil, "prod")

	assert.Equal(t, "", got.Ksuid, "tombstoned KSUID must be cleared")
	assert.Equal(t, "web-server", got.Label)
	assert.Equal(t, "", got.Alias)
}

// Snapshot without a KSUID: pure pass-through, no live lookup attempted.
func TestReconcileResourceFromSnapshot_NoKsuid_PassthroughLabel(t *testing.T) {
	snapshot := datastore.ResourceSnapshot{
		Type:  "AWS::EC2::Instance",
		Label: "web-server",
	}

	got := reconcileResourceFromSnapshot(snapshot, nil, "prod")

	assert.Equal(t, "", got.Ksuid)
	assert.Equal(t, "web-server", got.Label)
	assert.Equal(t, "", got.Alias)
}
