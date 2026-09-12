// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package model

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestCommandInputSnapshotPreservesLargeIntegerFromWire(t *testing.T) {
	var forma Forma
	if err := json.Unmarshal([]byte(`{"Properties":{"id":{"Type":"Int","Value":9007199254740993,"Sensitive":false}}}`), &forma); err != nil {
		t.Fatal(err)
	}
	snapshot := string(SnapshotInputProperties(forma.Properties))
	if snapshot != `{"id":{"type":"Int","value":9007199254740993}}` {
		t.Fatalf("integer rounded in history: %s", snapshot)
	}
}

func TestSnapshotInputPropertiesDistinguishesUnavailable(t *testing.T) {
	if SnapshotInputProperties(nil) != nil {
		t.Fatal("missing properties should remain unavailable")
	}
	if string(SnapshotInputProperties(map[string]Prop{})) != "{}" {
		t.Fatal("known empty inputs must be recorded")
	}
}

func TestSnapshotInputPropertiesDoesNotExposeSensitiveInputs(t *testing.T) {
	secret := true
	got := string(SnapshotInputProperties(map[string]Prop{
		"password": {Value: "secret-value", Default: "secret-default", Sensitive: &secret},
		"opaque":   {Value: map[string]any{"$ref": "resource", "$value": "material"}},
	}))
	var decoded map[string]map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["password"]["redacted"] != true || decoded["opaque"]["unavailable"] != true {
		t.Fatal(got)
	}
	for _, name := range []string{"password", "opaque"} {
		if _, ok := decoded[name]["value"]; ok {
			t.Fatalf("%s exposed its value", name)
		}
	}
}

func TestSnapshotInputPropertiesRecordsExplicitlyPublicScalarValues(t *testing.T) {
	public := false
	got := SnapshotInputProperties(map[string]Prop{
		"large": {Value: json.Number("9007199254740993"), Sensitive: &public},
		"false": {Value: false, Sensitive: &public},
		"zero":  {Value: 0, Sensitive: &public},
		"empty": {Value: "", Sensitive: &public},
		"null":  {Value: nil, Sensitive: &public},
	})
	if string(got) != `{"empty":{"value":""},"false":{"value":false},"large":{"value":9007199254740993},"null":{"value":null},"zero":{"value":0}}` {
		t.Fatalf("public scalar values not preserved: %s", got)
	}
}

func TestSnapshotInputPropertiesWithholdsUnsafePublicValuesWithoutDroppingSiblings(t *testing.T) {
	public := false
	got := SnapshotInputProperties(map[string]Prop{
		"valid": {
			Source: "supplied", Type: "String", Flag: "valid-flag",
			Value: "kept", Sensitive: &public,
		},
		"compound": {
			Source: "declaration", Type: "String",
			Value: map[string]any{"password": "nested-secret"}, Sensitive: &public,
		},
		"invalid-number": {
			Source: "supplied", Type: "Float",
			Value: json.Number("not-a-number"), Sensitive: &public,
		},
		"nan": {
			Source: "declaration", Type: "Float",
			Value: math.NaN(), Sensitive: &public,
		},
		"unsupported": {
			Source: "supplied", Type: "String",
			Value: func() {}, Sensitive: &public,
		},
	})

	if len(got) == 0 {
		t.Fatal("one invalid public value dropped the complete snapshot")
	}
	if strings.Contains(string(got), "nested-secret") || strings.Contains(string(got), "not-a-number") {
		t.Fatalf("unsafe public value exposed: %s", got)
	}
	var decoded map[string]map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["valid"]["value"] != "kept" || decoded["valid"]["flag"] != "valid-flag" {
		t.Fatalf("valid sibling lost: %s", got)
	}
	for _, name := range []string{"compound", "invalid-number", "nan", "unsupported"} {
		if decoded[name]["unavailable"] != true {
			t.Fatalf("%s should retain metadata as unavailable: %s", name, got)
		}
		if _, present := decoded[name]["value"]; present {
			t.Fatalf("%s retained an unsafe value: %s", name, got)
		}
	}
	if decoded["compound"]["source"] != "declaration" || decoded["invalid-number"]["type"] != "Float" {
		t.Fatalf("unsafe entries lost metadata: %s", got)
	}
}

func TestFormaPropertiesPreservesAbsentVersusEmptyInJSON(t *testing.T) {
	absent, err := json.Marshal(Forma{})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := json.Marshal(Forma{Properties: map[string]Prop{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(absent), `"Properties":null`) {
		t.Fatalf("absent properties collapsed: %s", absent)
	}
	if !strings.Contains(string(empty), `"Properties":{}`) {
		t.Fatalf("empty properties collapsed: %s", empty)
	}
}

func TestRecordInputPropertySourcesUsesSuppliedFlagsNotValueEquality(t *testing.T) {
	props := map[string]Prop{
		"image":    {Flag: "image-tag", Value: "stable", Default: "stable"},
		"replicas": {Value: 2, Default: 2},
	}
	RecordInputPropertySources(props, map[string]string{"image-tag": "stable"})
	if props["image"].Source != "supplied" {
		t.Fatal("explicit default-valued input lost provenance")
	}
	if props["replicas"].Source != "declaration" {
		t.Fatal("declaration input mislabeled")
	}
}

func TestSnapshotInputPropertiesWithholdsUnclassifiedValues(t *testing.T) {
	got := string(SnapshotInputProperties(map[string]Prop{
		"secret": {Value: "plaintext-secret", Default: "secret-default", Type: "String", Source: "supplied"},
	}))
	if strings.Contains(got, "plaintext-secret") || strings.Contains(got, "secret-default") {
		t.Fatal("unclassified input value leaked into command history")
	}
	var decoded map[string]map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["secret"]["unavailable"] != true || decoded["secret"]["source"] != "supplied" {
		t.Fatal("expected input metadata with unavailable value")
	}
}
