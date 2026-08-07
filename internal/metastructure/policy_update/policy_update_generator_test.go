// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package policy_update

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// stubDatastore is a minimal in-test PolicyDatastore. Only the methods a given
// test needs are populated; the rest return zero values. The *Err fields make a
// method fail, and lookupCalls counts the existing-state lookups.
type stubDatastore struct {
	stackByLabel        *pkgmodel.Stack
	stackByLabelErr     error
	policiesForStack    []pkgmodel.Policy
	policiesForStackErr error
	standalonePolicy    pkgmodel.Policy
	lookupCalls         int
}

func (s *stubDatastore) GetStackByLabel(label string) (*pkgmodel.Stack, error) {
	s.lookupCalls++
	if s.stackByLabelErr != nil {
		return nil, s.stackByLabelErr
	}
	return s.stackByLabel, nil
}

func (s *stubDatastore) GetPoliciesForStack(stackID string) ([]pkgmodel.Policy, error) {
	s.lookupCalls++
	if s.policiesForStackErr != nil {
		return nil, s.policiesForStackErr
	}
	return s.policiesForStack, nil
}

func (s *stubDatastore) GetStandalonePolicy(label string) (pkgmodel.Policy, error) {
	return s.standalonePolicy, nil
}

func (s *stubDatastore) IsPolicyAttachedToStack(stackLabel, policyLabel string) (bool, error) {
	return false, nil
}

func (s *stubDatastore) GetStacksReferencingPolicy(policyLabel string) ([]string, error) {
	return nil, nil
}

func (s *stubDatastore) GetAttachedPolicyLabelsForStack(stackLabel string) ([]string, error) {
	return nil, nil
}

func TestPoliciesEqual_AutoReconcile(t *testing.T) {
	a := &pkgmodel.AutoReconcilePolicy{Label: "reconcile-5m", IntervalSeconds: 300}
	b := &pkgmodel.AutoReconcilePolicy{Label: "reconcile-5m", IntervalSeconds: 300}
	assert.True(t, policiesEqual(a, b), "identical auto-reconcile policies should compare equal")

	c := &pkgmodel.AutoReconcilePolicy{Label: "reconcile-5m", IntervalSeconds: 600}
	assert.False(t, policiesEqual(a, c), "differing IntervalSeconds should compare not-equal")
}

func TestPoliciesEqual_TTL(t *testing.T) {
	a := &pkgmodel.TTLPolicy{Label: "ephemeral", TTLSeconds: 3600, OnDependents: "abort"}
	b := &pkgmodel.TTLPolicy{Label: "ephemeral", TTLSeconds: 3600, OnDependents: "abort"}
	assert.True(t, policiesEqual(a, b), "identical TTL policies should compare equal")

	c := &pkgmodel.TTLPolicy{Label: "ephemeral", TTLSeconds: 7200, OnDependents: "abort"}
	assert.False(t, policiesEqual(a, c), "differing TTLSeconds should compare not-equal")
}

func TestGenerateInlinePolicyUpdates_AutoReconcile_NewPolicyGeneratesLabel(t *testing.T) {
	pg := NewPolicyUpdateGenerator(nil)

	stack := pkgmodel.Stack{
		Label: "my-stack",
		Policies: []json.RawMessage{
			json.RawMessage(`{"Type":"auto-reconcile","IntervalSeconds":300}`),
		},
	}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	require.Len(t, updates, 1)

	assert.Equal(t, PolicyOperationCreate, updates[0].Operation)
	label := updates[0].Policy.GetLabel()
	assert.NotEmpty(t, label, "an inline auto-reconcile policy without a label should get one generated")
	assert.True(t, strings.HasPrefix(label, "my-stack-auto-reconcile-"),
		"generated label %q should be prefixed {stack}-auto-reconcile-", label)
}

func TestGenerateInlinePolicyUpdates_AutoReconcile_ReusesExistingLabel(t *testing.T) {
	ds := &stubDatastore{
		stackByLabel: &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		policiesForStack: []pkgmodel.Policy{
			&pkgmodel.AutoReconcilePolicy{Label: "existing-label", IntervalSeconds: 300, StackID: "stack-1"},
		},
	}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{
		Label: "my-stack",
		Policies: []json.RawMessage{
			json.RawMessage(`{"Type":"auto-reconcile","IntervalSeconds":300}`),
		},
	}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	require.Len(t, updates, 1)

	assert.Equal(t, PolicyOperationUpdate, updates[0].Operation)
	assert.Equal(t, "existing-label", updates[0].Policy.GetLabel(),
		"inline auto-reconcile policy should reuse the existing policy's label")
}

