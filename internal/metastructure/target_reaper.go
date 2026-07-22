// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"fmt"
	"log/slog"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// TargetReaper is the actor responsible for advancing the unreachability-
// accrual clock on every currently-unreachable target and detecting reap
// candidates. It runs on a scheduled interval, mirroring StackExpirer.
//
// Task scope: this actor performs NO reaping or tombstoning action. It reads
// unreachable targets, advances their accrual via the ResourcePersister (the
// single writer for target rows), applies the command front-door check, and
// logs the resulting candidates. Actually reaping a candidate is a later
// task's responsibility.
//
// Deconfliction: the reaper only skips a candidate that has an incomplete
// FormaCommand touching it (mirrors checkForConflictingCommands). It does NOT
// deconflict against synchronization — sync runs over the entire resource set
// and can be long-running, so treating it as a front-door blocker would
// starve the reaper indefinitely. The sync race is handled elsewhere (a
// write-guard plus an announce-to-sync mechanism), not here.
type TargetReaper struct {
	act.Actor

	datastore datastore.Datastore
	interval  time.Duration
}

func NewTargetReaper() gen.ProcessBehavior {
	return &TargetReaper{}
}

// CheckUnreachableTargets is the reaper's self-tick message. ForceReap sends
// the same message to drive a single deterministic tick outside the regular
// interval (mirrors StackExpirer's CheckExpiredStacks / ForceCheckTTL).
type CheckUnreachableTargets struct{}

func (r *TargetReaper) Init(args ...any) error {
	ds, ok := r.Env("Datastore")
	if !ok {
		r.Log().Error("Missing 'Datastore' environment variable")
		return fmt.Errorf("target_reaper: missing 'Datastore' environment variable")
	}
	r.datastore = ds.(datastore.Datastore)
	r.interval = config.ReaperInterval

	if _, err := r.SendAfter(r.PID(), CheckUnreachableTargets{}, r.interval); err != nil {
		return fmt.Errorf("failed to send initial check message: %s", err)
	}
	r.Log().Info("Target reaper ready, interval=%s", r.interval)

	return nil
}

func (r *TargetReaper) HandleMessage(from gen.PID, message any) error {
	switch message.(type) {
	case CheckUnreachableTargets:
		r.checkUnreachableTargets()
	default:
		r.Log().Warning("Received unknown message type: %T", message)
	}
	return nil
}

func (r *TargetReaper) checkUnreachableTargets() {
	plans, err := planAccrualForUnreachableTargets(r.datastore, time.Now)
	if err != nil {
		r.Log().Error("Failed to plan target accrual: %v", err)
		r.scheduleNextCheck()
		return
	}

	for _, plan := range plans {
		r.advanceAccrual(plan)
	}

	candidates := reapCandidatesFromPlans(r.datastore, plans)
	if len(candidates) > 0 {
		labels := make([]string, len(candidates))
		for i, c := range candidates {
			labels[i] = c.TargetLabel
		}
		r.Log().Info("Reap candidates detected (no action taken yet): %v", labels)
	}

	r.scheduleNextCheck()
}

// advanceAccrual hands the computed delta to the ResourcePersister, the
// single writer for target rows, via an async message (mirrors how
// UpdateTargetHealth observations are applied).
func (r *TargetReaper) advanceAccrual(plan accrualPlan) {
	err := r.Send(
		gen.ProcessID{Name: actornames.ResourcePersister, Node: r.Node().Name()},
		messages.AdvanceTargetAccrual{
			TargetLabel:   plan.Target.Label,
			IncarnationID: plan.Target.Health.IncarnationID,
			LastSampleAt:  plan.LastSampleAt,
			DeltaSeconds:  plan.DeltaSeconds,
		},
	)
	if err != nil {
		r.Log().Error("Failed to send accrual advance for target label=%s: %v", plan.Target.Label, err)
	}
}

func (r *TargetReaper) scheduleNextCheck() {
	if _, err := r.SendAfter(r.PID(), CheckUnreachableTargets{}, r.interval); err != nil {
		r.Log().Error("Failed to schedule next reap check: %v", err)
	}
}

// accrualPlan is the computed per-target effect for one reaper tick: the
// unreachability-accrual delta to apply and the resulting (assumed) total.
// The assumed total drives reap-candidate evaluation without waiting for the
// asynchronous ResourcePersister write to land.
type accrualPlan struct {
	Target          *pkgmodel.Target
	LastSampleAt    time.Time
	DeltaSeconds    int64
	NewAccumSeconds int64
}

// planAccrualForUnreachableTargets computes the accrual step for every
// current-max-version target whose health_state is 'unreachable'. It performs
// no writes.
//
// Accrual formula (exact, not keyed on any nominal interval):
//
//	delta = observed_at − last_sample_at (both real read timestamps)
//	if delta > config.MaxBeatGap: delta = 0, but last_sample_at still advances
//	else: unreachable_accum_seconds += delta, last_sample_at advances
//	if last_sample_at is NULL: seed it to observed_at this tick, accrue 0
//
// now is used only defensively (logging) for a target with no observed_at at
// all, which should not happen for a row with health_state == 'unreachable'
// but is guarded against rather than assumed.
func planAccrualForUnreachableTargets(ds datastore.Datastore, now func() time.Time) ([]accrualPlan, error) {
	targets, err := ds.GetUnreachableTargets()
	if err != nil {
		return nil, fmt.Errorf("failed to load unreachable targets: %w", err)
	}

	plans := make([]accrualPlan, 0, len(targets))
	for _, target := range targets {
		if target.Health == nil || target.Health.ObservedAt == nil {
			slog.Warn("Skipping accrual for unreachable target with no observed_at",
				"target", target.Label, "now", now())
			continue
		}

		delta, newLastSampleAt := accrualStep(target.Health)
		plans = append(plans, accrualPlan{
			Target:          target,
			LastSampleAt:    newLastSampleAt,
			DeltaSeconds:    delta,
			NewAccumSeconds: target.Health.UnreachableAccumSeconds + delta,
		})
	}

	return plans, nil
}

