// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package migration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// DottedKeyReingestKey names this migration in the data_migrations table.
const DottedKeyReingestKey = "dotted_key_unmanaged_reingest"

// reingestCommandID is recorded on the tombstones this migration writes, so an
// operator reading the resource history can see what forgot the rows.
const reingestCommandID = "dotted-key-reingest"

// unmanagedScanner is the read the migration scans through. It is a named
// dependency rather than a direct datastore call so a test can observe whether
// the scan happened at all — final state alone cannot tell "skipped because the
// migration was already complete" apart from "scanned and found nothing".
type unmanagedScanner interface {
	QueryResources(query *datastore.ResourceQuery) ([]*pkgmodel.Resource, error)
}

// ReingestCorruptedUnmanagedRows repairs unmanaged rows that an older build
// stored with dot-exploded duplicates of their literal keys.
//
// The repair is not an edit. Every candidate row is tombstoned so the next
// discovery cycle re-ingests it from the cloud, which means the predicate that
// selects them may over-match without risk: the cost of a false positive is one
// redundant re-discovery, never a lost value. That is only true because
// unmanaged rows hold no user declarations — they are plugin reads plus the
// parent links discovery injected. Managed rows carry declarations, so they are
// never touched here; the troubleshooting documentation carries a query an
// operator can run against them instead.
//
// The granularity is the whole target, not the individual row. Discovery injects
// references between an unmanaged row and its parents, so forgetting one row
// would leave its siblings pointing at nothing; forgetting every unmanaged row
// on the target re-mints the family together. Re-ingested rows get new KSUIDs
// and possibly new labels, which is why a target with a reference from OUTSIDE
// that family is excluded rather than repaired.
//
// It runs at startup, before any actor, so nothing is reading or writing these
// rows while it works, and it holds a datastore-wide lease so a second agent
// booting at the same time cannot repair them concurrently.
func ReingestCorruptedUnmanagedRows(ds datastore.Datastore) error {
	return reingestCorruptedUnmanagedRows(ds, ds)
}

func reingestCorruptedUnmanagedRows(ds datastore.Datastore, scanner unmanagedScanner) error {
	ctx := context.Background()

	capable, ok := ds.(datastore.DataMigrationCapable)
	if !ok {
		// Holding a lease needs a session to pin, which this backend has not
		// got. Without one, two booting agents could repair concurrently, so the
		// repair does not run: the corruption persists, visibly, and the
		// documented manual remediation applies.
		slog.Info("data migration unsupported on this backend, skipping the dotted-key re-ingest")
		return nil
	}

	lease, err := capable.AcquireDataMigrationLease(ctx)
	if err != nil {
		if errors.Is(err, datastore.ErrDataMigrationLeaseUnavailable) {
			slog.Info("another process holds the data migration lease, deferring the dotted-key re-ingest to the next boot")
			return nil
		}
		return fmt.Errorf("failed to acquire the data migration lease: %w", err)
	}

	if err := runDottedKeyReingest(ctx, ds, scanner, lease); err != nil {
		// Release anyway so the lease is not stranded; the migration's own
		// writes roll back where they were staged, and the next boot retries.
		_ = lease.Release()
		return err
	}
	return lease.Release()
}

func runDottedKeyReingest(ctx context.Context, ds datastore.Datastore, scanner unmanagedScanner, lease datastore.DataMigrationLease) error {
	done, err := lease.HasCompletion(ctx, DottedKeyReingestKey)
	if err != nil {
		return err
	}
	if done {
		// Every target that existed when the migration finished was accounted
		// for, and a target created since cannot carry the corruption: the build
		// that mints it no longer writes dot-exploded keys.
		return nil
	}

	targets, err := ds.LoadAllTargets()
	if err != nil {
		return fmt.Errorf("failed to load targets for the dotted-key re-ingest: %w", err)
	}
	markers, err := lease.LoadMarkers(ctx, DottedKeyReingestKey)
	if err != nil {
		return err
	}

	// Loaded once: the guard asks the same question of every target.
	incomplete, err := ds.LoadIncompleteFormaCommands()
	if err != nil {
		return fmt.Errorf("failed to load in-flight commands for the dotted-key re-ingest: %w", err)
	}
	commandTouched := targetsTouchedByCommands(incomplete)

	for _, target := range targets {
		marker := datastore.DataMigrationMarker{
			TargetLabel:   target.Label,
			IncarnationID: incarnationOf(target),
		}
		if _, decided := markers[marker]; decided {
			continue
		}
		if err := processTarget(ctx, ds, scanner, lease, target, marker, commandTouched); err != nil {
			return err
		}
	}

	return writeCompletionIfDone(ctx, ds, lease)
}

