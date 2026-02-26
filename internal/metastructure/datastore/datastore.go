// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package datastore provides backward-compatible type aliases for the datastore
// interface and query types. The canonical definitions now live in
// internal/datastore; this package re-exports them so that existing consumers
// continue to compile without import-path changes.
package datastore

import (
	newds "github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Type aliases — these make old and new types interchangeable.
type (
	Datastore            = newds.Datastore
	QueryItemConstraint  = newds.QueryItemConstraint
	QueryItem[T any]     = newds.QueryItem[T]
	StatusQuery          = newds.StatusQuery
	ResourceQuery        = newds.ResourceQuery
	DestroyResourcesQuery = newds.DestroyResourcesQuery
	TargetQuery          = newds.TargetQuery
	ResourceModification = newds.ResourceModification
	ResourceUpdateRef    = newds.ResourceUpdateRef
	ExpiredStackInfo     = newds.ExpiredStackInfo
	StackReconcileInfo   = newds.StackReconcileInfo
	ResourceSnapshot     = newds.ResourceSnapshot
)

// Constant aliases — re-exported so existing code compiles unchanged.
const (
	CommandsTable                  = newds.CommandsTable
	DefaultFormaCommandsQueryLimit = newds.DefaultFormaCommandsQueryLimit
	Optional                       = newds.Optional
	Required                       = newds.Required
	Excluded                       = newds.Excluded
)

// resourcesAreEqual compares two resources and returns two booleans: the first one indicating whether the
// non-readonly properties of the resources are equal, and the second one indicating whether the readonly
// properties are equal.
func resourcesAreEqual(resource1, resource2 *pkgmodel.Resource) (bool, bool) {
	readWriteEqual, readOnlyEqual := true, true

	// Compare table fields
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

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
