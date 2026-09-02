// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sort"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/drift"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// GeneratorRotator is the actor that rotates generators on their declared
// cadence. It is the third scheduled actor, beside the StackExpirer and the
// AutoReconciler, and it works the same way: a fixed sweep asks the datastore
// what is due and submits one command per item.
//
// The cadence is DERIVED, never stored. GetGeneratorsWithRotation reads the
// interval off the generator's declared spec and the last-rotation instant out
// of command history, exactly as the auto-reconcile schedule derives
// LastReconcileAt. A stored last-rotated-at would participate in
// desired-config equality, read as metadata drift, and get rendered into
// formae people copy between environments.
const (
	// DefaultGeneratorRotatorInterval is how often the rotator asks what is
	// due. Rotation cadences are hours and days, so the sweep only has to be
	// fine enough that a due generator is not left waiting noticeably longer
	// than its jitter.
	DefaultGeneratorRotatorInterval = 30 * time.Second

	// maxRotationJitter caps the per-generator offset. Without a cap an
	// annual cadence would drift by weeks, which is a schedule nobody can
	// reason about. An hour is wide enough that a large fleet on a daily
	// cadence is spread thin, and narrow enough that the cadence an operator
	// declared is still the cadence they get.
	maxRotationJitter = time.Hour

	// rotationJitterDivisor makes the jitter bound a tenth of the cadence, so
	// the offset is always small next to the interval it spreads.
	rotationJitterDivisor = 10

	// rotationRetryBaseDelay is the delay before a first retry. Doubling from
	// here, and saturating at one cadence, is what keeps a generator whose
	// rotation keeps failing from either hammering its provider or being
	// abandoned.
	rotationRetryBaseDelay = 30 * time.Second

	// maxRotationRetryAttempts is where the backoff stops growing. Beyond it
	// the generator is retried once per cadence: rotation is not optional
	// work that can be dropped, so there is no attempt count at which the
	// credential stops being rotated altogether.
	maxRotationRetryAttempts = 5
)

// rotationClientID is what a rotation command records as its client, mirroring
// the auto-reconciler's and the stack expirer's.
const rotationClientID = "generator-rotator"

type GeneratorRotator struct {
	act.Actor

	datastore datastore.Datastore
	interval  time.Duration

	// inFlight tracks the generators with a rotation command in progress.
	//
	// This lives in actor memory, with no datastore lease and no
	// compare-and-swap, and that is deliberate rather than an omission. An
	// agent runs a single task, and the metastructure actor serializes
	// command mutation, so there is no second rotator to race with: the only
	// concurrency a lease would protect against cannot occur. Neither of the
	// other two scheduled actors has one either, and requiring one here would
	// hold rotation to a bar the mechanisms beside it do not meet, for a race
	// the deployment shape rules out.
	inFlight map[string]rotationAttempt

	// attempts counts consecutive failed rotation attempts per generator, and
	// nextAttemptAt holds the instant each may next be tried. Both are actor
	// memory: a restart resets the backoff and the generator is retried at
	// once. That is accepted as annoying rather than dangerous — the worst
	// case is one extra attempt at the failure that was already failing, and
	// the cadence itself, which is what must not be lost, is derived from the
	// datastore and survives the restart.
	attempts      map[string]int
	nextAttemptAt map[string]time.Time
}

// rotationAttempt is one in-flight rotation: the command executing it and the
// cadence it was submitted under. The cadence is carried here because the
// completion message names only a command, and the retry backoff is bounded by
// the generator's own interval — a generator that rotates every thirty seconds
// must not be retried eight minutes later.
type rotationAttempt struct {
	commandID string
	interval  time.Duration
}

func NewGeneratorRotator() gen.ProcessBehavior {
	return &GeneratorRotator{}
}

// CheckGeneratorRotations is the rotator's sweep tick.
type CheckGeneratorRotations struct{}

