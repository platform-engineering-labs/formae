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
	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Three independent walkers turn declared envelopes into resolved ones, each
// building its own gjson/sjson paths out of literal map keys: ordinary $res
// translation, embedded-span translation, and $visibility stamping. A key
// carrying path syntax has to survive all three.

const dottedGeneratorKey = "objectset.rio.cattle.io/applied"

func seedResolvableTarget(t *testing.T) (ResourceDataLookup, pkgmodel.TripletKey, string) {
	t.Helper()
	ds, _ := GetDeps(t)
	triplet := pkgmodel.TripletKey{Stack: "default", Label: "kvs", Type: "AWS::CloudFront::KeyValueStore"}
	ksuid := "testdotted123"
	_, err := ds.StoreResource(&pkgmodel.Resource{
		Ksuid: ksuid, Label: triplet.Label, Type: triplet.Type, Stack: triplet.Stack,
	}, "cmd-1")
	require.NoError(t, err)
	return ds, triplet, ksuid
}

func resEnvelope(triplet pkgmodel.TripletKey) map[string]any {
	return map[string]any{
		"$res":      true,
		"$label":    triplet.Label,
		"$type":     triplet.Type,
		"$stack":    triplet.Stack,
		"$property": "id",
	}
}

// A whole-value $res under a literal dotted key must become a $ref AT that key.
// Unescaped, the write explodes the key before it ever becomes a reference.
func TestTranslatePropertiesJSON_ResUnderDottedKey(t *testing.T) {
	ds, triplet, _ := seedResolvableTarget(t)

	properties, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{dottedGeneratorKey: resEnvelope(triplet)},
		},
	})
	require.NoError(t, err)

	result, _, err := translatePropertiesJSON(properties, map[pkgmodel.TripletKey]string{}, map[pkgmodel.GeneratorKey]string{}, ds)
	require.NoError(t, err)

	annotations := gjson.GetBytes(result, "metadata.annotations")
	require.True(t, annotations.IsObject(), "annotations must stay an object, got %s", result)
	require.Len(t, annotations.Map(), 1, "no exploded sibling may appear beside the literal key, got %s", result)
	require.Contains(t, annotations.Map(), dottedGeneratorKey, "the literal key must survive, got %s", result)

	assert.NotEmpty(t, annotations.Map()[dottedGeneratorKey].Get("$ref").String(),
		"the envelope at the literal key must be translated to a $ref, got %s", result)
}

func TestTranslatePropertiesJSON_ResUnderDottedKeyInsideArray(t *testing.T) {
	ds, triplet, _ := seedResolvableTarget(t)

	properties, err := json.Marshal(map[string]any{
		"items": []any{map[string]any{dottedGeneratorKey: resEnvelope(triplet)}},
	})
	require.NoError(t, err)

	result, _, err := translatePropertiesJSON(properties, map[pkgmodel.TripletKey]string{}, map[pkgmodel.GeneratorKey]string{}, ds)
	require.NoError(t, err)

	element := gjson.GetBytes(result, "items.0")
	require.True(t, element.IsObject(), "the array element must stay an object, got %s", result)
	require.Len(t, element.Map(), 1, "no exploded sibling may appear beside the literal key, got %s", result)
	assert.NotEmpty(t, element.Map()[dottedGeneratorKey].Get("$ref").String(),
		"the envelope at the literal key must be translated to a $ref, got %s", result)
}

func TestTranslatePropertiesJSON_ResUnderDottedTopLevelKey(t *testing.T) {
	ds, triplet, _ := seedResolvableTarget(t)

	properties, err := json.Marshal(map[string]any{dottedGeneratorKey: resEnvelope(triplet)})
	require.NoError(t, err)

	result, _, err := translatePropertiesJSON(properties, map[pkgmodel.TripletKey]string{}, map[pkgmodel.GeneratorKey]string{}, ds)
	require.NoError(t, err)

	parsed := gjson.ParseBytes(result)
	require.Len(t, parsed.Map(), 1, "no exploded sibling may appear beside the literal key, got %s", result)
	assert.NotEmpty(t, parsed.Map()[dottedGeneratorKey].Get("$ref").String(),
		"the envelope at the literal key must be translated to a $ref, got %s", result)
}

// An $embed whose field sits under a literal dotted key must have its $template
// rewritten in place, not written to a nested tree.
func TestTranslateEmbedSpans_UnderDottedKey(t *testing.T) {
	ds, triplet, _ := seedResolvableTarget(t)

	resEnvJSON, err := json.Marshal(resEnvelope(triplet))
	require.NoError(t, err)
	tmpl := "cf.kvs('" + pkgmodel.FrameEnvelope(string(resEnvJSON)) + "')"

	properties, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				dottedGeneratorKey: map[string]any{"$embed": true, "$template": tmpl},
			},
		},
	})
	require.NoError(t, err)

	result, _, err := translatePropertiesJSON(properties, map[pkgmodel.TripletKey]string{triplet: "testdotted123"}, map[pkgmodel.GeneratorKey]string{}, ds)
	require.NoError(t, err)

	annotations := gjson.GetBytes(result, "metadata.annotations")
	require.True(t, annotations.IsObject(), "annotations must stay an object, got %s", result)
	require.Len(t, annotations.Map(), 1, "no exploded sibling may appear beside the literal key, got %s", result)

	assertSpanTranslated(t, annotations.Map()[dottedGeneratorKey].Get("$template").String())
}

