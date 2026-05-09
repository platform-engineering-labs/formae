// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"strings"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// stripArrayIndices removes dot-separated numeric segments from a field path,
// producing a key suitable for looking up a FieldHint.
//
// The resolver emits TargetPath values like "LoadBalancers.0.TargetGroupArn"
// (dot-separated, array indices as plain integers). Plugin schemas declare
// Hints keyed by the indexless field path ("LoadBalancers.TargetGroupArn"),
// so lookups must strip indices before consulting the map.
// Note: numeric segments at the root (e.g., "42") are preserved.
func stripArrayIndices(path string) string {
	if path == "" {
		return path
	}

	parts := strings.Split(path, ".")
	var filtered []string
	for _, part := range parts {
		// Only skip numeric parts if they are not the only part (root level).
		if isNumeric(part) && len(parts) > 1 {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, ".")
}

// isNumeric returns true if all characters in s are digits.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// fieldHintForPath looks up the FieldHint for a TargetPath in the resource's
// schema. Returns the zero value if no hint is declared for the (stripped) path.
func fieldHintForPath(schema pkgmodel.Schema, targetPath string) pkgmodel.FieldHint {
	return schema.Hints[stripArrayIndices(targetPath)]
}
