// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package policy_update

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// stubDatastore is a minimal in-test PolicyDatastore. Only the methods a given
// test needs are populated; the rest return zero values.
type stubDatastore struct {
	stackByLabel     *pkgmodel.Stack
	policiesForStack []pkgmodel.Policy
	standalonePolicy pkgmodel.Policy
}

func (s *stubDatastore) GetStackByLabel(label string) (*pkgmodel.Stack, error) {
	return s.stackByLabel, nil
}

func (s *stubDatastore) GetPoliciesForStack(stackID string) ([]pkgmodel.Policy, error) {
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

	updates, err := pg.generateInlinePolicyUpdates(stack)
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
			&pkgmodel.AutoReconcilePolicy{Label: "existing-label", IntervalSeconds: 300},
		},
	}
	pg := NewPolicyUpdateGenerator(ds)

	stack := pkgmodel.Stack{
		Label: "my-stack",
		Policies: []json.RawMessage{
			json.RawMessage(`{"Type":"auto-reconcile","IntervalSeconds":300}`),
		},
	}

	updates, err := pg.generateInlinePolicyUpdates(stack)
	require.NoError(t, err)
	require.Len(t, updates, 1)

	assert.Equal(t, PolicyOperationUpdate, updates[0].Operation)
	assert.Equal(t, "existing-label", updates[0].Policy.GetLabel(),
		"inline auto-reconcile policy should reuse the existing policy's label")
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