func (g *GeneratorRotator) Init(args ...any) error {
	ds, ok := g.Env("Datastore")
	if !ok {
		g.Log().Error("Missing 'Datastore' environment variable")
		return fmt.Errorf("generator_rotator: missing 'Datastore' environment variable")
	}

	g.datastore = ds.(datastore.Datastore)
	g.interval = DefaultGeneratorRotatorInterval
	g.inFlight = make(map[string]rotationAttempt)
	g.attempts = make(map[string]int)
	g.nextAttemptAt = make(map[string]time.Time)

	if _, err := g.SendAfter(g.PID(), CheckGeneratorRotations{}, g.interval); err != nil {
		return fmt.Errorf("failed to send initial rotation check message: %s", err)
	}
	g.Log().Info("Generator rotator ready, interval=%s", g.interval)

	return nil
}

func (g *GeneratorRotator) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case CheckGeneratorRotations:
		g.checkRotations()
	case changeset.ChangesetCompleted:
		g.rotationCompleted(msg)
	default:
		g.Log().Warning("Received unknown message type: %T", message)
	}
	return nil
}

// checkRotations submits a rotation for every generator whose cadence has
// elapsed. Each is submitted once: a generator whose cadence elapsed ten times
// over is one command, because the next committed rotation moves the anchor to
// now and nothing counts the windows that were missed.
func (g *GeneratorRotator) checkRotations() {
	defer g.scheduleNextRotationCheck()

	infos, err := g.datastore.GetGeneratorsWithRotation()
	if err != nil {
		g.Log().Error("Failed to query generators with rotation: %v", err)
		return
	}

	now := time.Now().UTC()
	for _, info := range rotationsDueNow(infos, g.inFlight, g.nextAttemptAt, now) {
		g.Log().Info("Rotating generator label=%s stack=%s interval=%ds lastRotationAt=%s",
			info.Label, info.StackLabel, info.IntervalSeconds, rotationAnchor(info))

		commandID, err := g.startRotation(info)
		if err != nil {
			g.Log().Error("Failed to start rotation generator=%s: %v", info.GeneratorID, err)
			g.recordFailedAttempt(info)
			continue
		}
		if commandID == "" {
			// Nothing to rotate right now, and not a failure: the generator
			// moved under us, has no destination, or its stack is busy. The
			// next sweep re-reads and decides again.
			continue
		}
		g.inFlight[info.GeneratorID] = rotationAttempt{
			commandID: commandID,
			interval:  time.Duration(info.IntervalSeconds) * time.Second,
		}
	}
}

// rotationCompleted clears the in-flight guard and, on failure, arms the
// backoff. Success clears the backoff outright: the cadence itself is derived
// from the command that just succeeded, so there is nothing to remember.
func (g *GeneratorRotator) rotationCompleted(msg changeset.ChangesetCompleted) {
	var generatorID string
	var interval time.Duration
	for id, attempt := range g.inFlight {
		if attempt.commandID == msg.CommandID {
			generatorID = id
			interval = attempt.interval
			break
		}
	}
	if generatorID == "" {
		g.Log().Debug("Received ChangesetCompleted for a command that is not a rotation commandID=%s", msg.CommandID)
		return
	}

	delete(g.inFlight, generatorID)

	if msg.State == changeset.ChangeSetStateFinishedSuccessfully {
		g.Log().Info("Rotation complete generator=%s command=%s", generatorID, msg.CommandID)
		delete(g.attempts, generatorID)
		delete(g.nextAttemptAt, generatorID)
		return
	}

	// The cadence does not move: it is derived from the command's state, and
	// this command is not a success, so the next sweep still measures from
	// the previous committed rotation. Only the retry delay changes.
	g.Log().Warning("Rotation did not commit, cadence unchanged generator=%s command=%s state=%s",
		generatorID, msg.CommandID, msg.State)
	g.recordFailedAttemptByID(generatorID, interval)
}

// recordFailedAttempt arms the backoff for a generator whose rotation could
// not be submitted or did not commit.
func (g *GeneratorRotator) recordFailedAttempt(info datastore.GeneratorRotationInfo) {
	g.recordFailedAttemptByID(info.GeneratorID, time.Duration(info.IntervalSeconds)*time.Second)
}

