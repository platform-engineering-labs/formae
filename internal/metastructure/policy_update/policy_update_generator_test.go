// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package policy_update

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// stubDatastore is a minimal in-test PolicyDatastore. Only the methods a given
// test needs are populated; the rest return zero values. The *Err fields make a
// method fail, and lookupCalls counts the existing-state lookups.
//
// It models both stack-scoped policy reads the datastore offers, because they
// differ: the inline read returns only the policies the stack owns, while the
// stack-wide read also returns the standalone policies attached to it through
// the junction table, stamping every row it returns with the queried stack's ID.
// A policy's stack ID therefore cannot tell inline and attached apart.
type stubDatastore struct {
	stackByLabel             *pkgmodel.Stack
	stackByLabelErr          error
	inlinePoliciesForStack   []pkgmodel.Policy
	attachedPoliciesForStack []pkgmodel.Policy
	policyLookupErr          error
	standalonePolicy         pkgmodel.Policy
	lookupCalls              int
}

func (s *stubDatastore) GetStackByLabel(label string) (*pkgmodel.Stack, error) {
	s.lookupCalls++
	if s.stackByLabelErr != nil {
		return nil, s.stackByLabelErr
	}
	return s.stackByLabel, nil
}

func (s *stubDatastore) GetInlinePoliciesForStack(stackID string) ([]pkgmodel.Policy, error) {
	s.lookupCalls++
	if s.policyLookupErr != nil {
		return nil, s.policyLookupErr
	}
	return stampStackID(s.inlinePoliciesForStack, stackID), nil
}

