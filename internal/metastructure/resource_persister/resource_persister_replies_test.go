// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_persister

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// rejectingStore wraps a real datastore and rejects every resource-version
// write, the shape a reaped/incarnation-guard rejection takes.
type rejectingStore struct {
	datastore.Datastore
}

func (s *rejectingStore) StoreResource(resource *pkgmodel.Resource, commandID string, expectedIncarnation ...string) (string, error) {
	return "", fmt.Errorf("%w: incarnation mismatch", datastore.ErrResourceWriteRejected)
}

func newPersisterWithStore(t *testing.T, ds datastore.Datastore) (*unit.TestActor, gen.PID, error) {
	env := map[gen.Env]any{
		"Datastore": ds,
		"DiscoveryConfig": pkgmodel.DiscoveryConfig{
			Enabled:  true,
			Interval: 10 * time.Minute,
		},
	}
	sender := gen.PID{Node: "test", ID: 100}
	actor, err := unit.Spawn(t, NewResourcePersister, unit.WithEnv(env))
	return actor, sender, err
}

func successfulCreateUpdate(label string) resource_update.PersistResourceUpdate {
	return resource_update.PersistResourceUpdate{
		CommandID:         "cmd-" + label,
		ResourceOperation: resource_update.OperationCreate,
		PluginOperation:   resource.OperationCreate,
		ResourceUpdate: resource_update.ResourceUpdate{
			DesiredState: pkgmodel.Resource{
				Label:      label,
				Type:       "FakeAWS::S3::Bucket",
				Properties: json.RawMessage(`{"foo":"bar"}`),
				Stack:      "test-stack",
				Ksuid:      util.NewID(),
			},
			ResourceTarget: pkgmodel.Target{Label: "test-target", Namespace: "aws"},
			State:          resource_update.ResourceUpdateStateSuccess,
			StackLabel:     "test-stack",
			ProgressResult: []plugin.TrackedProgress{{
				ProgressResult: resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					NativeID:           "native-" + label,
					ResourceProperties: json.RawMessage(`{"foo":"bar"}`),
				},
				ResourceType: "FakeAWS::S3::Bucket",
				StartTs:      util.TimeNow(),
				ModifiedTs:   util.TimeNow(),
				Attempts:     1,
			}},
		},
	}
}

// A load for a resource that does not exist is a request-scoped failure: the
// caller must get an answer and the persister must keep serving. Terminating
// instead kills every request queued in the mailbox.
func TestResourcePersister_LoadMiss_RepliesAndStaysAlive(t *testing.T) {
	persister, sender, ds, err := newResourcePersisterForTest(t)
	require.NoError(t, err)

	result := persister.Call(sender, messages.LoadResource{
		ResourceURI: pkgmodel.NewFormaeURI(util.NewID(), ""),
	})

	require.NoError(t, result.Error, "a load miss must not terminate the persister")
	loadRes, ok := result.Response.(messages.LoadResourceResult)
	require.True(t, ok, "the caller must receive a typed reply, got %T", result.Response)
	assert.Contains(t, loadRes.Error, "not found")

	// The same actor instance must keep serving requests.
	_, err = ds.CreateTarget(&pkgmodel.Target{Label: "test-target", Namespace: "aws"})
	require.NoError(t, err)
	next := persister.Call(sender, successfulCreateUpdate("after-miss"))
	require.NoError(t, next.Error, "the persister must still serve after a failed request")
	persistRes, ok := next.Response.(resource_update.PersistResourceUpdateResult)
	require.True(t, ok, "expected a typed persist reply, got %T", next.Response)
	assert.Empty(t, persistRes.Error, "a valid request after a failure must succeed")
}

// A write the datastore rejects (the reaped/incarnation guard) must be
// answered with the rejection, not terminate the persister.
func TestResourcePersister_RejectedWrite_RepliesAndStaysAlive(t *testing.T) {
	real, err := newTestDatastore()
	require.NoError(t, err)
	_, err = real.CreateTarget(&pkgmodel.Target{Label: "test-target", Namespace: "aws"})
	require.NoError(t, err)

	persister, sender, err := newPersisterWithStore(t, &rejectingStore{Datastore: real})
	require.NoError(t, err)

	result := persister.Call(sender, successfulCreateUpdate("rejected"))

	require.NoError(t, result.Error, "a rejected write must not terminate the persister")
	persistRes, ok := result.Response.(resource_update.PersistResourceUpdateResult)
	require.True(t, ok, "the caller must receive a typed reply, got %T", result.Response)
	assert.Contains(t, persistRes.Error, "rejected by reaped/incarnation guard")

	// A read on the same actor instance must still be served.
	load := persister.Call(sender, messages.LoadResource{
		ResourceURI: pkgmodel.NewFormaeURI(util.NewID(), ""),
	})
	require.NoError(t, load.Error, "the persister must still serve after a rejected write")
	loadRes, ok := load.Response.(messages.LoadResourceResult)
	require.True(t, ok, "expected a typed load reply, got %T", load.Response)
	assert.NotEmpty(t, loadRes.Error, "the load miss is itself answered, proving the actor is alive")
}

// An unknown request type is a protocol bug, not a request-scoped outcome:
// the error return keeps the meaning ergo assigns to it and terminates the
// actor, so supervision surfaces the defect instead of a reply masking it.
func TestResourcePersister_UnknownRequestType_Terminates(t *testing.T) {
	persister, sender, _, err := newResourcePersisterForTest(t)
	require.NoError(t, err)

	type notARequest struct{}
	result := persister.Call(sender, notARequest{})
	require.Error(t, result.Error, "an unknown request type must terminate the actor")
}
