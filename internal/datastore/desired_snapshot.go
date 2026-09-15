// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package datastore

import (
	"encoding/json"
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// DecodeDesiredSnapshot keeps the compatibility projection and full declaration
// sourced from the same eligible row. Physical resource identity is authoritative.
func DecodeDesiredSnapshot(id, commandID, stackID string, raw []byte) (ResourceSnapshot, error) {
	var r pkgmodel.Resource
	if err := json.Unmarshal(raw, &r); err != nil {
		return ResourceSnapshot{}, fmt.Errorf("invalid desired declaration for %s: %w", id, err)
	}
	r.Ksuid = id
	return ResourceSnapshot{KSUID: id, Type: r.Type, Label: r.Label, Target: r.Target, Properties: r.Properties, NativeID: r.NativeID, Schema: r.Schema, Declaration: &r, CommandID: commandID, StackID: stackID}, nil
}

// DesiredOwnershipReader supplies accepted ownership without pretending it is
// an inventory observation. A present key with nil ownership is authoritative.
// Call under stack/command revision protection when preparing a command.
type DesiredOwnershipReader interface {
	GetDesiredOwnership(stack string) (map[string]pkgmodel.OwnedMembers, error)
}

func ReadDesiredOwnership(ds Datastore, label string) (map[string]pkgmodel.OwnedMembers, error) {
	stack, err := ds.GetStackByLabel(label)
	if err != nil {
		return nil, err
	}
	if stack == nil {
		return nil, nil
	}
	snapshots, err := ds.GetResourcesAtLastReconcile(label)
	if err != nil {
		return nil, err
	}
	result := map[string]pkgmodel.OwnedMembers{}
	for _, s := range snapshots {
		if s.Declaration != nil && s.StackID != "" && s.StackID == stack.ID {
			result[s.KSUID] = s.Declaration.OwnedMembers
		}
	}
	return result, nil
}

// DesiredMetadataReader refuses undecodable rows; legacy inventory readers may
// continue their historical best-effort behaviour.
type DesiredMetadataReader interface {
	GetDesiredInlinePoliciesForStack(string) ([]pkgmodel.Policy, error)
	LoadDesiredGeneratorsByStack(string) ([]pkgmodel.Generator, error)
}
