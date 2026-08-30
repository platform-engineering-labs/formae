// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package generator_update

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// The compile-time assertion that *GeneratorUpdate satisfies changeset.Update
// lives in internal/metastructure/changeset, not here: the changeset package
// imports generator_update transitively (via forma_persister/forma_command),
// so this package importing changeset back would be an import cycle. See
// changeset/changeset_test.go (the assertion) and
// changeset/generator_update_node_uri_test.go (a matching collision test
// built from the real, in-package URI constructors on both sides).

func newTestGeneratorUpdate(stack, label string, op GeneratorOperation, state GeneratorUpdateState) *GeneratorUpdate {
	return &GeneratorUpdate{
		Generator:  &pkgmodel.PasswordGenerator{Label: label},
		Operation:  op,
		State:      state,
		StackLabel: stack,
	}
}

func TestGeneratorUpdateNodeURI(t *testing.T) {
	gu := newTestGeneratorUpdate("prod", "db-password", GeneratorOperationCreate, GeneratorUpdateStateNotStarted)

	assert.Equal(t, pkgmodel.FormaeURI("generator://prod/db-password/create"), gu.NodeURI())
}

func TestGeneratorUpdateNodeURIVariesByOperationAndIdentity(t *testing.T) {
	create := newTestGeneratorUpdate("prod", "db-password", GeneratorOperationCreate, GeneratorUpdateStateNotStarted)
	update := newTestGeneratorUpdate("prod", "db-password", GeneratorOperationUpdate, GeneratorUpdateStateNotStarted)
	otherStack := newTestGeneratorUpdate("staging", "db-password", GeneratorOperationCreate, GeneratorUpdateStateNotStarted)
	otherLabel := newTestGeneratorUpdate("prod", "api-key", GeneratorOperationCreate, GeneratorUpdateStateNotStarted)

	uris := []pkgmodel.FormaeURI{create.NodeURI(), update.NodeURI(), otherStack.NodeURI(), otherLabel.NodeURI()}
	seen := make(map[pkgmodel.FormaeURI]bool)
	for _, u := range uris {
		assert.False(t, seen[u], "duplicate NodeURI %q", u)
		seen[u] = true
	}
}

// A StackLabel and a generator label are both free-form strings that may
// themselves contain "/" (nothing validates against it). Naively joining
// "<stack>/<label>/<operation>" with "/" would let two distinct (stack,
// label) pairs collapse onto the same NodeURI — StackLabel="s", label="a/b"
// and StackLabel="s/a", label="b" both naively join to the same
// "s/a/b/create". NodeURI must percent-encode each segment so these two
// genuinely different generators never collide in ExecutionDAG.Nodes.
func TestGeneratorUpdateNodeURIDoesNotCollideAcrossStackAndLabelBoundary(t *testing.T) {
	slashInLabel := newTestGeneratorUpdate("s", "a/b", GeneratorOperationCreate, GeneratorUpdateStateNotStarted)
	slashInStack := newTestGeneratorUpdate("s/a", "b", GeneratorOperationCreate, GeneratorUpdateStateNotStarted)

	assert.NotEqual(t, slashInLabel.NodeURI(), slashInStack.NodeURI())
}

// resourceOperationURI replicates changeset.createOperationURI's (unexported)
// format exactly: "<ksuid>/<propertyPath>/<operation>", with no scheme. It
// exists here only to construct a resource-shaped URI for the collision test
// below, without reaching into the changeset package's internals.
func resourceOperationURI(ksuid, propertyPath string, operation types.OperationType) pkgmodel.FormaeURI {
	base := pkgmodel.NewFormaeURI(ksuid, propertyPath)
	return pkgmodel.FormaeURI(fmt.Sprintf("%s/%s/%s", base.KSUID(), base.PropertyPath(), operation))
}

// targetOperationURI replicates target_update.TargetUpdate.NodeURI's format:
// "target://<label>/<operation>".
func targetOperationURI(label string, operation types.OperationType) pkgmodel.FormaeURI {
	return pkgmodel.FormaeURI("target://" + label + "/" + string(operation))
}

