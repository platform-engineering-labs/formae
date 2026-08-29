// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package generator_update

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// GeneratorDatastore defines the datastore operations needed for generator
// updates. LoadGeneratorsByStack resolves the stack label to its KSUID
// internally, so — unlike the policy generator, which needs a separate
// GetStackByLabel to get from a label to the ID that GetInlinePoliciesForStack
// wants — a single method is enough here.
type GeneratorDatastore interface {
	// LoadGeneratorsByStack returns the non-deleted generators owned by the
	// named stack. A stack the datastore does not know yet has none.
	LoadGeneratorsByStack(stackLabel string) ([]pkgmodel.Generator, error)
}

// GeneratorUpdateGenerator generates generator updates by comparing desired
// state (a Forma's declared generators) with existing state (what the
// datastore has stored for each generator's stack).
type GeneratorUpdateGenerator struct {
	datastore GeneratorDatastore
}

// NewGeneratorUpdateGenerator creates a new generator update generator.
func NewGeneratorUpdateGenerator(ds GeneratorDatastore) *GeneratorUpdateGenerator {
	return &GeneratorUpdateGenerator{datastore: ds}
}

// GenerateGeneratorUpdates determines what generator changes are needed.
func (gg *GeneratorUpdateGenerator) GenerateGeneratorUpdates(forma *pkgmodel.Forma, command pkgmodel.Command, mode pkgmodel.FormaApplyMode) ([]GeneratorUpdate, error) {
	// A generator has no standalone form: it is always owned by exactly one
	// stack, and it is deleted implicitly when that stack is destroyed —
	// mirroring how an inline policy is handled on Destroy, and unlike a
	// standalone policy, there is nothing left for a destroy command to do
	// here.
	if command == pkgmodel.CommandDestroy {
		return nil, nil
	}

	declared, err := pkgmodel.ParseGenerators(forma.Generators)
	if err != nil {
		return nil, fmt.Errorf("failed to parse generators: %w", err)
	}

	// Group declared generators by the stack they belong to (their own Stack
	// field — generators are never nested inside a stack block the way
	// policies are). stackOrder preserves first-encounter order so output is
	// deterministic.
	byStack := make(map[string][]pkgmodel.Generator)
	var stackOrder []string
	for _, gen := range declared {
		stackLabel := gen.GetStack()
		if _, ok := byStack[stackLabel]; !ok {
			stackOrder = append(stackOrder, stackLabel)
		}
		byStack[stackLabel] = append(byStack[stackLabel], gen)
	}

	exact := mode == pkgmodel.FormaApplyModeReconcile
	now := util.TimeNow()

	var updates []GeneratorUpdate
	processed := make(map[string]bool, len(forma.Stacks))

	// Visit every declared stack first, so a stack that used to own
	// generators but no longer declares any is still considered for the
	// reconcile delete pass below.
	for _, stack := range forma.Stacks {
		stackUpdates, err := gg.generateStackGeneratorUpdates(stack.Label, byStack[stack.Label], exact, now)
		if err != nil {
			return nil, fmt.Errorf("failed to generate generator updates for stack %s: %w", stack.Label, err)
		}
		updates = append(updates, stackUpdates...)
		processed[stack.Label] = true
	}

	// A generator can name a stack the forma doesn't otherwise declare (e.g.
	// an existing stack with no resource changes this apply); still process
	// its declared generators.
	for _, stackLabel := range stackOrder {
		if processed[stackLabel] {
			continue
		}
		stackUpdates, err := gg.generateStackGeneratorUpdates(stackLabel, byStack[stackLabel], exact, now)
		if err != nil {
			return nil, fmt.Errorf("failed to generate generator updates for stack %s: %w", stackLabel, err)
		}
		updates = append(updates, stackUpdates...)
	}

	return updates, nil
}

