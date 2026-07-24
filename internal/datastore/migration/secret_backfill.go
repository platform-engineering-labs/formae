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
// existed. It hashes:
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

			// Opacity is resolved inside the transformer from the embedded schema
			// AND the hard-coded known-opaque table keyed on resource type, so a
			// row written with a stale (pre-SecretValue) non-opaque schema — or by
			// a plugin whose SDK predates FieldHint.Opaque — is still hashed.
			// PriorState/PreviousProperties don't carry their own resource type, so
			// pass DesiredState's.
			rt := ru.DesiredState.Type

			// PreviousProperties is a read-back snapshot only ever used for
			// logging and API diff display, so it's always safe to hash —
			// regardless of the owning command's state.
			changed, err := hashPropsInPlace(t, ru.DesiredState.Schema, rt, &ru.PreviousProperties)
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

			changed, err = hashPropsInPlace(t, ru.DesiredState.Schema, rt, &ru.PriorState.Properties)
			if err != nil {
				return fmt.Errorf("backfill: hash prior state for %s: %w", ru.DesiredState.Label, err)
			}
			dirty = dirty || changed

			changed, err = hashResourceValuesInPlace(t, &ru.DesiredState)
			if err != nil {
				return fmt.Errorf("backfill: hash desired state for %s: %w", ru.DesiredState.Label, err)
			}
			dirty = dirty || changed
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

// backfillResourcesTable scrubs plaintext opaque values from EVERY stored
// resource version, not just the current one. The resources table keeps version
// history, so a superseded version can still hold a pre-fix plaintext secret at
// rest; each such version is rewritten in place (keyed by uri+version) so no new
// version is appended and no plaintext lingers in history.
func backfillResourcesTable(ds datastore.Datastore, t *transformations.PersistValueTransformer) error {
	versions, err := ds.LoadAllResourceVersions()
	if err != nil {
		return fmt.Errorf("backfill: load resource versions: %w", err)
	}

	for _, v := range versions {
		changed, err := hashResourceValuesInPlace(t, v.Resource)
		if err != nil {
			return fmt.Errorf("backfill: hash resource %s version %s: %w", v.Resource.Label, v.Version, err)
		}
		if !changed {
			continue
		}
		if err := ds.UpdateResourceVersionData(v.URI, v.Version, v.Resource); err != nil {
			return fmt.Errorf("backfill: update resource %s version %s: %w", v.Resource.Label, v.Version, err)
		}
	}

	return nil
}

// hashResourceValuesInPlace hashes the payload columns (Properties,
// ReadOnlyProperties, PatchDocument) of res, without altering the resource's
// stored Schema (the migration hashes values, it does not rewrite embedded
// schemas). Opacity is resolved by the transformer from res.Schema plus its
// hard-coded known-opaque table keyed on res.Type. It mutates res only when
// hashing actually changed something and reports whether it did.
func hashResourceValuesInPlace(t *transformations.PersistValueTransformer, res *pkgmodel.Resource) (bool, error) {
	tmp := &pkgmodel.Resource{
		Type:               res.Type,
		Schema:             res.Schema,
		Properties:         res.Properties,
		ReadOnlyProperties: res.ReadOnlyProperties,
		PatchDocument:      res.PatchDocument,
	}
	out, err := t.ApplyToResource(tmp)
	if err != nil {
		return false, err
	}
	if !resourceChanged(tmp, out) {
		return false, nil
	}
	res.Properties = out.Properties
	res.ReadOnlyProperties = out.ReadOnlyProperties
	res.PatchDocument = out.PatchDocument
	return true, nil
}

// hashPropsInPlace hashes any opaque / opaque-enveloped values found in *props,
// with opacity resolved by the transformer from schema plus its hard-coded
// known-opaque table keyed on resourceType. schema and resourceType are passed
// separately because PriorState/PreviousProperties don't carry their own —
// the authoritative schema and type for a ResourceUpdate live on DesiredState.
// It mutates *props only when hashing actually changed something.
func hashPropsInPlace(t *transformations.PersistValueTransformer, schema pkgmodel.Schema, resourceType string, props *json.RawMessage) (bool, error) {
	if len(*props) == 0 {
		return false, nil
	}
	tmp := &pkgmodel.Resource{Type: resourceType, Schema: schema, Properties: *props}
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