// accrualStep computes the incremental delta (seconds) to add to
// unreachable_accum_seconds and the new last_sample_at to persist, per the
// exact accrual formula documented on planAccrualForUnreachableTargets.
func accrualStep(h *pkgmodel.TargetHealth) (deltaSeconds int64, newLastSampleAt time.Time) {
	observedAt := *h.ObservedAt

	if h.LastSampleAt == nil {
		// First sample since the target became unreachable (or since it was
		// last recovered): seed last_sample_at, accrue nothing this tick.
		return 0, observedAt
	}

	gap := observedAt.Sub(*h.LastSampleAt)
	if gap > config.MaxBeatGap {
		// Clock-stop: too large a gap between beats to trust as continuous
		// unreachable time (e.g. the reaper itself was down). Don't accrue,
		// but still move last_sample_at forward so the next tick's gap is
		// measured from here.
		return 0, observedAt
	}
	if gap < 0 {
		// Defensive: observed_at should never regress relative to
		// last_sample_at, but never accrue a negative delta.
		gap = 0
	}
	return int64(gap.Seconds()), observedAt
}

// ReapCandidate identifies a target whose (assumed post-tick) accumulated
// unreachable time has reached its configured reap-after duration and which
// has no incomplete command touching it. Task 3 only detects candidates — no
// reaping action is taken here.
type ReapCandidate struct {
	TargetLabel             string
	IncarnationID           string
	UnreachableAccumSeconds int64
}

// reapCandidatesFromPlans filters the computed accrual plans down to targets
// that have reached their configured reap-after duration AND have no
// incomplete command touching them (the front-door check, mirroring
// checkForConflictingCommands). Errors resolving an individual target's
// reaping behaviour or front-door status are logged and that target is
// skipped for this tick rather than failing the whole batch.
func reapCandidatesFromPlans(ds datastore.Datastore, plans []accrualPlan) []ReapCandidate {
	var candidates []ReapCandidate
	for _, plan := range plans {
		isCandidate, err := exceedsReapThreshold(plan.Target, plan.NewAccumSeconds)
		if err != nil {
			slog.Error("Failed to resolve reaping behaviour", "target", plan.Target.Label, "error", err)
			continue
		}
		if !isCandidate {
			continue
		}

		blocked, err := targetHasIncompleteCommand(ds, plan.Target.Label)
		if err != nil {
			slog.Error("Failed to check for incomplete commands touching target", "target", plan.Target.Label, "error", err)
			continue
		}
		if blocked {
			continue
		}

		candidates = append(candidates, ReapCandidate{
			TargetLabel:             plan.Target.Label,
			IncarnationID:           plan.Target.Health.IncarnationID,
			UnreachableAccumSeconds: plan.NewAccumSeconds,
		})
	}
	return candidates
}

// exceedsReapThreshold resolves the target's reaping behaviour (explicit or
// global default; admission always writes a resolved value, but nil is
// tolerated defensively) and reports whether accumSeconds has reached it. A
// NeverReap target is never a candidate.
func exceedsReapThreshold(target *pkgmodel.Target, accumSeconds int64) (bool, error) {
	explicit, err := pkgmodel.ParseReaping(target.Reaping)
	if err != nil {
		return false, fmt.Errorf("invalid reaping for target %s: %w", target.Label, err)
	}

	switch b := pkgmodel.ResolveReaping(explicit, nil).(type) {
	case *pkgmodel.NeverReap:
		return false, nil
	case *pkgmodel.ReapAfter:
		return accumSeconds >= b.MaxUnreachableSeconds, nil
	default:
		return false, fmt.Errorf("unknown reaping behaviour for target %s: %T", target.Label, b)
	}
}

// targetHasIncompleteCommand reports whether any incomplete FormaCommand
// touches targetLabel, mirroring the state-filtering semantics of
// checkForConflictingCommands (metastructure.go): a resource update in a
// non-terminal state (excluding Read) on the target, or a target update in a
// non-terminal state for the target itself, both count as "touching" it.
func targetHasIncompleteCommand(ds datastore.Datastore, targetLabel string) (bool, error) {
	incomplete, err := ds.LoadIncompleteFormaCommands()
	if err != nil {
		return false, fmt.Errorf("failed to load incomplete forma commands: %w", err)
	}

	for _, fc := range incomplete {
		for _, ru := range fc.ResourceUpdates {
			if ru.ResourceTarget.Label != targetLabel {
				continue
			}
			if ru.Operation == resource_update.OperationRead {
				continue
			}
			if ru.State == resource_update.ResourceUpdateStateSuccess ||
				ru.State == resource_update.ResourceUpdateStateFailed ||
				ru.State == resource_update.ResourceUpdateStateRejected {
				continue
			}
			return true, nil
		}

		for _, tu := range fc.TargetUpdates {
			if tu.Target.Label != targetLabel {
				continue
			}
			if tu.State == target_update.TargetUpdateStateSuccess ||
				tu.State == target_update.TargetUpdateStateFailed ||
				tu.State == target_update.TargetUpdateStateCanceled {
				continue
			}
			return true, nil
		}
	}

	return false, nil
}
