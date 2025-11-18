// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package util

import (
	"encoding/json"
	"testing"
)

func TestMapToString(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]string
		want string
	}{
		{
			name: "empty map",
			m:    map[string]string{},
			want: "",
		},
		{
			name: "single entry",
			m:    map[string]string{"key1": "value1"},
			want: `{"key1":"value1"}`,
		},
		{
			name: "multiple entries",
			m:    map[string]string{"key2": "value2", "key1": "value1"},
			// JSON object order doesn't matter, so we'll check via unmarshal
			want: "", // will be checked differently
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MapToString(tt.m)
			if tt.want == "" && len(tt.m) > 0 {
				// For multi-entry case, verify by unmarshaling
				result := StringToMap[string](got)
				if !mapsEqual(result, tt.m) {
					t.Errorf("MapToString() round-trip failed, got %v, want %v", result, tt.m)
				}
			} else if got != tt.want {
				t.Errorf("MapToString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStringToMap(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want map[string]string
	}{
		{
			name: "empty string",
			s:    "",
			want: map[string]string{},
		},
		{
			name: "single entry",
			s:    `{"key1":"value1"}`,
			want: map[string]string{"key1": "value1"},
		},
		{
			name: "multiple entries",
			s:    `{"key1":"value1","key2":"value2"}`,
			want: map[string]string{"key1": "value1", "key2": "value2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StringToMap[string](tt.s); !mapsEqual(got, tt.want) {
				t.Errorf("StringToMap() = %v, want %v", got, tt.want)
			}
		})
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestStruct is a test struct for testing MapToString and StringToMap with struct values
type TestStruct struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
	Flag  bool   `json:"flag"`
}

func TestMapToStringWithStruct(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]TestStruct
		want string
	}{
		{
			name: "empty map",
			m:    map[string]TestStruct{},
			want: "",
		},
		{
			name: "single entry",
			m: map[string]TestStruct{
				"key1": {Name: "test1", Value: 42, Flag: true},
			},
			want: "", // will be checked via round-trip
		},
		{
			name: "multiple entries",
			m: map[string]TestStruct{
				"key1": {Name: "test1", Value: 42, Flag: true},
				"key2": {Name: "test2", Value: 100, Flag: false},
			},
			want: "", // will be checked via round-trip
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MapToString(tt.m)
			if len(tt.m) == 0 {
				if got != "" {
					t.Errorf("MapToString() = %v, want empty string", got)
				}
				return
			}
			// Verify by unmarshaling
			result := StringToMap[TestStruct](got)
			if !mapsEqualStruct(result, tt.m) {
				t.Errorf("MapToString() round-trip failed, got %v, want %v", result, tt.m)
			}
		})
	}
}

func TestStringToMapWithStruct(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want map[string]TestStruct
	}{
		{
			name: "empty string",
			s:    "",
			want: map[string]TestStruct{},
		},
		{
			name: "single entry",
			s:    `{"key1":{"name":"test1","value":42,"flag":true}}`,
			want: map[string]TestStruct{
				"key1": {Name: "test1", Value: 42, Flag: true},
			},
		},
		{
			name: "multiple entries",
			s:    `{"key1":{"name":"test1","value":42,"flag":true},"key2":{"name":"test2","value":100,"flag":false}}`,
			want: map[string]TestStruct{
				"key1": {Name: "test1", Value: 42, Flag: true},
				"key2": {Name: "test2", Value: 100, Flag: false},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StringToMap[TestStruct](tt.s)
			if !mapsEqualStruct(got, tt.want) {
				t.Errorf("StringToMap() = %v, want %v", got, tt.want)
			}
		})
	}
}

func mapsEqualStruct(a, b map[string]TestStruct) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		if v != bv {
			return false
		}
	}
	return true
}

func TestRoundTripWithStruct(t *testing.T) {
	original := map[string]TestStruct{
		"resource1": {Name: "bucket", Value: 1, Flag: true},
		"resource2": {Name: "instance", Value: 2, Flag: false},
		"resource3": {Name: "database", Value: 3, Flag: true},
	}

	str := MapToString(original)
	if str == "" {
		t.Error("MapToString() returned empty string for non-empty map")
	}

	result := StringToMap[TestStruct](str)
	if !mapsEqualStruct(result, original) {
		t.Errorf("Round-trip failed: got %v, want %v", result, original)
	}

	// Verify it's valid JSON
	var jsonCheck map[string]json.RawMessage
	if err := json.Unmarshal([]byte(str), &jsonCheck); err != nil {
		t.Errorf("MapToString() produced invalid JSON: %v", err)
	}
}
