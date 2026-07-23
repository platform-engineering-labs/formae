// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package target_reaper

import (
	"fmt"
	"log/slog"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/reaping"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// TargetReaper is the actor responsible for advancing the unreachability-
// accrual clock on every currently-unreachable target, detecting reap
// candidates, and actually reaping them via the ResourcePersister. It runs on
// a scheduled interval, mirroring StackExpirer.
//
// Blast-radius control: reaping a whole tick's worth of candidates
// unconditionally would let a correlated outage (many targets crossing their
// threshold together) tombstone the entire fleet in one cycle, so at most
// maxReapsPerTick candidates are reaped per tick and the remainder are logged
// and retried on the next tick. Reaping is fully recoverable (a re-apply
// re-adopts), so this is blast-radius insurance rather than a hard limit — an
// internal constant, not user-configurable.
//
// Deconfliction: the reaper only skips a candidate that has an incomplete
// FormaCommand touching it (mirrors checkForConflictingCommands). It does NOT
// deconflict against synchronization by skipping — sync runs over the entire
// resource set and can be long-running, so treating it as a front-door
// blocker would starve the reaper indefinitely. Instead, around each reap the
// reaper announces the target's resources to the Synchronizer's exclusion set
// (the same mechanism the changeset executor uses), so sync does not race a
// reap in flight; the datastore resource-write guard remains the
// timing-independent backstop regardless.
type TargetReaper struct {
	act.Actor

	datastore  datastore.Datastore
	interval   time.Duration
	maxBeatGap time.Duration
}

// maxReapsPerTick bounds how many over-threshold targets the reaper tombstones
// in a single tick (see the type doc). Internal, not user-configurable.
const maxReapsPerTick = 50

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

	// Both the reaper cadence and its clock-stop threshold derive from the one
	// user-facing knob — the agent's synchronization interval — so a fast (e.g.
	// demo) interval reaps responsively and neither value needs separate
	// operator tuning. A missing SynchronizationConfig falls back to the package
	// nominal defensively (production always sets it).
	syncInterval := reaping.NominalSyncInterval
	if sc, ok := r.Env("SynchronizationConfig"); ok {
		syncInterval = sc.(pkgmodel.SynchronizationConfig).Interval
	}
	r.interval = reaping.DeriveReaperInterval(syncInterval)
	r.maxBeatGap = reaping.DeriveMaxBeatGap(syncInterval)

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
	// A single "now" anchors this whole tick: it is the clock used to detect
	// candidates AND the grace cutoff passed to PersistTargetReap for any of
	// them actually reaped this tick, so a health beat that lands after this
	// instant (mid-tick) always invalidates a stale reap attempt rather than
	// racing it.
	now := time.Now()

	plans, err := planAccrualForUnreachableTargets(r.datastore, func() time.Time { return now }, r.maxBeatGap)
	if err != nil {
		r.Log().Error("Failed to plan target accrual: %v", err)
		r.scheduleNextCheck()
		return
	}

	for _, plan := range plans {
		r.advanceAccrual(plan)
	}

	candidates := reapCandidatesFromPlans(r.datastore, plans)
	if len(candidates) == 0 {
		r.scheduleNextCheck()
		return
	}

	labels := make([]string, len(candidates))
	for i, c := range candidates {
		labels[i] = c.TargetLabel
	}
	r.Log().Info("Reap candidates detected (reap-pending): %v", labels)

	execution := planReapExecution(candidates, maxReapsPerTick)

	for _, candidate := range execution.ToReap {
		r.reap(candidate, now)
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

// reap actually reaps one candidate: it announces the target's resources to
// the Synchronizer's exclusion set (so an in-flight sync cycle can't race the
// reap), calls ResourcePersister.PersistTargetReap, then un-announces
// regardless of the outcome. now is the tick's anchor instant, used as the
// grace cutoff for both last_seen_at and last_sample_at and as the audit
// ReapedAt.
//
// A false PersistTargetReapResult.Reaped is not an error — it means the CAS
// was rejected (a fresher health beat landed, an incomplete command now
// touches the target, or it was already reaped/recovered since this tick's
// candidate detection). The target simply stays a candidate and is
// re-evaluated on the next tick.
func (r *TargetReaper) reap(candidate ReapCandidate, now time.Time) {
	resourceURIs := r.announceToSynchronizer(candidate.TargetLabel)
	defer r.unannounceFromSynchronizer(resourceURIs)

	response, err := r.Call(
		gen.ProcessID{Name: actornames.ResourcePersister, Node: r.Node().Name()},
		messages.PersistTargetReap{
			Label:            candidate.TargetLabel,
			IncarnationID:    candidate.IncarnationID,
			LastSeenBefore:   now,
			LastSampleBefore: now,
			ReapedAt:         now,
		},
	)
	if err != nil {
		r.Log().Error("Failed to call PersistTargetReap for target label=%s: %v", candidate.TargetLabel, err)
		return
	}

	result, ok := response.(messages.PersistTargetReapResult)
	if !ok {
		r.Log().Error("Unexpected response type from ResourcePersister for target reap label=%s type=%T",
			candidate.TargetLabel, response)
		return
	}

	if !result.Reaped {
		r.Log().Warning("Target reap did not commit for label=%s (stale, blocked, or already reaped); will re-evaluate next tick", candidate.TargetLabel)
		return
	}

	r.Log().Info("Target reaped label=%s incarnation=%s unreachableAccumSeconds=%d",
		candidate.TargetLabel, candidate.IncarnationID, candidate.UnreachableAccumSeconds)
}

// announceToSynchronizer registers every live resource on targetLabel as
// in-progress with the Synchronizer, mirroring the changeset executor's
// register-before-mutate pattern (registerAllResourcesWithSynchronizer) —
// reaping bypasses the changeset executor entirely, so it announces directly.
// Returns the URIs actually registered, so they can be unregistered
// afterwards regardless of the reap's outcome.
func (r *TargetReaper) announceToSynchronizer(targetLabel string) []string {
	resources, err := r.datastore.QueryResources(&datastore.ResourceQuery{
		Target: &datastore.QueryItem[string]{Item: targetLabel, Constraint: datastore.Required},
	})
	if err != nil {
		r.Log().Error("Failed to load resources to announce for target reap label=%s: %v", targetLabel, err)
		return nil
	}

	synchronizerPID := gen.ProcessID{Name: actornames.Synchronizer, Node: r.Node().Name()}
	registeredURIs := make([]string, 0, len(resources))
	for _, resource := range resources {
		uri := string(resource.URI())
		if err := r.Send(synchronizerPID, messages.RegisterInProgressResource{ResourceURI: uri}); err != nil {
			r.Log().Error("Failed to register in-progress resource with synchronizer resourceURI=%s: %v", uri, err)
			continue
		}
		registeredURIs = append(registeredURIs, uri)
	}
	return registeredURIs
}

// unannounceFromSynchronizer reverses announceToSynchronizer for every URI it
// successfully registered.
func (r *TargetReaper) unannounceFromSynchronizer(resourceURIs []string) {
	synchronizerPID := gen.ProcessID{Name: actornames.Synchronizer, Node: r.Node().Name()}
	for _, uri := range resourceURIs {
		if err := r.Send(synchronizerPID, messages.UnregisterInProgressResource{ResourceURI: uri}); err != nil {
			r.Log().Error("Failed to unregister in-progress resource from synchronizer resourceURI=%s: %v", uri, err)
		}
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
//	if delta > maxBeatGap: delta = 0, but last_sample_at still advances
//	else: unreachable_accum_seconds += delta, last_sample_at advances
//	if last_sample_at is NULL: seed it to observed_at this tick, accrue 0
//
// maxBeatGap is the reaper's derived clock-stop threshold (reaping.DeriveMaxBeatGap),
// computed once from the agent's configured synchronization interval.
//
// now is used only defensively (logging) for a target with no observed_at at
// all, which should not happen for a row with health_state == 'unreachable'
// but is guarded against rather than assumed.
func planAccrualForUnreachableTargets(ds datastore.Datastore, now func() time.Time, maxBeatGap time.Duration) ([]accrualPlan, error) {
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

		delta, newLastSampleAt := accrualStep(target.Health, maxBeatGap)
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
// maxBeatGap is the reaper's derived clock-stop threshold.
func accrualStep(h *pkgmodel.TargetHealth, maxBeatGap time.Duration) (deltaSeconds int64, newLastSampleAt time.Time) {
	observedAt := *h.ObservedAt

	if h.LastSampleAt == nil {
		// First sample since the target became unreachable (or since it was
		// last recovered): seed last_sample_at, accrue nothing this tick.
		return 0, observedAt
	}

	gap := observedAt.Sub(*h.LastSampleAt)
	if gap > maxBeatGap {
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
// has no incomplete command touching it (the front-door check). Detecting a
// candidate does not by itself reap it — see planReapExecution and
// TargetReaper.reap for the per-tick rate cap applied on top.
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

// reapExecutionPlan is the result of applying the per-tick rate cap to this
// tick's reap candidates: ToReap will actually be reaped this tick; Deferred
// is retried on the next tick (still a candidate — nothing about it changed,
// it was simply not this tick's turn).
type reapExecutionPlan struct {
	ToReap   []ReapCandidate
	Deferred []ReapCandidate
}

// planReapExecution applies the global per-cycle rate/batch cap: at most
// maxPerTick candidates are reaped this tick, in the order reapCandidatesFromPlans
// returned them; the remainder are logged (so nothing is silently dropped)
// and deferred to the next tick. maxPerTick <= 0 means unbounded — every
// candidate is reaped this tick.
//
// This is a pure function (aside from the logging side effect): it makes no
// datastore writes and no actor calls, so the rate-cap decision is testable
// without a running actor system.
func planReapExecution(candidates []ReapCandidate, maxPerTick int) reapExecutionPlan {
	if maxPerTick <= 0 || len(candidates) <= maxPerTick {
		return reapExecutionPlan{ToReap: candidates}
	}

	plan := reapExecutionPlan{
		ToReap:   candidates[:maxPerTick],
		Deferred: candidates[maxPerTick:],
	}
	slog.Warn("Target reap rate cap reached; deferring remaining candidates to next tick",
		"cap", maxPerTick, "candidateCount", len(candidates), "deferredCount", len(plan.Deferred),
		"deferred", reapCandidateLabels(plan.Deferred))
	return plan
}

// reapCandidateLabels extracts the target labels from a candidate slice, for
// logging.
func reapCandidateLabels(candidates []ReapCandidate) []string {
	labels := make([]string, len(candidates))
	for i, c := range candidates {
		labels[i] = c.TargetLabel
	}
	return labels
}

// TargetReapStatus returns the derived display status for a target:
//   - "reaped" when its persisted health_state already is 'reaped';
//   - "reap-pending" when it is 'unreachable' and has already accrued at
//     least its configured reap-after duration — i.e. it is a would-be reap
//     candidate, whether or not the front-door incomplete-command check would
//     currently block the actual reap. This makes an over-threshold target
//     visible before any tombstone, independent of whether the reaper has run
//     a tick yet.
//   - its raw persisted health_state otherwise (e.g. "reachable", "unknown").
//
// "reap-pending" is a purely computed status: it is never written to the
// health_state column.
func TargetReapStatus(target *pkgmodel.Target) (string, error) {
	if target == nil || target.Health == nil {
		return pkgmodel.TargetHealthStateUnknown, nil
	}
	if target.Health.State == pkgmodel.TargetHealthStateReaped {
		return pkgmodel.TargetHealthStateReaped, nil
	}
	if target.Health.State == pkgmodel.TargetHealthStateUnreachable {
		pending, err := exceedsReapThreshold(target, target.Health.UnreachableAccumSeconds)
		if err != nil {
			return "", err
		}
		if pending {
			return pkgmodel.TargetHealthStateReapPending, nil
		}
	}
	return target.Health.State, nil
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
