// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"encoding/json"
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ResolvableObject.Path is built by pkg/model out of literal JSON map keys, and
// the harness uses it as both a gjson read path and an sjson write path. A key
// carrying path syntax — a Kubernetes annotation, say — has to address itself
// through both, or the reference resolves into a nested tree instead of the key
// it was declared under. The harness ships separately from formae core, so it
// pins the behavior for itself.

const dottedHarnessKey = "objectset.rio.cattle.io/applied"

func dottedResolvable() string {
	return `{
		"$res": true,
		"$label": "the-secret",
		"$type": "Test::Secret",
		"$stack": "default",
		"$property": "Arn"
	}`
}

func TestResolveResolvables_ResolvesUnderDottedKey(t *testing.T) {
	h := &TestHarness{t: t}

	props, err := h.resolveResolvablesInProperties(
		json.RawMessage(`{"metadata":{"annotations":{"`+dottedHarnessKey+`":`+dottedResolvable()+`}}}`),
		createdSecret(`{"Name":"the-secret","Arn":"arn:test:secret-1"}`),
	)
	if err != nil {
		t.Fatalf("resolution failed: %v", err)
	}

	var parsed struct {
		Metadata struct {
			Annotations map[string]any
		}
	}
	if err := json.Unmarshal(props, &parsed); err != nil {
		t.Fatalf("resolved properties are not valid JSON: %v", err)
	}
	if len(parsed.Metadata.Annotations) != 1 {
		t.Fatalf("no exploded sibling may appear beside the literal key, got %s", props)
	}
	if got := parsed.Metadata.Annotations[dottedHarnessKey]; got != "arn:test:secret-1" {
		t.Errorf("the reference must resolve at the literal key, got %v (%s)", got, props)
	}
}

func TestResolveResolvables_ResolvesUnderDottedKeyInsideArray(t *testing.T) {
	h := &TestHarness{t: t}

	props, err := h.resolveResolvablesInProperties(
		json.RawMessage(`{"items":[{"`+dottedHarnessKey+`":`+dottedResolvable()+`}]}`),
		createdSecret(`{"Name":"the-secret","Arn":"arn:test:secret-1"}`),
	)
	if err != nil {
		t.Fatalf("resolution failed: %v", err)
	}

	var parsed struct {
		Items []map[string]any
	}
	if err := json.Unmarshal(props, &parsed); err != nil {
		t.Fatalf("resolved properties are not valid JSON: %v", err)
	}
	if len(parsed.Items) != 1 || len(parsed.Items[0]) != 1 {
		t.Fatalf("no exploded sibling may appear beside the literal key, got %s", props)
	}
	if got := parsed.Items[0][dottedHarnessKey]; got != "arn:test:secret-1" {
		t.Errorf("the reference must resolve at the literal key, got %v (%s)", got, props)
	}
}

func TestResolveResolvables_ResolvesUnderDottedTopLevelKey(t *testing.T) {
	h := &TestHarness{t: t}

	props, err := h.resolveResolvablesInProperties(
		json.RawMessage(`{"`+dottedHarnessKey+`":`+dottedResolvable()+`}`),
		createdSecret(`{"Name":"the-secret","Arn":"arn:test:secret-1"}`),
	)
	if err != nil {
		t.Fatalf("resolution failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(props, &parsed); err != nil {
		t.Fatalf("resolved properties are not valid JSON: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("no exploded sibling may appear beside the literal key, got %s", props)
	}
	if got := parsed[dottedHarnessKey]; got != "arn:test:secret-1" {
		t.Errorf("the reference must resolve at the literal key, got %v (%s)", got, props)
	}
}

// The unresolved-envelope guard runs after replacement: it must not report a
// dotted-key reference that did resolve.
func TestResolveResolvables_DottedKeyLeavesNoUnresolvedEnvelope(t *testing.T) {
	h := &TestHarness{t: t}

	props, err := h.resolveResolvablesInProperties(
		json.RawMessage(`{"metadata":{"annotations":{`+
			`"`+dottedHarnessKey+`":`+dottedResolvable()+`,`+
			`"app.kubernetes.io/name":`+dottedResolvable()+`}}}`),
		createdSecret(`{"Name":"the-secret","Arn":"arn:test:secret-1"}`),
	)
	if err != nil {
		t.Fatalf("both dotted-key references must resolve, got: %v", err)
	}

	var parsed struct {
		Metadata struct {
			Annotations map[string]any
		}
	}
	if err := json.Unmarshal(props, &parsed); err != nil {
		t.Fatalf("resolved properties are not valid JSON: %v", err)
	}
	if len(parsed.Metadata.Annotations) != 2 {
		t.Fatalf("both literal keys and nothing else, got %s", props)
	}
}

// referenceIsOpaque reads the envelope back at the resolvable's path, so it must
// find one declared under a dotted key.
func TestReferenceIsOpaque_UnderDottedKey(t *testing.T) {
	props := `{"metadata":{"annotations":{"` + dottedHarnessKey + `":{
		"$res": true,
		"$label": "the-secret",
		"$type": "Test::Secret",
		"$stack": "default",
		"$property": "Arn",
		"$visibility": "Opaque"
	}}}}`

	resolvables := pkgmodel.FindResolvablesFromProperties(props)
	if len(resolvables) != 1 {
		t.Fatalf("expected one resolvable, got %d", len(resolvables))
	}
	if !referenceIsOpaque(props, resolvables[0]) {
		t.Errorf("the envelope under a dotted key declares itself Opaque and must be read back as such")
	}
}
