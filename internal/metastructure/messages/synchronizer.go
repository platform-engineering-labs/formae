// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package messages

// RegisterInProgressResource notifies the Synchronizer that a resource is being
// updated by a non-sync operation and should be excluded from synchronization
// until the operation completes.
type RegisterInProgressResource struct {
	ResourceURI string
}

// UnregisterInProgressResource notifies the Synchronizer that a resource update
// has completed and the resource can be included in future synchronization operations.
type UnregisterInProgressResource struct {
	ResourceURI string
}