// recordFailedAttemptByID arms the backoff by generator id. A zero interval
// means the cadence is not in hand; the backoff then grows from the base delay
// with nothing to saturate at, which maxRotationRetryAttempts still bounds.
func (g *GeneratorRotator) recordFailedAttemptByID(generatorID string, interval time.Duration) {
	g.attempts[generatorID]++
	delay := rotationBackoff(g.attempts[generatorID], interval)
	g.nextAttemptAt[generatorID] = time.Now().UTC().Add(delay)
	g.Log().Debug("Armed rotation backoff generator=%s attempts=%d delay=%s",
		generatorID, g.attempts[generatorID], delay)
}

func (g *GeneratorRotator) scheduleNextRotationCheck() {
	if _, err := g.SendAfter(g.PID(), CheckGeneratorRotations{}, g.interval); err != nil {
		g.Log().Error("Failed to schedule next rotation check: %v", err)
	}
}

// startRotation prepares and submits one generator's rotation, returning the
// command id. An empty command id and a nil error mean there was nothing to do.
func (g *GeneratorRotator) startRotation(info datastore.GeneratorRotationInfo) (string, error) {
	// A user command on the stack takes priority, exactly as it does for
	// auto-reconcile: the rotation is not urgent to the second, and planning
	// against a stack that is mid-apply reads state that is about to move.
	hasActive, err := g.datastore.StackHasActiveCommands(info.StackLabel)
	if err != nil {
		return "", fmt.Errorf("failed to check for active commands: %w", err)
	}
	if hasActive {
		g.Log().Info("Skipping rotation, stack has active commands stack=%s", info.StackLabel)
		return "", nil
	}

	result, err := prepareRotation(g.datastore, info)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", nil
	}

	_, err = g.Call(
		gen.ProcessID{Name: actornames.FormaCommandPersister, Node: g.Node().Name()},
		forma_persister.StoreNewFormaCommand{Command: *result.command},
	)
	if err != nil {
		return "", fmt.Errorf("failed to store rotation command: %w", err)
	}

	_, err = g.Call(
		gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: g.Node().Name()},
		changeset.EnsureChangesetExecutor{CommandID: result.command.ID},
	)
	if err != nil {
		return "", fmt.Errorf("failed to ensure changeset executor: %w", err)
	}

	// NotifyOnComplete is what makes the milestone observable here: the
	// completion tells the rotator whether every authority-side update
	// succeeded, which is the one thing that decides whether the cadence
	// advanced.
	err = g.Send(
		gen.ProcessID{Name: actornames.ChangesetExecutor(result.command.ID), Node: g.Node().Name()},
		changeset.Start{Changeset: result.changeset, NotifyOnComplete: true},
	)
	if err != nil {
		return "", fmt.Errorf("failed to start changeset executor: %w", err)
	}

	return result.command.ID, nil
}

// rotationJitterBound is the width of the window a generator's rotation may
// slip into: a tenth of its cadence, capped.
func rotationJitterBound(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	bound := interval / rotationJitterDivisor
	if bound > maxRotationJitter {
		bound = maxRotationJitter
	}
	return bound
}

// rotationJitter is the offset added to one generator's cadence so a fleet
// does not rotate in lockstep.
//
// It is derived from the generator's identity rather than drawn at random, and
// that is the point rather than a shortcut. A fleet stood up by one apply
// shares a last-rotation instant to the second, so an offset that is a
// function of the generator spreads them permanently, while a redrawn random
// offset would reshuffle the schedule on every restart and give an operator
// nothing to predict. The identity is a KSUID, so the hash spreads across the
// window rather than clustering.
func rotationJitter(generatorID string, interval time.Duration) time.Duration {
	bound := rotationJitterBound(interval)
	if bound <= 0 {
		return 0
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(generatorID))
	return time.Duration(hash.Sum64() % uint64(bound))
}

