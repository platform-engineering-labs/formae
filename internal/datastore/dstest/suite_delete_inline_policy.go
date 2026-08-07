// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createDeleteInlinePolicyStack creates a stack for the DeleteInlinePolicy suite
// tests and returns it with its generated ID populated.
func createDeleteInlinePolicyStack(t *testing.T, ds datastore.Datastore, label string) *pkgmodel.Stack {
	t.Helper()
	stack := &pkgmodel.Stack{Label: label, Description: "inline policy deletion"}
	_, err := ds.CreateStack(stack, "cmd-stack")
	require.NoError(t, err)
	require.NotEmpty(t, stack.ID)
	return stack
}

// deleteInlinePolicyTTL returns a TTL policy bound to the given stack, or a
// standalone one when stackID is empty.
func deleteInlinePolicyTTL(label, stackID string, ttlSeconds int64) *pkgmodel.TTLPolicy {
	return &pkgmodel.TTLPolicy{
		Type:         "ttl",
		Label:        label,
		TTLSeconds:   ttlSeconds,
		OnDependents: "cascade",
		StackID:      stackID,
	}
}

// policyLabels collects the labels of the given policies for set assertions.
func policyLabels(policies []pkgmodel.Policy) []string {
	labels := make([]string, 0, len(policies))
	for _, p := range policies {
		labels = append(labels, p.GetLabel())
	}
	return labels
}

// expiredStackLabels collects the stack labels reported by GetExpiredStacks.
func expiredStackLabels(expired []datastore.ExpiredStackInfo) []string {
	labels := make([]string, 0, len(expired))
	for _, e := range expired {
		labels = append(labels, e.StackLabel)
	}
	return labels
}

// RunDeleteInlinePolicy verifies that deleting an inline policy by label
// tombstones it, so the stack no longer carries it.
func RunDeleteInlinePolicy(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteInlinePolicy", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createDeleteInlinePolicyStack(t, ds, "inline-delete")
		_, err := ds.CreatePolicy(deleteInlinePolicyTTL("keep-a-day", stack.ID, 86400), "cmd-create")
		require.NoError(t, err)

		policies, err := ds.GetPoliciesForStack(stack.ID)
		require.NoError(t, err)
		require.Equal(t, []string{"keep-a-day"}, policyLabels(policies))

		version, err := ds.DeleteInlinePolicy(stack.ID, "keep-a-day", "cmd-delete")
		require.NoError(t, err)
		assert.NotEmpty(t, version, "a tombstoned inline policy must report its version")

		policies, err = ds.GetPoliciesForStack(stack.ID)
		require.NoError(t, err)
		assert.Empty(t, policies, "deleted inline policy must not be returned for the stack")
	})
}

// RunDeleteInlinePolicyDeletesEveryMatchingID verifies that every live inline
// policy carrying the label is tombstoned, not just the first one found.
func RunDeleteInlinePolicyDeletesEveryMatchingID(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteInlinePolicy_DeletesEveryMatchingID", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createDeleteInlinePolicyStack(t, ds, "inline-duplicates")
		_, err := ds.CreatePolicy(deleteInlinePolicyTTL("keep-a-day", stack.ID, 86400), "cmd-create")
		require.NoError(t, err)
		_, err = ds.CreatePolicy(deleteInlinePolicyTTL("keep-a-day", stack.ID, 7200), "cmd-create-again")
		require.NoError(t, err)

		policies, err := ds.GetPoliciesForStack(stack.ID)
		require.NoError(t, err)
		require.Equal(t, []string{"keep-a-day", "keep-a-day"}, policyLabels(policies))

		_, err = ds.DeleteInlinePolicy(stack.ID, "keep-a-day", "cmd-delete")
		require.NoError(t, err)

		policies, err = ds.GetPoliciesForStack(stack.ID)
		require.NoError(t, err)
		assert.Empty(t, policies, "every inline policy carrying the label must be deleted")
	})
}

// RunDeleteInlinePolicyEmptyStackID verifies that an empty stack id never
// reaches standalone policies, which are stored without a stack id.
func RunDeleteInlinePolicyEmptyStackID(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteInlinePolicy_EmptyStackID", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		_, err := ds.CreatePolicy(deleteInlinePolicyTTL("standalone-ttl", "", 3600), "cmd-create")
		require.NoError(t, err)

		version, err := ds.DeleteInlinePolicy("", "standalone-ttl", "cmd-delete")
		require.NoError(t, err, "deleting with an empty stack id must be a no-op success")
		assert.Empty(t, version)

		standalone, err := ds.GetStandalonePolicy("standalone-ttl")
		require.NoError(t, err)
		require.NotNil(t, standalone, "the standalone policy must survive an empty stack id delete")
		assert.Equal(t, int64(3600), standalone.(*pkgmodel.TTLPolicy).TTLSeconds)
	})
}

// RunDeleteInlinePolicyLeavesAttachedStandalone verifies that deleting an inline
// policy does not disturb a standalone policy attached to the same stack.
func RunDeleteInlinePolicyLeavesAttachedStandalone(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteInlinePolicy_LeavesAttachedStandalone", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createDeleteInlinePolicyStack(t, ds, "inline-and-attached")
		_, err := ds.CreatePolicy(deleteInlinePolicyTTL("inline-ttl", stack.ID, 86400), "cmd-create")
		require.NoError(t, err)
		_, err = ds.CreatePolicy(&pkgmodel.AutoReconcilePolicy{
			Type:            "auto-reconcile",
			Label:           "shared-reconcile",
			IntervalSeconds: 300,
		}, "cmd-create")
		require.NoError(t, err)
		require.NoError(t, ds.AttachPolicyToStack(stack.ID, "shared-reconcile"))

		policies, err := ds.GetPoliciesForStack(stack.ID)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"inline-ttl", "shared-reconcile"}, policyLabels(policies))

		_, err = ds.DeleteInlinePolicy(stack.ID, "inline-ttl", "cmd-delete")
		require.NoError(t, err)

		policies, err = ds.GetPoliciesForStack(stack.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{"shared-reconcile"}, policyLabels(policies),
			"the attached standalone policy must survive an inline delete")

		attached, err := ds.IsPolicyAttachedToStack("inline-and-attached", "shared-reconcile")
		require.NoError(t, err)
		assert.True(t, attached, "the attachment must survive an inline delete")
	})
}

