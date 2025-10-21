// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package util

import "testing"

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
			want: "key1:value1",
		},
		{
			name: "multiple entries",
			m:    map[string]string{"key2": "value2", "key1": "value1"},
			want: "key1:value1,key2:value2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MapToString(tt.m); got != tt.want {
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
			s:    "key1:value1",
			want: map[string]string{"key1": "value1"},
		},
		{
			name: "multiple entries",
			s:    "key1:value1,key2:value2",
			want: map[string]string{"key1": "value1", "key2": "value2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StringToMap(tt.s); !mapsEqual(got, tt.want) {
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