// nextRotationDue returns the instant this generator is next due. The zero
// instant means due now.
//
// The delay is fixed and measured from the last rotation that COMMITTED, which
// is what GetGeneratorRotationInfo derives: a failed attempt leaves the anchor
// where it was, so a run of failures does not push the cadence out.
func nextRotationDue(info datastore.GeneratorRotationInfo) time.Time {
	if info.LastRotationAt.IsZero() {
		// No rotation on record. Attaching rotation to a credential nobody
		// has rotated is a request to rotate it, so it is due now — and
		// deliberately without jitter, which exists to break up a RECURRING
		// alignment rather than to delay a first run.
		return time.Time{}
	}
	interval := time.Duration(info.IntervalSeconds) * time.Second
	return info.LastRotationAt.Add(interval + rotationJitter(info.GeneratorID, interval))
}

// rotationIsDue reports whether this generator's cadence has elapsed as of now.
func rotationIsDue(info datastore.GeneratorRotationInfo, now time.Time) bool {
	due := nextRotationDue(info)
	return !now.Before(due)
}

// rotationsDueNow selects the generators this sweep should submit a rotation
// for, in the order the datastore returned them.
//
// It is the whole of the sweep's decision, separated from the submission so
// that "what a tick does" is answerable without a running node. Three things
// take a generator out of this sweep:
//
//   - an attempt already in flight. This is the guard that makes a repeated
//     tick a no-op: a rotation takes as long as the provider writes take, which
//     is many sweeps, and submitting a second command for the same generator
//     would draw twice and leave the two halves of one credential's
//     destinations on different generations.
//   - a backoff still running after a failed attempt.
//   - a cadence that has not elapsed.
//
// A generator whose cadence elapsed many times over appears ONCE, like any
// other due generator: there is no queue of missed windows to work through,
// and the next committed rotation moves the anchor to now.
func rotationsDueNow(
	infos []datastore.GeneratorRotationInfo,
	inFlight map[string]rotationAttempt,
	nextAttemptAt map[string]time.Time,
	now time.Time,
) []datastore.GeneratorRotationInfo {
	var due []datastore.GeneratorRotationInfo
	for _, info := range infos {
		if _, running := inFlight[info.GeneratorID]; running {
			continue
		}
		if retryAt, backing := nextAttemptAt[info.GeneratorID]; backing && now.Before(retryAt) {
			continue
		}
		if !rotationIsDue(info, now) {
			continue
		}
		due = append(due, info)
	}
	return due
}

// rotationAnchor renders the instant a generator's cadence is measured from,
// for the log line that explains a rotation.
func rotationAnchor(info datastore.GeneratorRotationInfo) string {
	if info.LastRotationAt.IsZero() {
		return "never"
	}
	return info.LastRotationAt.UTC().Format(time.RFC3339)
}

// rotationBackoff is the delay before the next attempt for a generator that
// has failed this many times in a row.
//
// It doubles from rotationRetryBaseDelay and saturates at one cadence, so the
// retry rate never falls below the rate the credential is supposed to rotate
// at, and never exceeds it either. Zero attempts means no backoff.
func rotationBackoff(attempts int, interval time.Duration) time.Duration {
	if attempts < 1 {
		return 0
	}
	if attempts >= maxRotationRetryAttempts {
		if interval > 0 {
			return interval
		}
		return rotationRetryBaseDelay << (maxRotationRetryAttempts - 1)
	}
	delay := rotationRetryBaseDelay << (attempts - 1)
	if interval > 0 && delay > interval {
		return interval
	}
	return delay
}

// rotationResult holds the prepared rotation command and changeset, ready for
// persistence and execution.
type rotationResult struct {
	command   *forma_command.FormaCommand
	changeset changeset.Changeset
}