// assertSpanTranslated decodes the framed span out of a $template and asserts it
// carries a $ref rather than the $res it started as.
func assertSpanTranslated(t *testing.T, template string) {
	t.Helper()
	spans, err := pkgmodel.ScanEmbedSpans(template)
	require.NoError(t, err)
	require.Len(t, spans, 1, "expected exactly one span in the translated $template, got %q", template)
	assert.Contains(t, spans[0].EnvelopeJSON, `"$ref"`,
		"the span must be rewritten to $ref form, got %s", spans[0].EnvelopeJSON)
	assert.NotContains(t, spans[0].EnvelopeJSON, `"$res"`,
		"no $res span may survive, got %s", spans[0].EnvelopeJSON)
}

func TestTranslateEmbedSpans_UnderDottedKeyInsideArray(t *testing.T) {
	ds, triplet, _ := seedResolvableTarget(t)

	resEnvJSON, err := json.Marshal(resEnvelope(triplet))
	require.NoError(t, err)
	tmpl := "cf.kvs('" + pkgmodel.FrameEnvelope(string(resEnvJSON)) + "')"

	properties, err := json.Marshal(map[string]any{
		"items": []any{map[string]any{
			dottedGeneratorKey: map[string]any{"$embed": true, "$template": tmpl},
		}},
	})
	require.NoError(t, err)

	result, _, err := translatePropertiesJSON(properties, map[pkgmodel.TripletKey]string{triplet: "testdotted123"}, map[pkgmodel.GeneratorKey]string{}, ds)
	require.NoError(t, err)

	element := gjson.GetBytes(result, "items.0")
	require.Len(t, element.Map(), 1, "no exploded sibling may appear beside the literal key, got %s", result)
	assertSpanTranslated(t, element.Map()[dottedGeneratorKey].Get("$template").String())
}

// $visibility:Opaque must be stamped inside the envelope at the literal key.
func TestMarkOpaqueResolvables_UnderDottedKey(t *testing.T) {
	triplet := pkgmodel.TripletKey{Stack: "default", Label: "secret", Type: "K8s::Core::Secret"}
	opaqueByTriplet := map[pkgmodel.TripletKey]map[string]bool{triplet: {"data": true}}

	props := `{"metadata":{"annotations":{"` + dottedGeneratorKey +
		`":{"$res":true,"$label":"secret","$type":"K8s::Core::Secret","$stack":"default","$property":"data"}}}}`

	updated, changed := markOpaqueResolvablesInProps(props, opaqueByTriplet)
	require.True(t, changed, "the envelope must be recognized as opaque")

	annotations := gjson.Get(updated, "metadata.annotations")
	require.True(t, annotations.IsObject(), "annotations must stay an object, got %s", updated)
	require.Len(t, annotations.Map(), 1, "no exploded sibling may appear beside the literal key, got %s", updated)
	assert.Equal(t, pkgmodel.VisibilityOpaque,
		annotations.Map()[dottedGeneratorKey].Get("$visibility").String(),
		"the marker must be stamped inside the envelope at the literal key, got %s", updated)
}

func TestMarkOpaqueResolvables_UnderDottedKeyInsideArray(t *testing.T) {
	triplet := pkgmodel.TripletKey{Stack: "default", Label: "secret", Type: "K8s::Core::Secret"}
	opaqueByTriplet := map[pkgmodel.TripletKey]map[string]bool{triplet: {"data": true}}

	props := `{"items":[{"` + dottedGeneratorKey +
		`":{"$res":true,"$label":"secret","$type":"K8s::Core::Secret","$stack":"default","$property":"data"}}]}`

	updated, changed := markOpaqueResolvablesInProps(props, opaqueByTriplet)
	require.True(t, changed, "the envelope must be recognized as opaque")

	element := gjson.Get(updated, "items.0")
	require.Len(t, element.Map(), 1, "no exploded sibling may appear beside the literal key, got %s", updated)
	assert.Equal(t, pkgmodel.VisibilityOpaque,
		element.Map()[dottedGeneratorKey].Get("$visibility").String(),
		"the marker must be stamped inside the envelope at the literal key, got %s", updated)
}

func TestMarkOpaqueResolvables_UnderDottedTopLevelKey(t *testing.T) {
	triplet := pkgmodel.TripletKey{Stack: "default", Label: "secret", Type: "K8s::Core::Secret"}
	opaqueByTriplet := map[pkgmodel.TripletKey]map[string]bool{triplet: {"data": true}}

	props := `{"` + dottedGeneratorKey +
		`":{"$res":true,"$label":"secret","$type":"K8s::Core::Secret","$stack":"default","$property":"data"}}`

	updated, changed := markOpaqueResolvablesInProps(props, opaqueByTriplet)
	require.True(t, changed, "the envelope must be recognized as opaque")

	parsed := gjson.Parse(updated)
	require.Len(t, parsed.Map(), 1, "no exploded sibling may appear beside the literal key, got %s", updated)
	assert.Equal(t, pkgmodel.VisibilityOpaque,
		parsed.Map()[dottedGeneratorKey].Get("$visibility").String(),
		"the marker must be stamped inside the envelope at the literal key, got %s", updated)
}

// Plain keys keep the paths they have today across all three walkers.
func TestGeneratorWalkers_PlainKeysUnchanged(t *testing.T) {
	triplet := pkgmodel.TripletKey{Stack: "default", Label: "secret", Type: "K8s::Core::Secret"}
	opaqueByTriplet := map[pkgmodel.TripletKey]map[string]bool{triplet: {"data": true}}

	props := `{"spec":{"containers":[{"env":` +
		`{"$res":true,"$label":"secret","$type":"K8s::Core::Secret","$stack":"default","$property":"data"}}]}}`

	updated, changed := markOpaqueResolvablesInProps(props, opaqueByTriplet)
	require.True(t, changed)
	assert.Equal(t, pkgmodel.VisibilityOpaque,
		gjson.Get(updated, "spec.containers.0.env.$visibility").String(),
		"got %s", updated)
}
