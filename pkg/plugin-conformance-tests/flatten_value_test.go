// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"encoding/json"
	"testing"
)

func TestFlattenFormaeValueWalk(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "scalar value wrapped at top level",
			in:   `{"version":{"$value":"1.34","$strategy":"SetOnce","$visibility":"Clear"}}`,
			want: `{"version":"1.34"}`,
		},
		{
			name: "nested wrapper inside an array",
			in:   `{"items":[{"$value":1},{"$value":2}]}`,
			want: `{"items":[1,2]}`,
		},
		{
			name: "wrapper carrying a nested object as its $value payload",
			in:   `{"cfg":{"$value":{"a":1,"b":[{"$value":"x"}]}}}`,
			want: `{"cfg":{"a":1,"b":["x"]}}`,
		},
		{
			name: "$res markers are left intact",
			in:   `{"kubeId":{"$res":true,"$label":"c","$type":"OVH::Kube::Cluster","$property":"id"}}`,
			want: `{"kubeId":{"$res":true,"$label":"c","$type":"OVH::Kube::Cluster","$property":"id"}}`,
		},
		{
			name: "plain scalars and maps without $value untouched",
			in:   `{"name":"abc","tags":{"env":"prod"}}`,
			want: `{"name":"abc","tags":{"env":"prod"}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var in any
			if err := json.Unmarshal([]byte(tc.in), &in); err != nil {
				t.Fatalf("unmarshal in: %v", err)
			}
			got, err := json.Marshal(flattenFormaeValueWalk(in))
			if err != nil {
				t.Fatalf("marshal got: %v", err)
			}
			// Normalise want through encode/decode so key ordering is stable.
			var w any
			if err := json.Unmarshal([]byte(tc.want), &w); err != nil {
				t.Fatalf("unmarshal want: %v", err)
			}
			normalised, _ := json.Marshal(w)
			if string(got) != string(normalised) {
				t.Errorf("flatten:\n got  %s\n want %s", got, normalised)
			}
		})
	}
}