// findRotationConsumers walks outward from a generator's destinations to the
// resources that consume them by reference, transitively.
//
// A rotation moves the value a destination holds. Anything reading that value
// through a reference goes on holding the previous one until it is written
// again, and delivery reaches nodes in the changeset and nothing else, so a
// consumer absent from the changeset is a consumer that never follows. The
// database role whose password references a rotating secret is the shape
// production uses: after a rotation the secret carries the new credential and
// the engine still accepts only the old one until the role is written.
//
// Destinations are found by their binding to the generator; a consumer is one
// hop further out and holds a reference to the destination rather than to the
// generator, which is why the destination query cannot see it. The walk is
// transitive because a consumer may itself be referenced.
//
// Unmanaged rows are skipped and not walked through: they carry no declarations
// to re-resolve, exactly as the delete cascade treats them. Results are sorted
// so a rotation plans the same nodes in the same order every time.
func findRotationConsumers(ds datastore.Datastore, destinations []*pkgmodel.Resource) ([]*pkgmodel.Resource, error) {
	seen := make(map[string]bool, len(destinations))
	level := make([]string, 0, len(destinations))
	for _, destination := range destinations {
		if destination.Ksuid == "" {
			continue
		}
		seen[destination.Ksuid] = true
		level = append(level, destination.Ksuid)
	}

	var consumers []*pkgmodel.Resource
	for len(level) > 0 {
		dependents, err := ds.FindResourcesDependingOnMany(level)
		if err != nil {
			return nil, fmt.Errorf("failed to find the consumers of a rotation destination: %w", err)
		}
		var next []string
		for _, group := range dependents {
			for _, dependent := range group {
				if dependent == nil || dependent.Ksuid == "" || seen[dependent.Ksuid] {
					continue
				}
				seen[dependent.Ksuid] = true
				if dependent.Stack == constants.UnmanagedStack {
					continue
				}
				consumers = append(consumers, dependent)
				next = append(next, dependent.Ksuid)
			}
		}
		level = next
	}

	sort.Slice(consumers, func(i, j int) bool { return consumers[i].Ksuid < consumers[j].Ksuid })
	return consumers, nil
}

// prepareRotation builds the apply that moves one generator's generation
// forward. It returns nil (with no error) when there is nothing to rotate.
//
// The command's resource updates are its destinations, planned by the
// co-planning pass the apply path already uses for the same obligation: once a
// generator draws, every live destination of it has to be a node in the
// changeset, because delivery reaches nodes and nothing else and formae keeps
// only a hash of a drawn value. Everything downstream — the draw op, the
// delivery to each destination, the generation stamp — is the ordinary apply
// path.
//
// The forma is built from the destinations the datastore records as bound to
// this generator, rather than from a stack snapshot the way a reconcile is:
// rotation is about one credential, and its destinations may sit in several
// stacks.
func prepareRotation(ds datastore.Datastore, info datastore.GeneratorRotationInfo) (*rotationResult, error) {
	generator, err := admitRotation(ds, info)
	if err != nil {
		return nil, err
	}
	if generator == nil {
		return nil, nil
	}

	destinations, err := ds.FindResourcesReferencingGenerator(info.GeneratorID)
	if err != nil {
		return nil, fmt.Errorf("failed to find the destinations of generator %s: %w", info.GeneratorID, err)
	}
	if len(destinations) == 0 {
		// A generator nothing binds has no credential in place to rotate.
		// Drawing would advance its generation and write nowhere.
		slog.Debug("Skipping rotation: the generator has no destination",
			"generator", info.Label, "stack", info.StackLabel)
		return nil, nil
	}

	consumers, err := findRotationConsumers(ds, destinations)
	if err != nil {
		return nil, err
	}
	planned := append(append([]*pkgmodel.Resource{}, destinations...), consumers...)

	forma := pkgmodel.FormaFromResources(planned)

	existingTargets, err := ds.LoadAllTargets()
	if err != nil {
		return nil, fmt.Errorf("failed to load targets: %w", err)
	}
	// FormaFromResources only names the targets; substitute the real ones so
	// the co-planning pass has the config it needs.
	existingTargetMap := make(map[string]*pkgmodel.Target, len(existingTargets))
	for _, target := range existingTargets {
		existingTargetMap[target.Label] = target
	}
	for i, formaTarget := range forma.Targets {
		if existing, ok := existingTargetMap[formaTarget.Label]; ok {
			forma.Targets[i] = *existing
		}
	}

	coPlanKsuids := make(map[string]bool, len(planned))
	for _, resource := range planned {
		if resource.Ksuid != "" {
			coPlanKsuids[resource.Ksuid] = true
		}
	}

	// force stays false. A forced apply deliberately does NOT touch a
	// generator binding (see ClassifyOccurrence), so forcing here would
	// suppress the very rotation this command exists to perform. Drift is
	// handled by the refusal below, not by forcing over it.
	resourceUpdates, err := resource_update.CoPlanGeneratorDestinations(
		forma, coPlanKsuids, pkgmodel.FormaApplyModeReconcile,
		resource_update.FormaCommandSourceGeneratorRotation, existingTargets, ds, false)
	if err != nil {
		return nil, fmt.Errorf("failed to plan the destinations of generator %s: %w", info.GeneratorID, err)
	}
	if len(resourceUpdates) == 0 {
		slog.Debug("Skipping rotation: no destination of the generator could be planned",
			"generator", info.Label, "stack", info.StackLabel)
		return nil, nil
	}

	draws := []generator_update.GeneratorUpdate{
		generator_update.NewDrawGeneratorUpdate(generator, info.StackLabel),
	}

	rotationCommand := forma_command.NewFormaCommand(
		forma,
		&config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		},
		pkgmodel.CommandApply,
		resourceUpdates,
		nil, // No target updates
		nil, // No stack updates
		nil, // No policy updates
		nil, // No generator row writes: the spec is unchanged, only its generation moves
		rotationClientID,
		"",
		"",
		forma_command.SourceGeneratorRotator,
	)
	rotationCommand.DrawGeneratorUpdates = draws

	if err := refuseRotationOnDrift(ds, forma, rotationCommand); err != nil {
		return nil, err
	}

	synth, err := target_update.SynthesizeResolveTargetUpdates(
		resource_update.ReferencedTargetLabels(resourceUpdates),
		resource_update.SourceTargetByKsuid(resourceUpdates),
		nil, ds)
	if err != nil {
		return nil, fmt.Errorf("failed to create changeset: %w", err)
	}
	cs, err := changeset.NewChangeset(resourceUpdates, synth, draws,
		rotationCommand.ID, pkgmodel.CommandApply, rotationCommand.Config.Mode)
	if err != nil {
		return nil, fmt.Errorf("failed to create changeset: %w", err)
	}

	return &rotationResult{command: rotationCommand, changeset: cs}, nil
}

