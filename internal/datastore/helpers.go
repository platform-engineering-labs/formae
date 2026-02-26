// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ResourcesAreEqual compares two resources and returns two booleans: the first
// one indicating whether the non-readonly properties of the resources are equal,
// and the second one indicating whether the readonly properties are equal.
func ResourcesAreEqual(resource1, resource2 *pkgmodel.Resource) (bool, bool) {
	readWriteEqual, readOnlyEqual := true, true

	if resource1.NativeID != resource2.NativeID ||
		resource1.Stack != resource2.Stack ||
		resource1.Type != resource2.Type ||
		resource1.Label != resource2.Label {
		readWriteEqual = false
	}

	if !util.JsonEqualRaw(resource1.Properties, resource2.Properties) {
		readWriteEqual = false
	}

	if !util.JsonEqualRaw(resource1.ReadOnlyProperties, resource2.ReadOnlyProperties) {
		readOnlyEqual = false
	}

	return readWriteEqual, readOnlyEqual
}

// BoolToInt converts a boolean to 0 or 1.
func BoolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
