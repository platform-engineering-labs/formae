// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resolver

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A reference envelope may sit under a literal map key carrying path syntax —
// a Kubernetes annotation, say. TargetPath is that key's path, and it serves as
// the ref's identity, its read path, its write path and the subject of
// isTargetPath, so it has to name the literal key at every one of them.

const dottedAnnotation = "objectset.rio.cattle.io/applied"

func TestTargetPathNamesTheLiteralDottedKey(t *testing.T) {
	props := json.RawMessage(`{"metadata":{"annotations":{"` + dottedAnnotation +
		`":{"$ref":"formae://CL#/Arn","$value":"stale"}}}}`)

	refs := ExtractResolvableRefs(pkgmodel.Resource{Properties: props})
	require.Len(t, refs, 1)

	assert.Equal(t, `metadata.annotations.objectset\.rio\.cattle\.io\/applied`, refs[0].TargetPath,
		"the path must address the literal key, not a nested tree")
	assert.True(t, gjson.GetBytes(props, refs[0].TargetPath).Exists(),
		"TargetPath must read back the envelope it was built from")
}

// The resolved value must land on the literal key, leaving no exploded sibling
// tree beside it.
func TestResolveWritesToTheLiteralDottedKey(t *testing.T) {
	props := json.RawMessage(`{"metadata":{"annotations":{"` + dottedAnnotation +
		`":{"$ref":"formae://CL#/Arn","$value":"stale"}}}}`)

	resolved, err := ResolvePropertyReferences("formae://CL#/Arn", props, "fresh-value")
	require.NoError(t, err)

	annotations := gjson.GetBytes(resolved, "metadata.annotations")
	require.True(t, annotations.IsObject(), "annotations must stay an object, got %s", resolved)
	require.Len(t, annotations.Map(), 1, "no exploded sibling may appear beside the literal key, got %s", resolved)
	require.Contains(t, annotations.Map(), dottedAnnotation, "the literal key must survive, got %s", resolved)

	assert.Equal(t, "fresh-value",
		gjson.GetBytes(resolved, `metadata.annotations.objectset\.rio\.cattle\.io\/applied.$value`).String(),
		"the resolved value must be written at the literal key, got %s", resolved)
}

// The same, for a reference under a dotted key inside an array element.
func TestResolveWritesToTheLiteralDottedKeyInsideArray(t *testing.T) {
	props := json.RawMessage(`{"items":[{"` + dottedAnnotation +
		`":{"$ref":"formae://CL#/Arn","$value":"stale"}}]}`)

	refs := ExtractResolvableRefs(pkgmodel.Resource{Properties: props})
	require.Len(t, refs, 1)
	assert.Equal(t, `items.0.objectset\.rio\.cattle\.io\/applied`, refs[0].TargetPath)

	resolved, err := ResolvePropertyReferences("formae://CL#/Arn", props, "fresh-value")
	require.NoError(t, err)

	element := gjson.GetBytes(resolved, "items.0")
	require.True(t, element.IsObject(), "the array element must stay an object, got %s", resolved)
	require.Len(t, element.Map(), 1, "no exploded sibling may appear beside the literal key, got %s", resolved)
	assert.Equal(t, "fresh-value",
		gjson.GetBytes(resolved, `items.0.objectset\.rio\.cattle\.io\/applied.$value`).String(),
		"the resolved value must be written at the literal key, got %s", resolved)
}

// ConvertToPluginFormat drives isTargetPath: a $value sitting under a dotted key
// that is ALSO a ref target must not be written twice, and a plain $value under
// a dotted key must be flattened to the literal key.
func TestConvertToPluginFormatFlattensDottedKeys(t *testing.T) {
	props := json.RawMessage(`{"metadata":{"annotations":{` +
		`"` + dottedAnnotation + `":{"$ref":"formae://CL#/Arn","$value":"resolved"},` +
		`"app.kubernetes.io/name":{"$value":"plain"}}}}`)

	converted, err := ConvertToPluginFormat(props)
	require.NoError(t, err)

	annotations := gjson.GetBytes(converted, "metadata.annotations")
	require.True(t, annotations.IsObject(), "annotations must stay an object, got %s", converted)
	require.Len(t, annotations.Map(), 2,
		"both literal keys and nothing else, got %s", converted)

	assert.Equal(t, "resolved", annotations.Map()[dottedAnnotation].String(),
		"the ref's value must be flattened onto its literal key, got %s", converted)
	assert.Equal(t, "plain", annotations.Map()["app.kubernetes.io/name"].String(),
		"the plain value must be flattened onto its literal key, got %s", converted)
}

// Re-parsing the properties a walk produced must yield the same identities the
// first walk did, so a ref found in one walk is still found in the next.
func TestTargetPathIdentitiesSurviveAReparse(t *testing.T) {
	props := json.RawMessage(`{"metadata":{"annotations":{"` + dottedAnnotation +
		`":{"$ref":"formae://CL#/Arn","$value":"stale"}}}}`)

	first := ExtractResolvableRefs(pkgmodel.Resource{Properties: props})
	require.Len(t, first, 1)

	resolved, err := ResolvePropertyReferences("formae://CL#/Arn", props, "fresh-value")
	require.NoError(t, err)

	second := ExtractResolvableRefs(pkgmodel.Resource{Properties: resolved})
	require.Len(t, second, 1, "the ref must still be found after a resolve round-trip, got %s", resolved)
	assert.Equal(t, first[0].TargetPath, second[0].TargetPath,
		"the identity must be stable across walks")
}

// Plain keys keep byte-identical paths, so every existing resolution is
// unaffected by escaping at construction.
func TestTargetPathUnchangedForPlainKeys(t *testing.T) {
	props := json.RawMessage(`{
		"Cluster": {"$ref": "formae://CL#/Arn", "$value": "arn-cluster"},
		"LoadBalancers": [
			{"TargetGroupArn": {"$ref": "formae://LN#/DefaultActions.0.TargetGroupArn", "$value": "arn-tg"}}
		]
	}`)

	got := map[string]bool{}
	for _, ref := range ExtractResolvableRefs(pkgmodel.Resource{Properties: props}) {
		got[ref.TargetPath] = true
	}

	assert.True(t, got["Cluster"], "got %v", got)
	assert.True(t, got["LoadBalancers.0.TargetGroupArn"], "got %v", got)
}
