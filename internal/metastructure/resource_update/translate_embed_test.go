// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestTranslatePropertiesJSON_RewritesEmbeddedSpan(t *testing.T) {
	ds, _ := GetDeps(t)

	triplet := pkgmodel.TripletKey{Stack: "default", Label: "kvs", Type: "AWS::CloudFront::KeyValueStore"}
	ksuid := "testembed123"

	// Seed mock datastore so GetKSUIDByTriplet finds it
	_, err := ds.StoreResource(&pkgmodel.Resource{
		Ksuid: ksuid,
		Label: triplet.Label,
		Type:  triplet.Type,
		Stack: triplet.Stack,
	}, "cmd-1")
	require.NoError(t, err)

	// Build a $res envelope that lives inside a $embed.$template
	resEnvJSON, _ := json.Marshal(map[string]any{
		"$res":      true,
		"$label":    triplet.Label,
		"$type":     triplet.Type,
		"$stack":    triplet.Stack,
		"$property": "id",
	})
	tmpl := "cf.kvs('" + pkgmodel.FrameEnvelope(string(resEnvJSON)) + "')"

	properties, err := json.Marshal(map[string]any{
		"functionCode": map[string]any{
			"$embed":    true,
			"$template": tmpl,
		},
	})
	require.NoError(t, err)

	// tripletToKsuid map includes our triplet
	tripletToKsuid := map[pkgmodel.TripletKey]string{triplet: ksuid}

	result, _, err := translatePropertiesJSON(json.RawMessage(properties), tripletToKsuid, ds)
	require.NoError(t, err)

	// The $template in the result should have a $ref span (not a $res span)
	tmplOut := gjson.Get(string(result), "functionCode.$template").String()
	spans, scanErr := pkgmodel.ScanEmbedSpans(tmplOut)
	require.NoError(t, scanErr)
	require.Len(t, spans, 1, "expected exactly one span in translated $template")

	assert.True(t, strings.Contains(spans[0].EnvelopeJSON, `"$ref"`),
		"span should contain $ref, got: %s", spans[0].EnvelopeJSON)
	assert.True(t, strings.Contains(spans[0].EnvelopeJSON, ksuid),
		"span should contain KSUID, got: %s", spans[0].EnvelopeJSON)
	assert.False(t, strings.Contains(spans[0].EnvelopeJSON, `"$res"`),
		"span should not contain $res after forward translation, got: %s", spans[0].EnvelopeJSON)
}

func TestTranslatePropertiesJSON_RewritesEmbeddedSpan_Idempotent(t *testing.T) {
	ds, _ := GetDeps(t)

	triplet := pkgmodel.TripletKey{Stack: "default", Label: "kvs", Type: "AWS::CloudFront::KeyValueStore"}
	ksuid := "testembed456"

	_, err := ds.StoreResource(&pkgmodel.Resource{
		Ksuid: ksuid,
		Label: triplet.Label,
		Type:  triplet.Type,
		Stack: triplet.Stack,
	}, "cmd-2")
	require.NoError(t, err)

	resEnvJSON, _ := json.Marshal(map[string]any{
		"$res":      true,
		"$label":    triplet.Label,
		"$type":     triplet.Type,
		"$stack":    triplet.Stack,
		"$property": "id",
	})
	tmpl := "cf.kvs('" + pkgmodel.FrameEnvelope(string(resEnvJSON)) + "')"

	properties, err := json.Marshal(map[string]any{
		"functionCode": map[string]any{
			"$embed":    true,
			"$template": tmpl,
		},
	})
	require.NoError(t, err)

	tripletToKsuid := map[pkgmodel.TripletKey]string{triplet: ksuid}

	result1, _, err := translatePropertiesJSON(json.RawMessage(properties), tripletToKsuid, ds)
	require.NoError(t, err)

	// Applying a second time (now $ref spans, not $res spans) should be idempotent
	result2, _, err := translatePropertiesJSON(result1, tripletToKsuid, ds)
	require.NoError(t, err)

	assert.Equal(t, string(result1), string(result2), "translatePropertiesJSON should be idempotent for embed spans")
}