func processTarget(
	ctx context.Context,
	ds datastore.Datastore,
	scanner unmanagedScanner,
	lease datastore.DataMigrationLease,
	target *pkgmodel.Target,
	marker datastore.DataMigrationMarker,
	commandTouched map[string]bool,
) error {
	rows, err := scanner.QueryResources(&datastore.ResourceQuery{
		Stack:   &datastore.QueryItem[string]{Item: constants.UnmanagedStack, Constraint: datastore.Required},
		Managed: &datastore.QueryItem[bool]{Item: false, Constraint: datastore.Required},
		Target:  &datastore.QueryItem[string]{Item: target.Label, Constraint: datastore.Required},
	})
	if err != nil {
		return fmt.Errorf("failed to scan unmanaged resources on target %s: %w", target.Label, err)
	}

	if !anyCorrupted(rows) {
		return lease.UpsertMarker(ctx, DottedKeyReingestKey, marker.TargetLabel, marker.IncarnationID,
			datastore.DataMigrationClean)
	}

	// Inbound-reference guard, terminal. Re-ingest mints new KSUIDs, so a
	// reference held from outside this target's unmanaged family would be left
	// pointing at nothing. Correcting that needs a decision only an operator can
	// make — re-import, or repoint the reference — so the target is recorded as
	// processed and never looked at again rather than retried forever.
	referrers, err := externalReferrers(ds, rows)
	if err != nil {
		return err
	}
	if len(referrers) > 0 {
		slog.Warn("skipping dotted-key re-ingest for a target whose unmanaged resources are referenced from outside",
			"target", target.Label,
			"referencedResources", referrers,
			"remedy", "re-import the affected resources, or repoint the references, then repair manually")
		return lease.UpsertMarker(ctx, DottedKeyReingestKey, marker.TargetLabel, marker.IncarnationID,
			datastore.DataMigrationExcluded)
	}

	// In-flight-command guard, transient. Startup replays persisted incomplete
	// commands, and a resumed Read against a still-present target stores what it
	// found — which would restore the very rows just tombstoned, under their old
	// identities. Deferring leaves no marker, so the next boot tries again;
	// commands complete or cancel, so the deferral drains.
	if commandTouched[target.Label] {
		slog.Info("deferring dotted-key re-ingest for a target with an in-flight command",
			"target", target.Label)
		return nil
	}

	if err := lease.TombstoneResources(ctx, rows, reingestCommandID); err != nil {
		return err
	}
	slog.Info("forgot a target's unmanaged resources so discovery re-ingests them without dot-exploded keys",
		"target", target.Label,
		"resources", len(rows))

	return lease.UpsertMarker(ctx, DottedKeyReingestKey, marker.TargetLabel, marker.IncarnationID,
		datastore.DataMigrationWiped)
}

// writeCompletionIfDone records that the migration is finished once every
// CURRENT target incarnation has a marker. The target list is read again here
// rather than reused from the start of the run, so a target created in between
// is not counted as accounted for.
func writeCompletionIfDone(ctx context.Context, ds datastore.Datastore, lease datastore.DataMigrationLease) error {
	targets, err := ds.LoadAllTargets()
	if err != nil {
		return fmt.Errorf("failed to re-read targets for the dotted-key re-ingest completion: %w", err)
	}
	markers, err := lease.LoadMarkers(ctx, DottedKeyReingestKey)
	if err != nil {
		return err
	}

	for _, target := range targets {
		marker := datastore.DataMigrationMarker{
			TargetLabel:   target.Label,
			IncarnationID: incarnationOf(target),
		}
		if _, decided := markers[marker]; !decided {
			// Something was deferred, so the migration is not finished.
			return nil
		}
	}

	slog.Info("dotted-key re-ingest is complete for every current target")
	return lease.WriteCompletion(ctx, DottedKeyReingestKey)
}

// incarnationOf reads the target's incarnation, which is what keys its marker:
// a label is reused when a target is deleted and re-created, and only the
// incarnation tells the two apart. Health is nullable, and a marker keyed on an
// empty incarnation is merely conservative — the target is rescanned — where a
// dereference would take down the boot.
func incarnationOf(target *pkgmodel.Target) string {
	if target.Health == nil {
		return ""
	}
	return target.Health.IncarnationID
}

func anyCorrupted(rows []*pkgmodel.Resource) bool {
	for _, row := range rows {
		if HasDottedKeyCorruption(row.Properties) || HasDottedKeyCorruption(row.ReadOnlyProperties) {
			return true
		}
	}
	return false
}

// externalReferrers names the target's unmanaged resources that something
// outside the family points at, whether another resource or a target's own
// configuration. References BETWEEN the family's own rows do not count: they are
// re-minted together, so nothing is left dangling.
func externalReferrers(ds datastore.Datastore, rows []*pkgmodel.Resource) ([]string, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	ksuids := make([]string, 0, len(rows))
	family := make(map[string]bool, len(rows))
	for _, row := range rows {
		ksuids = append(ksuids, row.Ksuid)
		family[row.Ksuid] = true
	}

	byResource, err := ds.FindResourcesDependingOnMany(ksuids)
	if err != nil {
		return nil, fmt.Errorf("failed to find resources referencing unmanaged rows: %w", err)
	}
	byTarget, err := ds.FindTargetsDependingOnMany(ksuids)
	if err != nil {
		return nil, fmt.Errorf("failed to find targets referencing unmanaged rows: %w", err)
	}

	var referenced []string
	for ksuid, referrers := range byResource {
		for _, referrer := range referrers {
			if !family[referrer.Ksuid] {
				referenced = append(referenced, ksuid)
				break
			}
		}
	}
	for ksuid, referrers := range byTarget {
		if len(referrers) > 0 {
			referenced = append(referenced, ksuid)
		}
	}
	return referenced, nil
}

// targetsTouchedByCommands names every target an unfinished command would act
// on, drawn from both its resource updates (whose resources name a target) and
// its target updates.
func targetsTouchedByCommands(commands []*forma_command.FormaCommand) map[string]bool {
	touched := map[string]bool{}
	for _, command := range commands {
		if command == nil {
			continue
		}
		for _, update := range command.ResourceUpdates {
			if label := update.ResourceTarget.Label; label != "" {
				touched[label] = true
			}
			if label := update.ExistingTarget.Label; label != "" {
				touched[label] = true
			}
		}
		for _, update := range command.TargetUpdates {
			if label := update.Target.Label; label != "" {
				touched[label] = true
			}
			if update.ExistingTarget != nil && update.ExistingTarget.Label != "" {
				touched[update.ExistingTarget.Label] = true
			}
		}
	}
	return touched
}