func (s *stubDatastore) GetPoliciesForStack(stackID string) ([]pkgmodel.Policy, error) {
	s.lookupCalls++
	if s.policyLookupErr != nil {
		return nil, s.policyLookupErr
	}
	policies := make([]pkgmodel.Policy, 0, len(s.inlinePoliciesForStack)+len(s.attachedPoliciesForStack))
	policies = append(policies, stampStackID(s.inlinePoliciesForStack, stackID)...)
	policies = append(policies, stampStackID(s.attachedPoliciesForStack, stackID)...)
	return policies, nil
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

// stampStackID applies the stamping a stack-scoped read performs: every policy
// it returns carries the queried stack's ID, attached standalone ones included.
func stampStackID(policies []pkgmodel.Policy, stackID string) []pkgmodel.Policy {
	for _, p := range policies {
		p.SetStackID(stackID)
	}
	return policies
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

func TestPoliciesEqual_TTLAbsolute(t *testing.T) {
	deadline := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	a := &pkgmodel.TTLPolicy{Label: "trial", ExpiresAt: deadline, OnDependents: "abort"}
	b := &pkgmodel.TTLPolicy{Label: "trial", ExpiresAt: deadline, OnDependents: "abort"}
	assert.True(t, policiesEqual(a, b), "identical absolute TTL policies should compare equal")

	c := &pkgmodel.TTLPolicy{
		Label: "trial", ExpiresAt: deadline.Add(time.Hour), OnDependents: "abort",
	}
	assert.False(t, policiesEqual(a, c), "a moved deadline should compare not-equal")

	// Switching a policy between the two variants is a change, not a no-op.
	relative := &pkgmodel.TTLPolicy{Label: "trial", TTLSeconds: 3600, OnDependents: "abort"}
	assert.False(t, policiesEqual(a, relative), "ttl to expiresAt should compare not-equal")
	assert.False(t, policiesEqual(relative, a), "expiresAt to ttl should compare not-equal")
}

// The same instant spelled differently is the same deadline: storage
// canonicalises to UTC whole seconds, so comparing representations rather than
// instants would report a change on every re-apply.
func TestPoliciesEqual_TTLAbsoluteSameInstantDifferentSpelling(t *testing.T) {
	utc := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	offset := time.Date(2026, 8, 7, 14, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	subsecond := time.Date(2026, 8, 7, 12, 0, 0, 750_000_000, time.UTC)

	a := &pkgmodel.TTLPolicy{Label: "trial", ExpiresAt: utc, OnDependents: "abort"}
	b := &pkgmodel.TTLPolicy{Label: "trial", ExpiresAt: offset, OnDependents: "abort"}
	c := &pkgmodel.TTLPolicy{Label: "trial", ExpiresAt: subsecond, OnDependents: "abort"}

	assert.True(t, policiesEqual(a, b), "the same instant in another zone is not a change")
	assert.True(t, policiesEqual(a, c), "sub-second precision is dropped by storage, so not a change")
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
		inlinePoliciesForStack: []pkgmodel.Policy{
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
		stackByLabel:           &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		inlinePoliciesForStack: []pkgmodel.Policy{stored},
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
		stackByLabel:           &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		inlinePoliciesForStack: []pkgmodel.Policy{stored},
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
		inlinePoliciesForStack: []pkgmodel.Policy{
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

func TestGenerateInlinePolicyUpdates_Reconcile_SurplusPolicyOfDeclaredTypeDeleted(t *testing.T) {
	ds := &stubDatastore{
		stackByLabel: &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		inlinePoliciesForStack: []pkgmodel.Policy{
			&pkgmodel.TTLPolicy{Label: "my-stack-ttl-abc12345", TTLSeconds: 3600, OnDependents: "abort", StackID: "stack-1"},
			&pkgmodel.TTLPolicy{Label: "my-stack-ttl-def67890", TTLSeconds: 1800, OnDependents: "abort", StackID: "stack-1"},
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
	require.Len(t, updates, 2, "a single declaration matches one stored policy of its type; the surplus is deleted")

	require.Equal(t, PolicyOperationUpdate, updates[0].Operation)
	matched := updates[0].Policy.GetLabel()
	assert.Contains(t, []string{"my-stack-ttl-abc12345", "my-stack-ttl-def67890"}, matched)

	assert.Equal(t, PolicyOperationDelete, updates[1].Operation)
	assert.Equal(t, "ttl", updates[1].Policy.GetType())
	assert.Contains(t, []string{"my-stack-ttl-abc12345", "my-stack-ttl-def67890"}, updates[1].Policy.GetLabel())
	assert.NotEqual(t, matched, updates[1].Policy.GetLabel(),
		"the delete must target the stored policy the declaration did not match")
	assert.Equal(t, "my-stack", updates[1].StackLabel)
}

func TestGenerateInlinePolicyUpdates_Reconcile_AllUndeclaredPoliciesOfOneTypeDeleted(t *testing.T) {
	ds := &stubDatastore{
		stackByLabel: &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		inlinePoliciesForStack: []pkgmodel.Policy{
			&pkgmodel.TTLPolicy{Label: "my-stack-ttl-abc12345", TTLSeconds: 3600, OnDependents: "abort", StackID: "stack-1"},
			&pkgmodel.TTLPolicy{Label: "my-stack-ttl-def67890", TTLSeconds: 1800, OnDependents: "abort", StackID: "stack-1"},
		},
	}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{Label: "my-stack"}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	require.Len(t, updates, 2, "a stack declaring no policies drops every inline policy it stores")

	assert.Equal(t, PolicyOperationDelete, updates[0].Operation)
	assert.Equal(t, "my-stack-ttl-abc12345", updates[0].Policy.GetLabel())
	assert.Equal(t, PolicyOperationDelete, updates[1].Operation)
	assert.Equal(t, "my-stack-ttl-def67890", updates[1].Policy.GetLabel())
}

func TestGenerateInlinePolicyUpdates_Reconcile_AttachedStandalonePolicyNotDeleted(t *testing.T) {
	ds := &stubDatastore{
		stackByLabel: &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		attachedPoliciesForStack: []pkgmodel.Policy{
			&pkgmodel.TTLPolicy{Label: "shared-ttl", TTLSeconds: 3600, OnDependents: "abort"},
		},
	}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{Label: "my-stack"}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, updates, "an attached standalone policy is detached, never deleted as an inline policy")
}

func TestGenerateInlinePolicyUpdates_Reconcile_ReferencedStandalonePolicyNotDeleted(t *testing.T) {
	ds := &stubDatastore{
		stackByLabel: &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		attachedPoliciesForStack: []pkgmodel.Policy{
			&pkgmodel.TTLPolicy{Label: "shared-ttl", TTLSeconds: 3600, OnDependents: "abort"},
		},
	}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{
		Label: "my-stack",
		Policies: []json.RawMessage{
			json.RawMessage(`{"$ref":"policy://shared-ttl"}`),
		},
	}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, updates, "a standalone policy the stack still references must never be deleted")
}

func TestGenerateInlinePolicyUpdates_Reconcile_AttachedStandaloneLabelNotReused(t *testing.T) {
	ds := &stubDatastore{
		stackByLabel: &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		attachedPoliciesForStack: []pkgmodel.Policy{
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
		inlinePoliciesForStack: []pkgmodel.Policy{
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
		stackByLabel:    &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		policyLookupErr: errors.New("connection refused"),
	}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{Label: "my-stack"}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModeReconcile)
	require.Error(t, err, "a failed policy lookup must not be swallowed into a destructive decision")
	assert.Empty(t, updates)
}

func TestGenerateInlinePolicyUpdates_Reconcile_SecondApplyAfterDeleteIsNoOp(t *testing.T) {
	ds := &stubDatastore{
		stackByLabel: &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		inlinePoliciesForStack: []pkgmodel.Policy{
			&pkgmodel.TTLPolicy{Label: "my-stack-ttl-abc12345", TTLSeconds: 3600, OnDependents: "abort", StackID: "stack-1"},
		},
	}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{Label: "my-stack"}

	updates, err := pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	require.Len(t, updates, 1, "the first apply drops the inline policy the declaration omits")
	require.Equal(t, PolicyOperationDelete, updates[0].Operation)

	// The delete has been executed, so the stack no longer carries the policy.
	ds.inlinePoliciesForStack = nil

	updates, err = pg.generateInlinePolicyUpdates(stack, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, updates, "re-applying a stack whose inline policy is already gone should emit nothing")
}

func TestGeneratePolicyUpdates_Reconcile_DeletesUndeclaredInlinePolicy(t *testing.T) {
	stored := &pkgmodel.TTLPolicy{Label: "my-stack-ttl-abc12345", TTLSeconds: 3600, OnDependents: "abort", StackID: "stack-1"}
	ds := &stubDatastore{
		stackByLabel:           &pkgmodel.Stack{ID: "stack-1", Label: "my-stack"},
		inlinePoliciesForStack: []pkgmodel.Policy{stored},
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
		inlinePoliciesForStack: []pkgmodel.Policy{
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
