// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_local

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isProcessAlive returns true if the process registered under name is alive on the node.
func isProcessAlive(node gen.Node, name gen.Atom) bool {
	pid, err := node.ProcessPID(name)
	if err != nil {
		return false
	}
	state, err := node.ProcessState(pid)
	if err != nil {
		return false
	}
	return state != gen.ProcessStateTerminated && state != gen.ProcessStateZombee
}

// killProcessByName forcefully kills the process registered under name.
func killProcessByName(node gen.Node, name gen.Atom) error {
	pid, err := node.ProcessPID(name)
	if err != nil {
		return err
	}
	return node.Kill(pid)
}

// formaWithOneResource builds a minimal Forma with a single resource in a named stack.
func formaWithOneResource(stack, label string) *pkgmodel.Forma {
	return &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{
			{Label: stack},
		},
		Resources: []pkgmodel.Resource{
			{
				Label: label,
				Type:  "FakeAWS::EC2::VPC",
				Properties: json.RawMessage(`{
					"CidrBlock": "10.0.0.0/16"
				}`),
				Stack:   stack,
				Target:  "test-target",
				Managed: true,
			},
		},
		Targets: []pkgmodel.Target{
			{Label: "test-target"},
		},
	}
}

// TestExecutorTerminationCascadesToResourceUpdaters verifies two properties of the
// ChangesetExecutor → ResourceUpdater supervision link:
//
//  1. Cascade (parent → child): terminating the ChangesetExecutor causes its
//     ResourceUpdater children to terminate within a few seconds.
//
//  2. Unidirectional (child → parent): killing a ResourceUpdater directly does NOT
//     terminate the ChangesetExecutor.
func TestExecutorTerminationCascadesToResourceUpdaters(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Plugin returns InProgress forever so ResourceUpdaters stay alive indefinitely.
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusInProgress,
						RequestID:       "request-" + req.Label,
						NativeID:        "native-" + req.Label,
					},
				}, nil
			},
			Status: func(req *resource.StatusRequest) (*resource.StatusResult, error) {
				return &resource.StatusResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusInProgress,
						RequestID:       req.RequestID,
					},
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		// ── Assertion B (unidirectional, child → parent): kill RU, executor must stay. ──
		// Stack-b runs independently of stack-a so there are no conflicting commands.
		respB, err := m.ApplyForma(
			formaWithOneResource("stack-b", "b-vpc"),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			"test-client",
		)
		require.NoError(t, err)
		commandIDB := respB.CommandID

		var ruNameB gen.Atom
		assert.Eventually(t, func() bool {
			commands, err := m.Datastore.LoadFormaCommands()
			if err != nil {
				return false
			}
			for _, cmd := range commands {
				if cmd.ID != commandIDB {
					continue
				}
				for _, ru := range cmd.ResourceUpdates {
					if ru.State == resource_update.ResourceUpdateStateInProgress {
						ruNameB = actornames.ResourceUpdater(
							ru.DesiredState.URI(), string(ru.Operation), commandIDB)
						return isProcessAlive(m.Node, ruNameB)
					}
				}
			}
			return false
		}, 10*time.Second, 100*time.Millisecond, "stack-B ResourceUpdater should become alive")
		t.Logf("Assertion B: ResourceUpdater=%s", ruNameB)

		ceuxNameB := actornames.ChangesetExecutor(commandIDB)
		require.True(t, isProcessAlive(m.Node, ceuxNameB), "stack-B ChangesetExecutor must be alive before RU kill")

		// Kill the ResourceUpdater directly.
		require.NoError(t, killProcessByName(m.Node, ruNameB))

		// Give the executor a window to react — it must NOT die.
		time.Sleep(500 * time.Millisecond)
		assert.True(t, isProcessAlive(m.Node, ceuxNameB),
			"ChangesetExecutor must stay alive after its ResourceUpdater was killed (unidirectional link)")

		// ── Assertion A (cascade, parent → child): kill executor, RU must die. ──
		// Stack-a is independent so there is no conflicting command with stack-b.
		respA, err := m.ApplyForma(
			formaWithOneResource("stack-a", "a-vpc"),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			"test-client",
		)
		require.NoError(t, err)
		commandIDA := respA.CommandID

		var ruNameA gen.Atom
		assert.Eventually(t, func() bool {
			commands, err := m.Datastore.LoadFormaCommands()
			if err != nil {
				return false
			}
			for _, cmd := range commands {
				if cmd.ID != commandIDA {
					continue
				}
				for _, ru := range cmd.ResourceUpdates {
					if ru.State == resource_update.ResourceUpdateStateInProgress {
						ruNameA = actornames.ResourceUpdater(
							ru.DesiredState.URI(), string(ru.Operation), commandIDA)
						return isProcessAlive(m.Node, ruNameA)
					}
				}
			}
			return false
		}, 10*time.Second, 100*time.Millisecond, "stack-A ResourceUpdater should become alive")
		t.Logf("Assertion A: ResourceUpdater=%s", ruNameA)

		ceuxNameA := actornames.ChangesetExecutor(commandIDA)
		require.True(t, isProcessAlive(m.Node, ceuxNameA), "stack-A ChangesetExecutor must be alive before executor kill")

		// Kill the ChangesetExecutor.
		require.NoError(t, killProcessByName(m.Node, ceuxNameA))

		// The ResourceUpdater must cascade-terminate within a few seconds via LinkParent.
		assert.Eventually(t, func() bool {
			return !isProcessAlive(m.Node, ruNameA)
		}, 5*time.Second, 100*time.Millisecond,
			"ResourceUpdater must terminate after its parent ChangesetExecutor was killed (LinkParent cascade)")
	})
}