func TestGenerateInlinePolicyUpdates_Reconcile_UndeclaredInlinePolicyDeleted(t *testing.T) {
	stored := &pkgmodel.TTLPolicy{Label: "my-stack-ttl-abc12345", TTLSeconds: 3600, OnDependents: "abort", StackID: "stack-1"}
	ds := &stubDatastore{
		stackByLabel:     &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		policiesForStack: []pkgmodel.Policy{stored},
	}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{Label: "my-stack"}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	require.Len(t, updates, 1, "an inline policy dropped from the declaration should be deleted in reconcile mode")

	assert.Equal(t, PolicyOperationDelete, updates[0].Operation)
	assert.Equal(t, PolicyUpdateStateNotStarted, updates[0].State)
	assert.Same(t, stored, updates[0].Policy, "the delete should carry the stored policy")
	assert.Equal(t, "my-stack", updates[0].StackLabel, "an inline delete must carry its stack label")
}

func TestGenerateInlinePolicyUpdates_Reconcile_StandaloneRefDoesNotPreventInlineDelete(t *testing.T) {
	stored := &pkgmodel.TTLPolicy{Label: "my-stack-ttl-abc12345", TTLSeconds: 3600, OnDependents: "abort", StackID: "stack-1"}
	ds := &stubDatastore{
		stackByLabel:     &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		policiesForStack: []pkgmodel.Policy{stored},
	}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{
		Label: "my-stack",
		Policies: []json.RawMessage{
			json.RawMessage(`{"$ref":"policy://reconcile-5m"}`),
		},
	}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	require.Len(t, updates, 1, "a stack declaring only a standalone reference still drops its inline policy")

	assert.Equal(t, PolicyOperationDelete, updates[0].Operation)
	assert.Same(t, stored, updates[0].Policy)
}

func TestGenerateInlinePolicyUpdates_Reconcile_DeclaredTypeUpdatedUndeclaredDeleted(t *testing.T) {
	ds := &stubDatastore{
		stackByLabel: &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		policiesForStack: []pkgmodel.Policy{
			&pkgmodel.TTLPolicy{Label: "my-stack-ttl-abc12345", TTLSeconds: 3600, OnDependents: "abort", StackID: "stack-1"},
			&pkgmodel.AutoReconcilePolicy{Label: "my-stack-auto-reconcile-def67890", IntervalSeconds: 300, StackID: "stack-1"},
		},
	}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{
		Label: "my-stack",
		Policies: []json.RawMessage{
			json.RawMessage(`{"Type":"ttl","TTLSeconds":7200,"OnDependents":"abort"}`),
		},
	}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	require.Len(t, updates, 2)

	assert.Equal(t, PolicyOperationUpdate, updates[0].Operation)
	assert.Equal(t, "ttl", updates[0].Policy.GetType())
	assert.Equal(t, "my-stack-ttl-abc12345", updates[0].Policy.GetLabel())

	assert.Equal(t, PolicyOperationDelete, updates[1].Operation)
	assert.Equal(t, "auto-reconcile", updates[1].Policy.GetType())
	assert.Equal(t, "my-stack-auto-reconcile-def67890", updates[1].Policy.GetLabel())
}

func TestGenerateInlinePolicyUpdates_Reconcile_AttachedStandalonePolicyNotDeleted(t *testing.T) {
	ds := &stubDatastore{
		stackByLabel: &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		policiesForStack: []pkgmodel.Policy{
			// Attached via the junction table, so it carries no stack ID.
			&pkgmodel.TTLPolicy{Label: "shared-ttl", TTLSeconds: 3600, OnDependents: "abort"},
		},
	}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{Label: "my-stack"}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, updates, "an attached standalone policy is detached, never deleted as an inline policy")
}

func TestGenerateInlinePolicyUpdates_Reconcile_AttachedStandaloneLabelNotReused(t *testing.T) {
	ds := &stubDatastore{
		stackByLabel: &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		policiesForStack: []pkgmodel.Policy{
			&pkgmodel.TTLPolicy{Label: "shared-ttl", TTLSeconds: 3600, OnDependents: "abort"},
		},
	}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{
		Label: "my-stack",
		Policies: []json.RawMessage{
			json.RawMessage(`{"Type":"ttl","TTLSeconds":7200,"OnDependents":"abort"}`),
		},
	}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	require.Len(t, updates, 1, "the attached standalone policy should neither be updated nor deleted")

	assert.Equal(t, PolicyOperationCreate, updates[0].Operation)
	assert.NotEqual(t, "shared-ttl", updates[0].Policy.GetLabel(),
		"an inline policy must not take over an attached standalone policy's label")
}

