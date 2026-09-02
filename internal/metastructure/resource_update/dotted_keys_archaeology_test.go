// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Before the merge paths escaped their keys, a stored dotted key missed its
// plugin-side lookup and was preserved at the dotted PATH, materializing an
// exploded tree beside the literal key the plugin read already carried. Rows
// synced by such a build carry both to this day.
//
// These pins record that they do not heal on their own, which is the premise of
// the repair: the exploded keys are ordinary stored keys the plugin never
// corroborates, and the merger preserves exactly those by design. Nothing here
// asserts a defect — it asserts that a corrupted row stays corrupted, so if that
// ever stops being true the repair's justification has changed.

// corruptedAnnotations is the reported shape: the literal key, plus the
// dot-exploded duplicate carrying the same value. The leaf keeps its slash
// because sjson splits on dots and not on slashes.
const corruptedAnnotations = `{"metadata":{"annotations":{` +
	`"objectset.rio.cattle.io/applied":"v",` +
	`"objectset":{"rio":{"cattle":{"io/applied":"v"}}}` +
	`}}}`

// cleanAnnotations is what the plugin reads: the literal key alone.
const cleanAnnotations = `{"metadata":{"annotations":{"objectset.rio.cattle.io/applied":"v"}}}`

func TestArchaeology_CorruptedRowSurvivesACleanRead(t *testing.T) {
	merged, err := mergeRefsPreservingUserRefs(
		json.RawMessage(corruptedAnnotations), json.RawMessage(cleanAnnotations),
		pkgmodel.Schema{}, false, nil)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(merged, &out))
	annotations := out["metadata"].(map[string]any)["annotations"].(map[string]any)

	assert.Equal(t, "v", annotations["objectset.rio.cattle.io/applied"],
		"the literal key survives, got %s", merged)
	require.Contains(t, annotations, "objectset",
		"the exploded duplicate also survives: the plugin never corroborates it, "+
			"and user-side keys the plugin omits are preserved by design (got %s)", merged)

	exploded := annotations["objectset"].(map[string]any)["rio"].(map[string]any)["cattle"].(map[string]any)
	assert.Equal(t, "v", exploded["io/applied"], "got %s", merged)
}

// The historical merger walked objects inside arrays too, so array-contained
// corruption was minted as well. Whether it PERSISTS depends on how the array's
// elements are matched to the plugin's, and both answers are real.
//
// Matched by key, the corrupted element merges against its clean counterpart
// key by key, and the exploded duplicate is preserved exactly as at the top
// level. This is the durable case the repair has to cover.
func TestArchaeology_ArrayCorruptionSurvivesWhenElementsMatchByKey(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"items"},
		Hints: map[string]pkgmodel.FieldHint{
			"items": {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		},
	}
	corrupted := `{"items":[{"Key":"k",` +
		`"objectset.rio.cattle.io/applied":"v",` +
		`"objectset":{"rio":{"cattle":{"io/applied":"v"}}}` +
		`}]}`
	clean := `{"items":[{"Key":"k","objectset.rio.cattle.io/applied":"v"}]}`

	merged, err := mergeRefsPreservingUserRefs(
		json.RawMessage(corrupted), json.RawMessage(clean), schema, false, nil)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(merged, &out))
	element := out["items"].([]any)[0].(map[string]any)

	assert.Equal(t, "v", element["objectset.rio.cattle.io/applied"],
		"the literal key survives, got %s", merged)
	require.Contains(t, element, "objectset",
		"the exploded duplicate inside the array element also survives, got %s", merged)
}

// Matched structurally, the extra exploded keys stop the corrupted element
// matching its clean counterpart at all, so the plugin's element is taken whole
// and the corruption goes with the element it sat in. Recorded because it means
// array-contained corruption is not uniformly durable: under a structural match
// the row heals itself, and the repair simply finds nothing to do.
func TestArchaeology_ArrayCorruptionIsDiscardedWhenElementsMatchStructurally(t *testing.T) {
	corrupted := `{"items":[{` +
		`"objectset.rio.cattle.io/applied":"v",` +
		`"objectset":{"rio":{"cattle":{"io/applied":"v"}}}` +
		`}]}`
	clean := `{"items":[{"objectset.rio.cattle.io/applied":"v"}]}`

	merged, err := mergeRefsPreservingUserRefs(
		json.RawMessage(corrupted), json.RawMessage(clean), pkgmodel.Schema{}, false, nil)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(merged, &out))
	element := out["items"].([]any)[0].(map[string]any)

	assert.Equal(t, "v", element["objectset.rio.cattle.io/applied"])
	assert.NotContains(t, element, "objectset",
		"the unmatched corrupted element is replaced by the plugin's, got %s", merged)
}

// And the writer itself is closed: a clean stored row merged against a clean
// plugin read does not acquire an exploded sibling, so re-ingest after the
// repair cannot re-corrupt.
func TestArchaeology_CleanRowDoesNotAcquireAnExplodedSibling(t *testing.T) {
	merged, err := mergeRefsPreservingUserRefs(
		json.RawMessage(cleanAnnotations), json.RawMessage(cleanAnnotations),
		pkgmodel.Schema{}, false, nil)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(merged, &out))
	annotations := out["metadata"].(map[string]any)["annotations"].(map[string]any)

	require.Len(t, annotations, 1, "no exploded sibling may be minted, got %s", merged)
	assert.Equal(t, "v", annotations["objectset.rio.cattle.io/applied"])
}