// admitRotation re-reads the generator the sweep decided to rotate and returns
// it stamped with the identity and stack the draw is filed under, or nil when
// the rotation must not proceed.
//
// This is the admission check, and it is where a user apply landing between the
// sweep's read and the submission is caught. The sweep's view is a snapshot: by
// the time the command is built the generator may have been deleted, renamed,
// had its cadence removed, or had its spec edited. Planning against the
// snapshot would rotate a credential under a spec the user has already
// replaced, or rotate one they have just detached. Re-reading here means the
// worst case is a skipped sweep, and the next one acts on what is actually
// declared.
//
// The generator returned is the CURRENT one, so an edited spec is what the
// value is drawn under.
func admitRotation(ds datastore.Datastore, info datastore.GeneratorRotationInfo) (pkgmodel.Generator, error) {
	identity, err := ds.GetGeneratorIdentity(info.Label, info.StackLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to re-read the identity of generator %q in stack %q: %w",
			info.Label, info.StackLabel, err)
	}
	if identity.ID != info.GeneratorID {
		// Either the generator is gone (zero identity) or something else
		// holds its label now. Neither is the generator the sweep read.
		slog.Debug("Skipping rotation: the generator moved between the sweep and admission",
			"generator", info.Label, "stack", info.StackLabel,
			"expected", info.GeneratorID, "found", identity.ID)
		return nil, nil
	}

	generator, err := ds.GetGenerator(info.Label, info.StackLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to re-read generator %q in stack %q: %w",
			info.Label, info.StackLabel, err)
	}
	if generator == nil {
		return nil, nil
	}

	rotation := generator.GetRotation()
	if rotation == nil || rotation.EverySeconds <= 0 {
		slog.Debug("Skipping rotation: the generator no longer declares a cadence",
			"generator", info.Label, "stack", info.StackLabel)
		return nil, nil
	}
	// The cadence itself may have been widened since the sweep read it, in
	// which case the generator is no longer due and must not be rotated on
	// the old interval.
	current := info
	current.IntervalSeconds = rotation.EverySeconds
	if !rotationIsDue(current, time.Now().UTC()) {
		slog.Debug("Skipping rotation: the cadence was widened and the generator is no longer due",
			"generator", info.Label, "stack", info.StackLabel, "everySeconds", rotation.EverySeconds)
		return nil, nil
	}

	// A loaded generator carries neither its KSUID (PasswordGenerator.ID is
	// json:"-") nor reliably its stack, and the draw records the generation
	// against both.
	generator.SetID(info.GeneratorID)
	generator.SetStack(info.StackLabel)
	return generator, nil
}

