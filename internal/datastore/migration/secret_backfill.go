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

// knownOpaqueFields maps a resource type to the top-level secret property
// names that must be hashed at rest even when a row's embedded schema does not
// mark them opaque. Rows written before a field was typed formae.SecretValue
// carry a stale, non-opaque embedded schema; keying the sweep only on that
// schema would leave their secrets in cleartext forever. This hard-coded table
// is the authoritative, plugin-independent opacity source for the backfill, so
// the sweep needs neither a running plugin coordinator nor the actor system —
// it runs against the datastore alone, before the node starts.
//
// First cut: top-level scalar secret fields only, matching PLA-320's scope
// (nested/list/map secret fields are deferred). Keep in sync with the
// SecretValue-typed fields in the resource plugins.
var knownOpaqueFields = map[string][]string{
	"AWS::SecretsManager::Secret": {"SecretString"},
}

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

			// The authoritative opacity source is the embedded schema augmented
			// with the hard-coded known-opaque fields for this resource type, so
			// rows written with a stale (pre-SecretValue) non-opaque schema are
			// still hashed.
			schema := withKnownOpaqueFields(ru.DesiredState.Schema, ru.DesiredState.Type)

			// PreviousProperties is a read-back snapshot only ever used for
			// logging and API diff display, so it's always safe to hash —
			// regardless of the owning command's state.
			changed, err := hashPropsInPlace(t, schema, &ru.PreviousProperties)
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

			changed, err = hashPropsInPlace(t, schema, &ru.PriorState.Properties)
			if err != nil {
				return fmt.Errorf("backfill: hash prior state for %s: %w", ru.DesiredState.Label, err)
			}
			dirty = dirty || changed

			changed, err = hashResourceValuesInPlace(t, schema, &ru.DesiredState)
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

func backfillResourcesTable(ds datastore.Datastore, t *transformations.PersistValueTransformer) error {
	resources, err := ds.LoadAllResources()
	if err != nil {
		return fmt.Errorf("backfill: load resources: %w", err)
	}

	for _, res := range resources {
		schema := withKnownOpaqueFields(res.Schema, res.Type)
		changed, err := hashResourceValuesInPlace(t, schema, res)
		if err != nil {
			return fmt.Errorf("backfill: hash resource %s: %w", res.Label, err)
		}
		if !changed {
			continue
		}
		if _, err := ds.StoreResource(res, "backfill-hashed-secrets"); err != nil {
			return fmt.Errorf("backfill: re-store resource %s: %w", res.Label, err)
		}
	}

	return nil
}

// withKnownOpaqueFields returns schema with the hard-coded known-opaque fields
// for resourceType merged in as Opaque hints. It leaves the input schema
// untouched (returns a copy) and never clears an existing opaque hint, so the
// embedded schema and the hard-coded table union together as the opacity
// source. Returns schema unchanged when the type has no known opaque fields.
func withKnownOpaqueFields(schema pkgmodel.Schema, resourceType string) pkgmodel.Schema {
	fields := knownOpaqueFields[resourceType]
	if len(fields) == 0 {
		return schema
	}
	hints := make(map[string]pkgmodel.FieldHint, len(schema.Hints)+len(fields))
	for k, v := range schema.Hints {
		hints[k] = v
	}
	for _, f := range fields {
		h := hints[f]
		h.Opaque = true
		hints[f] = h
	}
	out := schema
	out.Hints = hints
	return out
}

// hashResourceValuesInPlace hashes the payload columns (Properties,
// ReadOnlyProperties, PatchDocument) of res using the supplied schema, without
// altering the resource's stored Schema (the migration hashes values, it does
// not rewrite embedded schemas). It mutates res only when hashing actually
// changed something and reports whether it did.
func hashResourceValuesInPlace(t *transformations.PersistValueTransformer, schema pkgmodel.Schema, res *pkgmodel.Resource) (bool, error) {
	tmp := &pkgmodel.Resource{
		Schema:             schema,
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