// TestTranslatePropertiesJSON_EmbeddedSpan_BareStack verifies that an embed span
// whose $stack was omitted resolves via (label, type). A bare resource (no
// explicit stack) renders its embed envelope before forma.pkl's single-stack
// defaulting runs, so the null $stack key is dropped — yet the resource is keyed
// in tripletToKsuid under its defaulted stack. This mirrors how the whole-value
// [formae.Resolvable] converter resolves via getResource(label, type).
func TestTranslatePropertiesJSON_EmbeddedSpan_BareStack(t *testing.T) {
	ds, _ := GetDeps(t)

	// Resource is keyed under its DEFAULTED stack, not "".
	triplet := pkgmodel.TripletKey{Stack: "default", Label: "kvs", Type: "AWS::CloudFront::KeyValueStore"}
	ksuid := "testembedbare"

	_, err := ds.StoreResource(&pkgmodel.Resource{
		Ksuid: ksuid,
		Label: triplet.Label,
		Type:  triplet.Type,
		Stack: triplet.Stack,
	}, "cmd-bare")
	require.NoError(t, err)

	// Envelope WITHOUT $stack — what the JSON renderer emits for a bare ref.
	resEnvJSON, _ := json.Marshal(map[string]any{
		"$res":      true,
		"$label":    triplet.Label,
		"$type":     triplet.Type,
		"$property": "id",
	})
	tmpl := "cf.kvs('" + pkgmodel.FrameEnvelope(string(resEnvJSON)) + "')"

	properties, err := json.Marshal(map[string]any{
		"functionCode": map[string]any{
			"$embed":    true,
			"$template": tmpl,
		},
	})
	require.NoError(t, err)

	tripletToKsuid := map[pkgmodel.TripletKey]string{triplet: ksuid}

	result, _, err := translatePropertiesJSON(json.RawMessage(properties), tripletToKsuid, ds)
	require.NoError(t, err, "bare-stack embed span should resolve via (label, type)")

	tmplOut := gjson.Get(string(result), "functionCode.$template").String()
	spans, scanErr := pkgmodel.ScanEmbedSpans(tmplOut)
	require.NoError(t, scanErr)
	require.Len(t, spans, 1)
	assert.Contains(t, spans[0].EnvelopeJSON, `"$ref"`)
	assert.Contains(t, spans[0].EnvelopeJSON, ksuid)
}

// TestTranslatePropertiesJSON_EmbeddedSpan_BareStackAmbiguous verifies that a
// bare embed reference whose (label, type) matches more than one resource across
// stacks is rejected, so the user disambiguates with an explicit stack.
func TestTranslatePropertiesJSON_EmbeddedSpan_BareStackAmbiguous(t *testing.T) {
	ds, _ := GetDeps(t)

	a := pkgmodel.TripletKey{Stack: "stack-a", Label: "kvs", Type: "AWS::CloudFront::KeyValueStore"}
	b := pkgmodel.TripletKey{Stack: "stack-b", Label: "kvs", Type: "AWS::CloudFront::KeyValueStore"}

	resEnvJSON, _ := json.Marshal(map[string]any{
		"$res":      true,
		"$label":    "kvs",
		"$type":     "AWS::CloudFront::KeyValueStore",
		"$property": "id",
	})
	tmpl := "cf.kvs('" + pkgmodel.FrameEnvelope(string(resEnvJSON)) + "')"

	properties, err := json.Marshal(map[string]any{
		"functionCode": map[string]any{
			"$embed":    true,
			"$template": tmpl,
		},
	})
	require.NoError(t, err)

	tripletToKsuid := map[pkgmodel.TripletKey]string{a: "ksuidA", b: "ksuidB"}

	_, _, err = translatePropertiesJSON(json.RawMessage(properties), tripletToKsuid, ds)
	require.Error(t, err, "ambiguous bare-stack embed should error")
	assert.Contains(t, err.Error(), "incomplete triplet")
}
