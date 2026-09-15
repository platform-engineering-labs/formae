//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package resource_persister

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestCleanupRetirementPreservesFailedCreate(t *testing.T) {
	for _, unresolved := range []bool{true, false} {
		t.Run(map[bool]string{true: "failed-create", false: "settled"}[unresolved], func(t *testing.T) {
			p, sender, ds, err := newResourcePersisterForTest(t)
			require.NoError(t, err)
			_, err = ds.CreateStack(&pkgmodel.Stack{Label: "retirement"}, "setup")
			require.NoError(t, err)
			stack, err := ds.GetStackByLabel("retirement")
			require.NoError(t, err)
			live := pkgmodel.Resource{Ksuid: util.NewID(), Stack: stack.Label, Label: "live", Target: "t", Type: "Test::Resource", Managed: true, Properties: []byte(`{}`)}
			create := &forma_command.FormaCommand{ID: util.NewID(), Command: pkgmodel.CommandApply, Source: forma_command.SourceUser, State: forma_command.CommandStateSuccess, Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, StartTs: time.Now().UTC(), Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}}, ResourceUpdates: []resource_update.ResourceUpdate{{DesiredState: live, StackLabel: stack.Label, Operation: resource_update.OperationCreate, Source: resource_update.FormaCommandSourceUser, State: resource_update.ResourceUpdateStateSuccess}}}
			require.NoError(t, ds.StoreFormaCommand(create, create.ID))
			_, err = ds.StoreResource(&live, create.ID)
			require.NoError(t, err)
			if unresolved {
				failed := *create
				failed.ID = util.NewID()
				failed.StartTs = time.Now().UTC()
				failed.State = forma_command.CommandStateFailed
				failed.ResourceUpdates = append([]resource_update.ResourceUpdate(nil), create.ResourceUpdates...)
				failed.ResourceUpdates[0].DesiredState.Ksuid = "never-created"
				failed.ResourceUpdates[0].DesiredState.Label = "pending"
				require.NoError(t, ds.StoreFormaCommand(&failed, failed.ID))
			}
			destroy := *create
			destroy.ID = util.NewID()
			destroy.StartTs = time.Now().UTC()
			destroy.Command = pkgmodel.CommandDestroy
			destroy.ResourceUpdates = append([]resource_update.ResourceUpdate(nil), create.ResourceUpdates...)
			destroy.ResourceUpdates[0].Operation = resource_update.OperationDelete
			require.NoError(t, ds.StoreFormaCommand(&destroy, destroy.ID))
			_, err = ds.DeleteResource(&live, destroy.ID)
			require.NoError(t, err)
			p.SendMessage(sender, messages.CleanupEmptyStacks{StackLabels: []string{stack.Label}, CommandID: destroy.ID})
			// The unit actor handles SendMessage synchronously; this read observes its completed cleanup.
			current, err := ds.GetStackByLabel(stack.Label)
			require.NoError(t, err)
			if unresolved {
				require.NotNil(t, current, "partial destroy must preserve failed-create ownership")
				baseline, err := ds.GetResourcesAtLastReconcile(stack.Label)
				require.NoError(t, err)
				require.Len(t, baseline, 1)
				require.Equal(t, "never-created", baseline[0].KSUID)
			} else {
				require.Nil(t, current, "settled last-resource destruction must retire")
			}
		})
	}
}

func TestReapRetirementSettledStack(t *testing.T) {
	persister, sender, ds, err := newResourcePersisterForTest(t)
	require.NoError(t, err)

	const label = "reap-actor-target"
	const stack = "reap-actor-stack"
	_, err = ds.CreateStack(&pkgmodel.Stack{Label: stack}, "setup")
	require.NoError(t, err)

	_, err = ds.CreateTarget(&pkgmodel.Target{
		Label:     label,
		Namespace: "AWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
		Reaping:   json.RawMessage(`{"Kind":"after","MaxUnreachableSeconds":100}`),
	})
	require.NoError(t, err)

	loaded, err := ds.LoadTarget(label)
	require.NoError(t, err)
	require.NotNil(t, loaded.Health)
	inc := loaded.Health.IncarnationID

	seenAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	observedAt := time.Now().UTC().Add(-90 * time.Minute).Truncate(time.Second)
	applied, err := ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
		TargetLabel:   label,
		State:         pkgmodel.TargetHealthStateUnreachable,
		ObservedAt:    observedAt,
		LastSeenAt:    &seenAt,
		IncarnationID: inc,
	})
	require.NoError(t, err)
	require.True(t, applied)
	sampleAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	applied, err = ds.AdvanceTargetAccrual(label, inc, sampleAt, 100)
	require.NoError(t, err)
	require.True(t, applied)

	res := &pkgmodel.Resource{
		Ksuid:      util.NewID(),
		NativeID:   "native-reap-actor",
		Stack:      stack,
		Type:       "AWS::S3::Bucket",
		Label:      "bucket",
		Target:     label,
		Managed:    true,
		Properties: json.RawMessage(`{"key":"value"}`),
	}
	_, err = ds.StoreResource(res, "cmd-create")
	require.NoError(t, err)

	result := persister.Call(sender, messages.PersistTargetReap{
		Label:            label,
		IncarnationID:    inc,
		LastSeenBefore:   time.Now().UTC(),
		LastSampleBefore: time.Now().UTC(),
		ReapedAt:         time.Now().UTC(),
	})
	require.NoError(t, result.Error)
	reapResult, ok := result.Response.(messages.PersistTargetReapResult)
	require.True(t, ok, "handler must reply with PersistTargetReapResult, got %T", result.Response)
	require.True(t, reapResult.Reaped, "an over-threshold unreachable target must reap")

	reloaded, err := ds.LoadTarget(label)
	require.NoError(t, err)
	require.NotNil(t, reloaded.Health)
	require.Equal(t, pkgmodel.TargetHealthStateReaped, reloaded.Health.State)

	live, err := ds.LoadResourcesByStack(stack)
	require.NoError(t, err)
	require.Empty(t, live, "the reaped target's resources must be invisible to live queries")
	current, err := ds.GetStackByLabel(stack)
	require.NoError(t, err)
	require.Nil(t, current, "settled reap must still retire its emptied stack")
}
