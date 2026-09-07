// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createInlinePoliciesStack creates a stack for the GetInlinePoliciesForStack
// suite tests and returns it with its generated ID populated.
func createInlinePoliciesStack(t *testing.T, ds datastore.Datastore, label string) *pkgmodel.Stack {
	t.Helper()
	stack := &pkgmodel.Stack{Label: label, Description: "inline policy lookup"}
	_, err := ds.CreateStack(stack, "cmd-stack")
	require.NoError(t, err)
	require.NotEmpty(t, stack.ID)
	return stack
}

// inlinePoliciesTTL returns a TTL policy bound to the given stack, or a
// standalone one when stackID is empty.
func inlinePoliciesTTL(label, stackID string, ttlSeconds int64) *pkgmodel.TTLPolicy {
	return &pkgmodel.TTLPolicy{
		Type:         "ttl",
		Label:        label,
		TTLSeconds:   ttlSeconds,
		OnDependents: "cascade",
		StackID:      stackID,
	}
}

// RunGetInlinePoliciesForStack verifies that a stack's own policies are
// returned, carrying its ID, and that another stack's are not.
func RunGetInlinePoliciesForStack(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetInlinePoliciesForStack", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createInlinePoliciesStack(t, ds, "inline-owner")
		other := createInlinePoliciesStack(t, ds, "inline-other")

		_, err := ds.CreatePolicy(inlinePoliciesTTL("keep-a-day", stack.ID, 86400), "cmd-create")
		require.NoError(t, err)
		_, err = ds.CreatePolicy(&pkgmodel.AutoReconcilePolicy{
			Type:            "auto-reconcile",
			Label:           "reconcile-5m",
			IntervalSeconds: 300,
			StackID:         stack.ID,
		}, "cmd-create")
		require.NoError(t, err)
		_, err = ds.CreatePolicy(inlinePoliciesTTL("other-ttl", other.ID, 3600), "cmd-create")
		require.NoError(t, err)

		policies, err := ds.GetInlinePoliciesForStack(stack.ID)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"keep-a-day", "reconcile-5m"}, policyLabels(policies))
		for _, p := range policies {
			assert.Equal(t, stack.ID, p.GetStackID(), "an inline policy must carry its own stack ID")
		}
	})
}

// RunGetInlinePoliciesForStackExcludesAttachedStandalone verifies that a
// standalone policy attached through the junction table is not reported as an
// inline policy, even though the stack-wide read does return it.
func RunGetInlinePoliciesForStackExcludesAttachedStandalone(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetInlinePoliciesForStack_ExcludesAttachedStandalone", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createInlinePoliciesStack(t, ds, "inline-and-attached")
		_, err := ds.CreatePolicy(inlinePoliciesTTL("inline-ttl", stack.ID, 86400), "cmd-create")
		require.NoError(t, err)
		_, err = ds.CreatePolicy(&pkgmodel.AutoReconcilePolicy{
			Type:            "auto-reconcile",
			Label:           "shared-reconcile",
			IntervalSeconds: 300,
		}, "cmd-create")
		require.NoError(t, err)
		require.NoError(t, ds.AttachPolicyToStack(stack.ID, "shared-reconcile"))

		all, err := ds.GetPoliciesForStack(stack.ID)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"inline-ttl", "shared-reconcile"}, policyLabels(all))

		policies, err := ds.GetInlinePoliciesForStack(stack.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{"inline-ttl"}, policyLabels(policies),
			"an attached standalone policy is not an inline policy of the stack")
	})
}

// RunGetInlinePoliciesForStackExcludesDeleted verifies that a tombstoned inline
// policy is no longer reported.
func RunGetInlinePoliciesForStackExcludesDeleted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetInlinePoliciesForStack_ExcludesDeleted", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createInlinePoliciesStack(t, ds, "inline-deleted")
		_, err := ds.CreatePolicy(inlinePoliciesTTL("keep-a-day", stack.ID, 86400), "cmd-create")
		require.NoError(t, err)
		_, err = ds.CreatePolicy(inlinePoliciesTTL("keep-an-hour", stack.ID, 3600), "cmd-create")
		require.NoError(t, err)

		_, err = ds.DeleteInlinePolicy(stack.ID, "keep-a-day", "cmd-delete")
		require.NoError(t, err)

		policies, err := ds.GetInlinePoliciesForStack(stack.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{"keep-an-hour"}, policyLabels(policies),
			"a deleted inline policy must not be returned for the stack")
	})
}

// RunGetInlinePoliciesForStackReturnsLatestVersion verifies that an updated
// inline policy is returned once, with its latest data.
func RunGetInlinePoliciesForStackReturnsLatestVersion(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetInlinePoliciesForStack_ReturnsLatestVersion", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := createInlinePoliciesStack(t, ds, "inline-updated")
		_, err := ds.CreatePolicy(inlinePoliciesTTL("keep-a-day", stack.ID, 86400), "cmd-create")
		require.NoError(t, err)
		_, err = ds.UpdatePolicy(inlinePoliciesTTL("keep-a-day", stack.ID, 7200), "cmd-update")
		require.NoError(t, err)

		policies, err := ds.GetInlinePoliciesForStack(stack.ID)
		require.NoError(t, err)
		require.Equal(t, []string{"keep-a-day"}, policyLabels(policies))
		assert.Equal(t, int64(7200), policies[0].(*pkgmodel.TTLPolicy).TTLSeconds)
	})
}

// RunGetInlinePoliciesForStackEmptyStackID verifies that an empty stack ID never
// reaches standalone policies, which are stored without a stack ID.
func RunGetInlinePoliciesForStackEmptyStackID(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetInlinePoliciesForStack_EmptyStackID", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		_, err := ds.CreatePolicy(inlinePoliciesTTL("standalone-ttl", "", 3600), "cmd-create")
		require.NoError(t, err)

		policies, err := ds.GetInlinePoliciesForStack("")
		require.NoError(t, err, "an empty stack id must be a no-op success")
		assert.Empty(t, policies, "a standalone policy is never an inline policy")
	})
}
