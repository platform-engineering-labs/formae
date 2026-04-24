// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"testing"
)

func TestFieldHintHostsOnJSONRoundTrip(t *testing.T) {
	original := FieldHint{HostsOn: true}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded FieldHint
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !decoded.HostsOn {
		t.Fatalf("expected HostsOn=true, got %#v (raw=%s)", decoded, string(data))
	}
}

func TestFieldHintHostsOnDefaultsFalse(t *testing.T) {
	var decoded FieldHint
	if err := json.Unmarshal([]byte(`{"CreateOnly":true}`), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.HostsOn {
		t.Fatalf("expected HostsOn default false for pre-existing stored schemas, got true")
	}
}