func TestGenerateInlinePolicyUpdates_Patch_NoPoliciesPerformsNoLookups(t *testing.T) {
	ds := &stubDatastore{
		stackByLabel: &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		policiesForStack: []pkgmodel.Policy{
			&pkgmodel.TTLPolicy{Label: "my-stack-ttl-abc12345", TTLSeconds: 3600, OnDependents: "abort", StackID: "stack-1"},
		},
	}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{Label: "my-stack"}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	assert.Empty(t, updates, "patch never deletes an inline policy the declaration omits")
	assert.Zero(t, ds.lookupCalls, "a policy-less stack in patch mode should perform no datastore lookups")
}

func TestGenerateInlinePolicyUpdates_Reconcile_StackLookupFails(t *testing.T) {
	ds := &stubDatastore{stackByLabelErr: errors.New("connection refused")}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{Label: "my-stack"}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModeReconcile)
	require.Error(t, err, "a failed stack lookup must not be swallowed into a destructive decision")
	assert.Empty(t, updates)
}

func TestGenerateInlinePolicyUpdates_Reconcile_PolicyLookupFails(t *testing.T) {
	ds := &stubDatastore{
		stackByLabel:        &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		policiesForStackErr: errors.New("connection refused"),
	}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{Label: "my-stack"}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModeReconcile)
	require.Error(t, err, "a failed policy lookup must not be swallowed into a destructive decision")
	assert.Empty(t, updates)
}

func TestGenerateInlinePolicyUpdates_Reconcile_SecondApplyAfterDeleteIsNoOp(t *testing.T) {
	ds := &stubDatastore{stackByLabel: &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"}}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{Label: "my-stack"}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, updates, "re-applying a stack whose inline policy is already gone should emit nothing")
}

func TestGeneratePolicyUpdates_Reconcile_DeletesUndeclaredInlinePolicy(t *testing.T) {
	stored := &pkgmodel.TTLPolicy{Label: "my-stack-ttl-abc12345", TTLSeconds: 3600, OnDependents: "abort", StackID: "stack-1"}
	ds := &stubDatastore{
		stackByLabel:     &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		policiesForStack: []pkgmodel.Policy{stored},
	}
	pg := NewPolicyUpdateGenerator(ds)

	forma := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "my-stack"}}}

	updates, err := pg.GeneratePolicyUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	require.Len(t, updates, 1)

	assert.Equal(t, PolicyOperationDelete, updates[0].Operation)
	assert.Same(t, stored, updates[0].Policy)
	assert.Equal(t, "my-stack", updates[0].StackLabel)
}

func TestGeneratePolicyUpdates_Destroy_EmitsNoInlinePolicyDelete(t *testing.T) {
	ds := &stubDatastore{
		stackByLabel: &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		policiesForStack: []pkgmodel.Policy{
			&pkgmodel.TTLPolicy{Label: "my-stack-ttl-abc12345", TTLSeconds: 3600, OnDependents: "abort", StackID: "stack-1"},
		},
	}
	pg := NewPolicyUpdateGenerator(ds)

	forma := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "my-stack"}}}

	updates, err := pg.GeneratePolicyUpdates(forma, pkgmodel.CommandDestroy, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, updates, "inline policies are removed with their stack, not listed as deletes on destroy")
}

func TestGenerateStandalonePolicyUpdates_AutoReconcile_UnchangedSkipped(t *testing.T) {
	ds := &stubDatastore{
		standalonePolicy: &pkgmodel.AutoReconcilePolicy{Label: "reconcile-5m", IntervalSeconds: 300},
	}
	pg := NewPolicyUpdateGenerator(ds)

	updates, err := pg.generateStandalonePolicyUpdates([]json.RawMessage{
		json.RawMessage(`{"Type":"auto-reconcile","Label":"reconcile-5m","IntervalSeconds":300}`),
	})
	require.NoError(t, err)
	assert.Empty(t, updates, "an unchanged standalone auto-reconcile policy should emit no update")
}