// A generator node URI must never collide with a resource or target node
// URI, since all three share the same ExecutionDAG.Nodes keyspace.
//
// A resource operation URI never contains "://" at all: a KSUID is 27
// base62 characters, a property path is dot/slash-joined JSON field names,
// and an OperationType is one of a fixed set of lowercase words (create,
// update, delete, read, replace, resolve, reaped) — none of those character
// sets can produce the substring "://". A generator URI and a target URI
// both start with a literal "<scheme>://" prefix, so by that presence/
// absence alone a generator URI can never equal a resource URI. A generator
// URI and a target URI carry different literal scheme prefixes
// ("generator://" vs "target://"), so they can never be equal to each other
// either. This test verifies both properties by construction, across a
// spread of realistic and adversarial inputs (empty property paths, a
// generator label that happens to spell "target" or the scheme itself).
func TestGeneratorNodeURICannotCollideWithResourceOrTargetURI(t *testing.T) {
	ksuids := []string{
		"0ujsswThIGTUYm2K8FjOOfXtY1K", // a realistic 27-char KSUID
		"generator",                   // adversarial: spells the other scheme's word
		"target",
	}
	propertyPaths := []string{"", "spec/length", "a/b/c"}
	resourceOps := []types.OperationType{
		types.OperationCreate, types.OperationUpdate, types.OperationDelete,
		types.OperationRead, types.OperationReplace, types.OperationReaped,
	}

	labels := []string{"db-password", "target", "generator", ""}
	stacks := []string{"prod", "staging", "target", "generator"}
	generatorOps := []GeneratorOperation{GeneratorOperationCreate, GeneratorOperationUpdate, GeneratorOperationDelete}

	var generatorURIs []pkgmodel.FormaeURI
	for _, stack := range stacks {
		for _, label := range labels {
			for _, op := range generatorOps {
				gu := newTestGeneratorUpdate(stack, label, op, GeneratorUpdateStateNotStarted)
				generatorURIs = append(generatorURIs, gu.NodeURI())
			}
		}
	}

	var resourceURIs []pkgmodel.FormaeURI
	for _, ksuid := range ksuids {
		for _, path := range propertyPaths {
			for _, op := range resourceOps {
				resourceURIs = append(resourceURIs, resourceOperationURI(ksuid, path, op))
			}
		}
	}

	var targetURIs []pkgmodel.FormaeURI
	for _, label := range labels {
		for _, op := range resourceOps {
			targetURIs = append(targetURIs, targetOperationURI(label, op))
		}
	}

	for _, gURI := range generatorURIs {
		assert.True(t, len(gURI) >= 12 && string(gURI)[:12] == "generator://",
			"generator NodeURI %q must start with the generator:// scheme", gURI)

		for _, rURI := range resourceURIs {
			assert.NotEqual(t, rURI, gURI, "generator URI collided with resource URI")
		}
		for _, tURI := range targetURIs {
			assert.NotEqual(t, tURI, gURI, "generator URI collided with target URI")
		}
	}

	// And the reverse invariant that the argument above rests on: no
	// resource-shaped URI ever contains "://" at all.
	for _, rURI := range resourceURIs {
		assert.NotContains(t, string(rURI), "://")
	}
}

func TestGeneratorUpdateResolvablesIsNil(t *testing.T) {
	gu := newTestGeneratorUpdate("prod", "db-password", GeneratorOperationCreate, GeneratorUpdateStateNotStarted)

	assert.Nil(t, gu.Resolvables())
}

func TestGeneratorUpdateIsRateLimitedFalse(t *testing.T) {
	gu := newTestGeneratorUpdate("prod", "db-password", GeneratorOperationCreate, GeneratorUpdateStateNotStarted)

	assert.False(t, gu.IsRateLimited())
}

// Namespace's value never gates concurrency while IsRateLimited is false
// (see the doc comment on Namespace), but the contract is still a non-empty,
// stable pseudo-namespace distinct from any provider's — pin it so a stub
// returning "" cannot pass silently.
func TestGeneratorUpdateNamespace(t *testing.T) {
	gu := newTestGeneratorUpdate("prod", "db-password", GeneratorOperationCreate, GeneratorUpdateStateNotStarted)

	assert.Equal(t, "generator", gu.Namespace())
}

// The state predicates must partition GeneratorUpdateState exactly: every
// defined state satisfies exactly one of IsReady/IsRunning/IsSuccess/
// IsFailed, with no state satisfying zero or more than one.
func TestGeneratorUpdateStatePredicatesPartitionState(t *testing.T) {
	allStates := []GeneratorUpdateState{
		GeneratorUpdateStateNotStarted,
		GeneratorUpdateStateInProgress,
		GeneratorUpdateStateSuccess,
		GeneratorUpdateStateFailed,
	}

	for _, state := range allStates {
		t.Run(string(state), func(t *testing.T) {
			gu := newTestGeneratorUpdate("prod", "db-password", GeneratorOperationCreate, state)

			predicates := map[string]bool{
				"IsReady":   gu.IsReady(),
				"IsRunning": gu.IsRunning(),
				"IsSuccess": gu.IsSuccess(),
				"IsFailed":  gu.IsFailed(),
			}

			trueCount := 0
			for _, v := range predicates {
				if v {
					trueCount++
				}
			}
			assert.Equal(t, 1, trueCount, "state %s satisfied %d predicates, want exactly 1: %+v", state, trueCount, predicates)
		})
	}

	assert.True(t, newTestGeneratorUpdate("prod", "l", GeneratorOperationCreate, GeneratorUpdateStateNotStarted).IsReady())
	assert.True(t, newTestGeneratorUpdate("prod", "l", GeneratorOperationCreate, GeneratorUpdateStateInProgress).IsRunning())
	assert.True(t, newTestGeneratorUpdate("prod", "l", GeneratorOperationCreate, GeneratorUpdateStateSuccess).IsSuccess())
	assert.True(t, newTestGeneratorUpdate("prod", "l", GeneratorOperationCreate, GeneratorUpdateStateFailed).IsFailed())
}

func TestGeneratorUpdateMarkInProgressAndMarkFailed(t *testing.T) {
	gu := newTestGeneratorUpdate("prod", "db-password", GeneratorOperationCreate, GeneratorUpdateStateNotStarted)

	gu.MarkInProgress()
	assert.Equal(t, GeneratorUpdateStateInProgress, gu.State)
	assert.True(t, gu.IsRunning())

	gu.MarkFailed()
	assert.Equal(t, GeneratorUpdateStateFailed, gu.State)
	assert.True(t, gu.IsFailed())
}