// TestExecutorTerminationCascadesToTargetUpdaters verifies two properties of the
// ChangesetExecutor → TargetUpdater supervision link:
//
//  1. Cascade (parent → child): terminating the ChangesetExecutor causes its
//     TargetUpdater children to terminate within a few seconds.
//
//  2. Unidirectional (child → parent): killing a TargetUpdater directly does NOT
//     terminate the ChangesetExecutor.
//
// To keep the TargetUpdater observably in-flight, the target config contains a $ref
// to a pre-seeded resource. The plugin's Read override blocks indefinitely, so the
// ResolveCache never responds and the TargetUpdater stays in StateResolving.
func TestExecutorTerminationCascadesToTargetUpdaters(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// deterministic ksuid for the pre-seeded cluster resource.
		const clusterKsuid = "2MiD2rA1SJbLMGZgTL0hCxjkjjr"

		// Block channel: Read blocks until the test is done, keeping the
		// TargetUpdater in StateResolving so we can observe the cascade.
		blockRead := make(chan struct{})
		defer close(blockRead)

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          "req-" + req.Label,
					NativeID:           "native-" + req.Label,
					ResourceProperties: json.RawMessage(`{"CidrBlock":"10.0.0.0/16"}`),
				}}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				// Block until the test channel is closed so the ResolveCache
				// never returns and the TargetUpdater stays in StateResolving.
				<-blockRead
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   `{"CidrBlock":"10.0.0.0/16"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		// ── Step 1: pre-seed the cluster resource so the ResolveCache can load it. ──
		seedForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "infra"}},
			Resources: []pkgmodel.Resource{{
				Label:      "cluster",
				Type:       "FakeAWS::EC2::VPC",
				Stack:      "infra",
				Target:     "provider",
				Managed:    true,
				Ksuid:      clusterKsuid,
				Properties: json.RawMessage(`{"CidrBlock":"10.0.0.0/16"}`),
			}},
			Targets: []pkgmodel.Target{
				{Label: "provider", Namespace: "FakeAWS"},
			},
		}

		seedResp, err := m.ApplyForma(seedForma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			"test-client")
		require.NoError(t, err)

		// Wait for the seed command to complete successfully.
		assert.Eventually(t, func() bool {
			cmds, _ := m.Datastore.LoadFormaCommands()
			for _, cmd := range cmds {
				if cmd.ID == seedResp.CommandID {
					return cmd.State == forma_command.CommandStateSuccess
				}
			}
			return false
		}, 15*time.Second, 100*time.Millisecond, "seed command must complete")

		// ── Step 2: apply a forma whose target config has a $ref to the cluster. ──
		// The TargetUpdater will enter StateResolving and block on the Read override.
		consumerConfig := json.RawMessage(fmt.Sprintf(
			`{"endpoint":{"$ref":"formae://%s#/CidrBlock"}}`, clusterKsuid))

		// ── Assertion B (unidirectional, child → parent): kill TU, executor must stay. ──
		respB, err := m.ApplyForma(
			&pkgmodel.Forma{
				Targets: []pkgmodel.Target{{Label: "consumer-b", Namespace: "FakeAWS", Config: consumerConfig}},
			},
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			"test-client",
		)
		require.NoError(t, err)
		commandIDB := respB.CommandID

		tuNameB := actornames.TargetUpdater("consumer-b", "create", commandIDB)
		assert.Eventually(t, func() bool {
			return isProcessAlive(m.Node, tuNameB)
		}, 10*time.Second, 100*time.Millisecond, "stack-B TargetUpdater should become alive")
		t.Logf("Assertion B: TargetUpdater=%s", tuNameB)

		ceuxNameB := actornames.ChangesetExecutor(commandIDB)
		require.True(t, isProcessAlive(m.Node, ceuxNameB), "stack-B ChangesetExecutor must be alive before TU kill")

		// Kill the TargetUpdater directly.
		require.NoError(t, killProcessByName(m.Node, tuNameB))

		// Give the executor a window to react — it must NOT die.
		time.Sleep(500 * time.Millisecond)
		assert.True(t, isProcessAlive(m.Node, ceuxNameB),
			"ChangesetExecutor must stay alive after its TargetUpdater was killed (unidirectional link)")

		// ── Assertion A (cascade, parent → child): kill executor, TU must die. ──
		respA, err := m.ApplyForma(
			&pkgmodel.Forma{
				Targets: []pkgmodel.Target{{Label: "consumer-a", Namespace: "FakeAWS", Config: consumerConfig}},
			},
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			"test-client",
		)
		require.NoError(t, err)
		commandIDA := respA.CommandID

		tuNameA := actornames.TargetUpdater("consumer-a", "create", commandIDA)
		assert.Eventually(t, func() bool {
			return isProcessAlive(m.Node, tuNameA)
		}, 10*time.Second, 100*time.Millisecond, "stack-A TargetUpdater should become alive")
		t.Logf("Assertion A: TargetUpdater=%s", tuNameA)

		ceuxNameA := actornames.ChangesetExecutor(commandIDA)
		require.True(t, isProcessAlive(m.Node, ceuxNameA), "stack-A ChangesetExecutor must be alive before executor kill")

		// Kill the ChangesetExecutor.
		require.NoError(t, killProcessByName(m.Node, ceuxNameA))

		// The TargetUpdater must cascade-terminate within a few seconds via LinkParent.
		assert.Eventually(t, func() bool {
			return !isProcessAlive(m.Node, tuNameA)
		}, 5*time.Second, 100*time.Millisecond,
			"TargetUpdater must terminate after its parent ChangesetExecutor was killed (LinkParent cascade)")
	})
}