// refuseRotationOnDrift refuses a rotation whose destinations, or anything
// beside them in their stacks, has moved out of band.
//
// A rotation is an ordinary update, so it confronts drift like one: writing a
// fresh credential over a resource somebody changed outside formae would
// silently discard that change. The gate is the same pair of readings a soft
// reconcile uses — filterUnabsorbedModifications for declared movement and
// witnessedMovedModifications for movement on content formae's own write
// witnessed — so a drifted secret and a drifted consumer of that secret are
// both refusals, and neither is a special case here.
//
// A stack carrying an auto-reconcile policy is exempt. That policy IS the
// operator's standing instruction to overwrite out-of-band change on the
// stack, so there is nothing left for rotation to ask about and it proceeds.
// The exemption is per stack, because a generator's destinations may sit in
// several and only the stack holding the drift has opted in.
func refuseRotationOnDrift(ds datastore.Datastore, forma *pkgmodel.Forma, fc *forma_command.FormaCommand) error {
	autoReconciled, err := stacksWithAutoReconcilePolicy(ds)
	if err != nil {
		return err
	}

	stackLabels := map[string]bool{}
	for _, label := range append(drift.StackLabelsFromForma(forma), fc.GetStackLabels()...) {
		if label == "" || autoReconciled[label] {
			continue
		}
		stackLabels[label] = true
	}

	// The same drift window the apply path loads, through the same function:
	// a second reading of it here would eventually classify differently from
	// the one a user's reconcile confronts.
	modificationsByStack := make(map[string][]datastore.ResourceModification)
	witnessByKsuid := make(map[string]json.RawMessage)
	for label := range stackLabels {
		if err := drift.LoadModificationsAndWitnesses(ds, label, modificationsByStack, witnessByKsuid); err != nil {
			return err
		}
	}

	modifiedStacks := map[string]apimodel.ModifiedStack{}
	for label, modifications := range modificationsByStack {
		unabsorbed := drift.FilterUnabsorbedModifications(modifications, forma, fc)
		unabsorbed = append(unabsorbed, drift.WitnessedMovedModifications(modifications, witnessByKsuid, forma, fc)...)
		if len(unabsorbed) == 0 {
			continue
		}
		modifiedResources := make([]apimodel.ResourceModification, 0, len(unabsorbed))
		for _, modification := range unabsorbed {
			modifiedResources = append(modifiedResources, drift.ToAPIResourceModification(modification))
		}
		modifiedStacks[label] = apimodel.ModifiedStack{ModifiedResources: modifiedResources}
	}
	if len(modifiedStacks) > 0 {
		return apimodel.FormaReconcileRejectedError{ModifiedStacks: modifiedStacks}
	}

	return nil
}

// stacksWithAutoReconcilePolicy indexes the stacks whose auto-reconcile policy
// stands as an opt-in to overwriting out-of-band change.
func stacksWithAutoReconcilePolicy(ds datastore.Datastore) (map[string]bool, error) {
	policies, err := ds.GetStacksWithAutoReconcilePolicy()
	if err != nil {
		return nil, fmt.Errorf("failed to read the auto-reconcile policies: %w", err)
	}
	labels := make(map[string]bool, len(policies))
	for _, policy := range policies {
		labels[policy.StackLabel] = true
	}
	return labels, nil
}