// generateStackGeneratorUpdates diffs one stack's declared generators against
// what is stored for it. Matching is by label; a declared generator whose
// label doesn't match a stored one falls back to matching by its Alias (the
// previous label), so a rename updates the existing row in place instead of
// deleting it and creating a fresh one. See PasswordGenerator.GetAlias.
func (gg *GeneratorUpdateGenerator) generateStackGeneratorUpdates(stackLabel string, declared []pkgmodel.Generator, exact bool, now time.Time) ([]GeneratorUpdate, error) {
	if len(declared) == 0 && !exact {
		return nil, nil
	}

	existing, err := gg.existingGenerators(stackLabel)
	if err != nil {
		return nil, err
	}

	existingByLabel := make(map[string]pkgmodel.Generator, len(existing))
	for _, e := range existing {
		existingByLabel[e.GetLabel()] = e
	}

	var updates []GeneratorUpdate
	matchedLabels := make(map[string]bool, len(declared))

	for _, gen := range declared {
		label := gen.GetLabel()

		match, found := existingByLabel[label]
		if !found && gen.GetAlias() != "" {
			match, found = existingByLabel[gen.GetAlias()]
		}

		if !found {
			updates = append(updates, GeneratorUpdate{
				Generator:  gen,
				Operation:  GeneratorOperationCreate,
				State:      GeneratorUpdateStateNotStarted,
				StackLabel: stackLabel,
				StartTs:    now,
				ModifiedTs: now,
			})
			slog.Debug("Generated generator create",
				"label", label, "stack", stackLabel)
			continue
		}

		matchedLabels[match.GetLabel()] = true

		// A match via alias always changed (the label moved); a match by
		// current label only changed if the spec itself differs.
		if match.GetLabel() == label && generatorsEqual(match, gen) {
			continue
		}

		updates = append(updates, GeneratorUpdate{
			Generator:         gen,
			ExistingGenerator: match,
			Operation:         GeneratorOperationUpdate,
			State:             GeneratorUpdateStateNotStarted,
			StackLabel:        stackLabel,
			StartTs:           now,
			ModifiedTs:        now,
		})
		slog.Debug("Generated generator update",
			"label", label, "stack", stackLabel, "renamedFrom", match.GetLabel())
	}

	if !exact {
		return updates, nil
	}

	for _, e := range existing {
		if matchedLabels[e.GetLabel()] {
			continue
		}
		updates = append(updates, GeneratorUpdate{
			Generator:  e,
			Operation:  GeneratorOperationDelete,
			State:      GeneratorUpdateStateNotStarted,
			StackLabel: stackLabel,
			StartTs:    now,
			ModifiedTs: now,
		})
		slog.Debug("Generated generator delete",
			"label", e.GetLabel(), "stack", stackLabel)
	}

	return updates, nil
}

// existingGenerators returns the generators the stack owns. A nil datastore
// (unit tests exercising the diff without persistence) means nothing is
// stored yet.
func (gg *GeneratorUpdateGenerator) existingGenerators(stackLabel string) ([]pkgmodel.Generator, error) {
	if gg.datastore == nil {
		return nil, nil
	}

	existing, err := gg.datastore.LoadGeneratorsByStack(stackLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to load generators for stack: %w", err)
	}

	return existing, nil
}

// generatorsEqual compares two generators' generation specs for equality.
// Label, Stack/StackID and Alias are identity and relocation fields, not
// spec, and are deliberately excluded — an update is generated for a rename
// via the caller's label comparison, not from here.
func generatorsEqual(a, b pkgmodel.Generator) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.GetType() != b.GetType() {
		return false
	}

	switch pa := a.(type) {
	case *pkgmodel.PasswordGenerator:
		pb, ok := b.(*pkgmodel.PasswordGenerator)
		if !ok {
			return false
		}
		return pa.Length == pb.Length &&
			pa.Uppercase == pb.Uppercase &&
			pa.Lowercase == pb.Lowercase &&
			pa.Digits == pb.Digits &&
			pa.Symbols == pb.Symbols &&
			pa.ExcludeCharacters == pb.ExcludeCharacters &&
			pa.RequireEachIncludedType == pb.RequireEachIncludedType
	default:
		// For unknown types, assume not equal to be safe.
		return false
	}
}
