// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestAlias_JSONRoundTrip(t *testing.T) {
	original := Alias{Label: "old-name"}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Label != "old-name" {
		t.Fatalf("expected Label=old-name, got %q", decoded.Label)
	}
}

func TestResource_AliasField(t *testing.T) {
	jsonInput := []byte(`{
        "Label": "app-server",
        "Stack": "prod",
        "Target": "aws-prod",
        "Type": "EC2.Instance",
        "Schema": {"Fields": []},
        "Properties": {},
        "Alias": {"Label": "web-server"}
    }`)

	var r Resource
	if err := json.Unmarshal(jsonInput, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if r.Alias == nil {
		t.Fatalf("expected Alias to be populated")
	}
	if r.Alias.Label != "web-server" {
		t.Fatalf("expected Alias.Label=web-server, got %q", r.Alias.Label)
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(data, []byte(`"Alias":{"Label":"web-server"}`)) {
		t.Fatalf("Alias not present in marshalled JSON: %s", data)
	}
}

func TestStack_AliasField(t *testing.T) {
	jsonInput := []byte(`{
        "Label": "production",
        "Description": "prod env",
        "Alias": {"Label": "prod"}
    }`)

	var s Stack
	if err := json.Unmarshal(jsonInput, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if s.Alias == nil || s.Alias.Label != "prod" {
		t.Fatalf("expected Alias.Label=prod, got %+v", s.Alias)
	}
}

func TestTarget_AliasField(t *testing.T) {
	jsonInput := []byte(`{
        "Label": "aws-prod-eu",
        "Namespace": "AWS",
        "Discoverable": true,
        "Alias": {"Label": "aws-prod"}
    }`)

	var tt Target
	if err := json.Unmarshal(jsonInput, &tt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if tt.Alias == nil || tt.Alias.Label != "aws-prod" {
		t.Fatalf("expected Alias.Label=aws-prod, got %+v", tt.Alias)
	}
}
