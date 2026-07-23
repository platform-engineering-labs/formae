// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package migration holds one-time, idempotent backfills that operate on row
// *values* already persisted by the datastore. This is distinct from the
// schema migrations under internal/datastore/migrations_sqlite (etc.), which
// change table structure rather than rewrite data in place.
package migration

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/transformations"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// BackfillHashedSecrets is a one-time, idempotent sweep that hashes opaque
// secret values left in cleartext by writes made before opaque-value hashing
// (PLA-320) existed. It hashes:
//
//   - PreviousProperties on every ResourceUpdate — this read-back snapshot is
//     only ever used for logging and API diff display, never fed back into a
//     plugin call, so it is always safe to hash regardless of the owning
//     command's state.
//   - PriorState.Properties and DesiredState.Properties on ResourceUpdates
//     that belong to a FormaCommand already in a final state (Success,
//     Failed, Canceled).
//   - The latest version of every row in the resources table.
//
// It deliberately does NOT hash PriorState.Properties or DesiredState on a
// ResourceUpdate whose command is still in flight (not yet in a final state):
// on resume, PriorState.Properties is fed directly into plugin Read/Update
// calls (resource_updater.convertResourceForPlugin), and a $hashed envelope
// there makes the plugin call fail (guardNoHashedValues). DesiredState
// plaintext is likewise what lets the command resume to completion after a
// restart. The live write path hashes both the moment a command reaches a
// final state (see forma_persister.hashSensitiveDataIfComplete), so this
// exemption only matters for the one sweep over old data — from then on the
// value is hashed at the moment it becomes safe to hash.
//
// Idempotent: transformations.PersistValueTransformer skips values already
// carrying $hashed:true, and this function only re-stores a row when hashing
// actually changed its bytes.
func BackfillHashedSecrets(ds datastore.Datastore) error {
	t := transformations.NewPersistValueTransformer()

	if err := backfillFormaCommands(ds, t); err != nil {
		return err
	}
	return backfillResourcesTable(ds, t)
}

func backfillFormaCommands(ds datastore.Datastore, t *transformations.PersistValueTransformer) error {
	cmds, err := ds.LoadFormaCommands()
	if err != nil {
		return fmt.Errorf("backfill: load commands: %w", err)
	}

	for _, cmd := range cmds {
		dirty := false

		for i := range cmd.ResourceUpdates {
			ru := &cmd.ResourceUpdates[i]

			// PreviousProperties is a read-back snapshot only ever used for
			// logging and API diff display, so it's always safe to hash —
			// regardless of the owning command's state.
			changed, err := hashPropsInPlace(t, ru.DesiredState.Schema, &ru.PreviousProperties)
			if err != nil {
				return fmt.Errorf("backfill: hash previous properties for %s: %w", ru.DesiredState.Label, err)
			}
			dirty = dirty || changed

			// PriorState.Properties and DesiredState input are only safe to hash
			// once the command is final: PriorState.Properties is fed directly
			// into plugin Read/Update calls on resume, and an in-flight command
			// still needs the DesiredState plaintext to resume.
			if !cmd.IsInFinalState() {
				continue
			}

			changed, err = hashPropsInPlace(t, ru.DesiredState.Schema, &ru.PriorState.Properties)
			if err != nil {
				return fmt.Errorf("backfill: hash prior state for %s: %w", ru.DesiredState.Label, err)
			}
			dirty = dirty || changed

			out, err := t.ApplyToResource(&ru.DesiredState)
			if err != nil {
				return fmt.Errorf("backfill: hash desired state for %s: %w", ru.DesiredState.Label, err)
			}
			if resourceChanged(&ru.DesiredState, out) {
				ru.DesiredState = *out
				dirty = true
			}
		}

		if !dirty {
			continue
		}
		if err := ds.BulkStoreResourceUpdates(cmd.ID, cmd.ResourceUpdates); err != nil {
			return fmt.Errorf("backfill: re-store resource updates for command %s: %w", cmd.ID, err)
		}
	}

	return nil
}

func backfillResourcesTable(ds datastore.Datastore, t *transformations.PersistValueTransformer) error {
	resources, err := ds.LoadAllResources()
	if err != nil {
		return fmt.Errorf("backfill: load resources: %w", err)
	}

	for _, res := range resources {
		out, err := t.ApplyToResource(res)
		if err != nil {
			return fmt.Errorf("backfill: hash resource %s: %w", res.Label, err)
		}
		if !resourceChanged(res, out) {
			continue
		}
		if _, err := ds.StoreResource(out, "backfill-hashed-secrets"); err != nil {
			return fmt.Errorf("backfill: re-store resource %s: %w", res.Label, err)
		}
	}

	return nil
}

// hashPropsInPlace hashes any schema-opaque / opaque-enveloped values found in
// *props, sourcing opaque-field names from schema. It mutates *props only when
// hashing actually changed something and reports whether it did. schema is
// passed in separately because PriorState/PreviousProperties don't carry their
// own schema — the authoritative schema for a ResourceUpdate's resource type
// lives on DesiredState.
func hashPropsInPlace(t *transformations.PersistValueTransformer, schema pkgmodel.Schema, props *json.RawMessage) (bool, error) {
	if len(*props) == 0 {
		return false, nil
	}
	tmp := &pkgmodel.Resource{Schema: schema, Properties: *props}
	out, err := t.ApplyToResource(tmp)
	if err != nil {
		return false, err
	}
	if bytes.Equal(out.Properties, *props) {
		return false, nil
	}
	*props = out.Properties
	return true, nil
}

// resourceChanged reports whether any of the transformable fields differ
// between before and after applying the transformer.
func resourceChanged(before, after *pkgmodel.Resource) bool {
	return !bytes.Equal(before.Properties, after.Properties) ||
		!bytes.Equal(before.ReadOnlyProperties, after.ReadOnlyProperties) ||
		!bytes.Equal(before.PatchDocument, after.PatchDocument)
}
