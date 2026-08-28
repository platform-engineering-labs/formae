// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/constants"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Two rows in the same stack can share (label, type) while being distinct
// resources (distinct ksuids, distinct native ids) — discovery labeling does
// not guarantee label uniqueness. A sync must emit exactly one read update per
// resource row: emitting more than one produces duplicate ksuid:operation keys
// downstream, where the changeset DAG keeps only one node per operation URI and
// the command's remaining resource updates can never reach a terminal state.
func TestGenerateResourceUpdatesForSync_DuplicateLabelsEmitOneUpdatePerResource(t *testing.T) {
	ds, _ := GetDeps(t)

	target := &pkgmodel.Target{
		Label:     "test-target",
		Namespace: "Test",
		Config:    json.RawMessage(`{}`),
	}

	rowA := pkgmodel.Resource{
		Ksuid:      "ksuidAAAAAAAAAAAAAAAAAAAAAA",
		Label:      "shared-label",
		Type:       "Test::Generic::Resource",
		Stack:      constants.UnmanagedStack,
		Target:     "test-target",
		NativeID:   "native-a",
		Properties: json.RawMessage(`{"Name":"shared-label"}`),
	}
	rowB := rowA
	rowB.Ksuid = "ksuidBBBBBBBBBBBBBBBBBBBBBB"
	rowB.NativeID = "native-b"

	_, err := ds.StoreStack(&pkgmodel.Forma{Resources: []pkgmodel.Resource{rowA, rowB}}, "test-command")
	require.NoError(t, err)

	forma := pkgmodel.FormaFromResources([]*pkgmodel.Resource{&rowA, &rowB})

	updates, err := GenerateResourceUpdates(
		forma,
		pkgmodel.CommandSync,
		pkgmodel.FormaApplyModePatch,
		FormaCommandSourceSynchronize,
		[]*pkgmodel.Target{target},
		ds,
		nil, nil, false)
	require.NoError(t, err)

	seen := make(map[string]bool, len(updates))
	for _, u := range updates {
		key := u.DesiredState.Ksuid + ":" + string(u.Operation)
		assert.False(t, seen[key], "duplicate resource update generated for %s (label %s)", key, u.DesiredState.Label)
		seen[key] = true
	}
	assert.Len(t, updates, 2)
}
