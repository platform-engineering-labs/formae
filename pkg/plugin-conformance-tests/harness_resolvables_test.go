// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"encoding/json"
	"strings"
	"testing"
)

// resolvableProps is a consumer's properties: one plain field plus one field
// fed by a reference to property on the resource labelled sourceLabel.
func resolvableProps(property, jsonPath string) json.RawMessage {
	jsonKey := ""
	if jsonPath != "" {
		jsonKey = `,"$json":"` + jsonPath + `"`
	}
	return json.RawMessage(`{
		"Name": "consumer",
		"Password": {
			"$res": true,
			"$label": "the-secret",
			"$type": "Test::Secret",
			"$stack": "default",
			"$property": "` + property + `"` + jsonKey + `
		}
	}`)
}

// createdSecret is the harness's record of the source resource after its
// out-of-band create, carrying whatever properties the plugin echoed back.
func createdSecret(properties string) []CreatedResourceInfo {
	return []CreatedResourceInfo{{
		ResourceType: "Test::Secret",
		Label:        "the-secret",
		NativeID:     "secret-1",
		Properties:   json.RawMessage(properties),
	}}
}

// TestResolveResolvables_UnreadablePropertyFails asserts that a property the
// source resource never echoed back — a writeOnly field, say — fails naming the
// property, rather than leaving the reference envelope in place for the plugin
// to receive as a value.
func TestResolveResolvables_UnreadablePropertyFails(t *testing.T) {
	h := &TestHarness{t: t}

	props, err := h.resolveResolvablesInProperties(
		resolvableProps("SecretString", ""),
		createdSecret(`{"Name":"the-secret","Arn":"arn:test:secret-1"}`),
	)
	if err == nil {
		t.Fatalf("expected an error for an unreadable property, got properties: %s", props)
	}
	for _, want := range []string{"SecretString", "the-secret", "Password"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name %q so the cause is legible, got: %v", want, err)
		}
	}
}

// TestResolveResolvables_MissingSourceResourceFails asserts the same for a
// reference to a resource that was never created.
func TestResolveResolvables_MissingSourceResourceFails(t *testing.T) {
	h := &TestHarness{t: t}

	_, err := h.resolveResolvablesInProperties(resolvableProps("Arn", ""), nil)
	if err == nil {
		t.Fatal("expected an error for a reference to a resource that was not created")
	}
	for _, want := range []string{"the-secret", "Test::Secret"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name %q so the cause is legible, got: %v", want, err)
		}
	}
}

// TestResolveResolvables_AppliesJSONPath asserts that a reference carrying
// $json takes the scalar at that path rather than the document holding it.
func TestResolveResolvables_AppliesJSONPath(t *testing.T) {
	h := &TestHarness{t: t}

	props, err := h.resolveResolvablesInProperties(
		resolvableProps("SecretString", "db.password"),
		createdSecret(`{"Name":"the-secret","SecretString":"{\"db\":{\"password\":\"p4ssw0rd\"}}"}`),
	)
	if err != nil {
		t.Fatalf("resolution failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(props, &parsed); err != nil {
		t.Fatalf("resolved properties are not valid JSON: %v", err)
	}
	if got := parsed["Password"]; got != "p4ssw0rd" {
		t.Errorf("Password must be the scalar at the $json path, got %v", got)
	}
}

// TestResolveResolvables_JSONPathOnNonJSONFails asserts that a $json path over
// a value that is not a JSON document fails naming the path, and never reports
// the value itself — which for a $json reference is a secret.
func TestResolveResolvables_JSONPathOnNonJSONFails(t *testing.T) {
	h := &TestHarness{t: t}

	_, err := h.resolveResolvablesInProperties(
		resolvableProps("SecretString", "db.password"),
		createdSecret(`{"Name":"the-secret","SecretString":"not-a-json-document"}`),
	)
	if err == nil {
		t.Fatal("expected an error for a $json path over a non-JSON value")
	}
	if !strings.Contains(err.Error(), "db.password") {
		t.Errorf("error must name the $json path, got: %v", err)
	}
	if strings.Contains(err.Error(), "not-a-json-document") {
		t.Errorf("error must not report the resolved value, got: %v", err)
	}
}

// TestResolveResolvables_ResolvesInsideArray covers a reference nested in an
// array element, whose path has to address the element by index.
func TestResolveResolvables_ResolvesInsideArray(t *testing.T) {
	h := &TestHarness{t: t}

	props, err := h.resolveResolvablesInProperties(
		json.RawMessage(`{"Tags":[{"Key":"owner","Value":{
			"$res": true,
			"$label": "the-secret",
			"$type": "Test::Secret",
			"$stack": "default",
			"$property": "Arn"
		}}]}`),
		createdSecret(`{"Name":"the-secret","Arn":"arn:test:secret-1"}`),
	)
	if err != nil {
		t.Fatalf("resolution failed: %v", err)
	}

	var parsed struct {
		Tags []struct {
			Key   string
			Value string
		}
	}
	if err := json.Unmarshal(props, &parsed); err != nil {
		t.Fatalf("resolved properties are not valid JSON: %v", err)
	}
	if len(parsed.Tags) != 1 || parsed.Tags[0].Value != "arn:test:secret-1" {
		t.Errorf("the array element's reference must resolve in place, got %s", props)
	}
}

// TestResolveResolvables_PlainReferenceResolves is the ordinary case: a
// reference to a property the source resource did echo back.
func TestResolveResolvables_PlainReferenceResolves(t *testing.T) {
	h := &TestHarness{t: t}

	props, err := h.resolveResolvablesInProperties(
		resolvableProps("Arn", ""),
		createdSecret(`{"Name":"the-secret","Arn":"arn:test:secret-1"}`),
	)
	if err != nil {
		t.Fatalf("resolution failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(props, &parsed); err != nil {
		t.Fatalf("resolved properties are not valid JSON: %v", err)
	}
	if got := parsed["Password"]; got != "arn:test:secret-1" {
		t.Errorf("Password must be the resolved value, got %v", got)
	}
	if got := parsed["Name"]; got != "consumer" {
		t.Errorf("unreferenced fields must survive resolution, got %v", got)
	}
}