// RunDeleteInlinePolicyLeavesSameLabelledStandalone verifies that labels are
// scoped: deleting an inline policy leaves a standalone policy carrying the same
// label untouched.
func RunDeleteInlinePolicyLeavesSameLabelledStandalone(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteInlinePolicy_LeavesSameLabelledStandalone", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createDeleteInlinePolicyStack(t, ds, "same-label")
		_, err := ds.CreatePolicy(deleteInlinePolicyTTL("ephemeral", stack.ID, 86400), "cmd-create")
		require.NoError(t, err)
		_, err = ds.CreatePolicy(deleteInlinePolicyTTL("ephemeral", "", 3600), "cmd-create")
		require.NoError(t, err)

		_, err = ds.DeleteInlinePolicy(stack.ID, "ephemeral", "cmd-delete")
		require.NoError(t, err)

		policies, err := ds.GetPoliciesForStack(stack.ID)
		require.NoError(t, err)
		assert.Empty(t, policies, "the inline policy must be gone from the stack")

		standalone, err := ds.GetStandalonePolicy("ephemeral")
		require.NoError(t, err)
		require.NotNil(t, standalone, "the same-labelled standalone policy must survive")
		assert.Equal(t, int64(3600), standalone.(*pkgmodel.TTLPolicy).TTLSeconds)
	})
}

// RunDeleteInlinePolicyIsIdempotent verifies that deleting an already-deleted
// inline policy succeeds without writing a further tombstone.
func RunDeleteInlinePolicyIsIdempotent(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteInlinePolicy_Idempotent", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createDeleteInlinePolicyStack(t, ds, "inline-twice")
		_, err := ds.CreatePolicy(deleteInlinePolicyTTL("keep-a-day", stack.ID, 86400), "cmd-create")
		require.NoError(t, err)

		_, err = ds.DeleteInlinePolicy(stack.ID, "keep-a-day", "cmd-delete")
		require.NoError(t, err)

		version, err := ds.DeleteInlinePolicy(stack.ID, "keep-a-day", "cmd-delete-again")
		require.NoError(t, err, "re-deleting an inline policy must be a no-op success")
		assert.Empty(t, version, "a no-op delete must not report a tombstone version")

		policies, err := ds.GetPoliciesForStack(stack.ID)
		require.NoError(t, err)
		assert.Empty(t, policies)
	})
}

// RunDeleteInlinePolicyUnknownLabel verifies that deleting a label the stack
// never carried is a no-op success.
func RunDeleteInlinePolicyUnknownLabel(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteInlinePolicy_UnknownLabel", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createDeleteInlinePolicyStack(t, ds, "inline-unknown")

		version, err := ds.DeleteInlinePolicy(stack.ID, "never-existed", "cmd-delete")
		require.NoError(t, err, "deleting an absent inline policy must be a no-op success")
		assert.Empty(t, version)
	})
}

// RunDeleteInlinePolicyClearsExpiry verifies that deleting an inline TTL policy
// takes its stack out of the expiry sweep.
func RunDeleteInlinePolicyClearsExpiry(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteInlinePolicy_ClearsExpiry", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createDeleteInlinePolicyStack(t, ds, "expiring-stack")
		_, err := ds.CreatePolicy(deleteInlinePolicyTTL("expired-ttl", stack.ID, 0), "cmd-create")
		require.NoError(t, err)

		// The stack's valid_from is stored with second granularity, so wait past
		// the next second boundary for the zero-second TTL to be in the past.
		time.Sleep(1100 * time.Millisecond)

		expired, err := ds.GetExpiredStacks()
		require.NoError(t, err)
		require.Contains(t, expiredStackLabels(expired), "expiring-stack")

		_, err = ds.DeleteInlinePolicy(stack.ID, "expired-ttl", "cmd-delete")
		require.NoError(t, err)

		expired, err = ds.GetExpiredStacks()
		require.NoError(t, err)
		assert.NotContains(t, expiredStackLabels(expired), "expiring-stack",
			"a stack whose inline TTL policy was deleted must no longer expire")
	})
}

// RunDeleteInlinePolicyThenRecreate verifies that an inline policy can be
// recreated under the same label after being deleted.
func RunDeleteInlinePolicyThenRecreate(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteInlinePolicy_ThenRecreate", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createDeleteInlinePolicyStack(t, ds, "inline-recreate")
		_, err := ds.CreatePolicy(deleteInlinePolicyTTL("keep-a-day", stack.ID, 86400), "cmd-create")
		require.NoError(t, err)

		_, err = ds.DeleteInlinePolicy(stack.ID, "keep-a-day", "cmd-delete")
		require.NoError(t, err)

		_, err = ds.CreatePolicy(deleteInlinePolicyTTL("keep-a-day", stack.ID, 7200), "cmd-recreate")
		require.NoError(t, err)

		policies, err := ds.GetPoliciesForStack(stack.ID)
		require.NoError(t, err)
		require.Equal(t, []string{"keep-a-day"}, policyLabels(policies))
		assert.Equal(t, int64(7200), policies[0].(*pkgmodel.TTLPolicy).TTLSeconds)
	})
}
