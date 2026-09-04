// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// errPersistRefused is the shape a persist failure takes at the updater: the
// call to the resource persister does not complete.
var errPersistRefused = errors.New("persister call timed out")

// persistFailingProcess fails every proc.Call, so the persist that follows a
// successful plugin operation cannot complete. Sends still succeed, matching a
// persister that accepts async messages but cannot commit writes.
type persistFailingProcess struct {
	*stubUpdaterProcess
	log *capturingLog
}

func (p *persistFailingProcess) Log() gen.Log { return p.log }

func (p *persistFailingProcess) Call(_ any, _ any) (any, error) {
	return nil, errPersistRefused
}

func newPersistFailingProcess() *persistFailingProcess {
	return &persistFailingProcess{stubUpdaterProcess: &stubUpdaterProcess{}, log: &capturingLog{}}
}

// A create the provider completed but formae could not persist leaves a live
// cloud object with no record: destroy will not find it and a re-apply can
// collide with it. The failure must carry a reason naming the surviving
// object, not the empty ErrorMessage a reason-less failure produces when
// every recorded plugin progress is a success.
func TestHandleProgressUpdate_PersistFailureAfterCreate_NamesSurvivingObject(t *testing.T) {
	const nativeID = "arn:aws:cloudfront::000000000000:response-headers-policy/abc123"

	ru := &ResourceUpdate{
		Operation: OperationCreate,
		DesiredState: pkgmodel.Resource{
			Label:      "edge-policy",
			Type:       "AWS::CloudFront::ResponseHeadersPolicy",
			Stack:      "web",
			Properties: json.RawMessage(`{"Name":"edge-policy"}`),
		},
		ResourceTarget: pkgmodel.Target{Label: "us-east-1"},
	}
	data := ResourceUpdateData{resourceUpdate: ru, commandID: "cmd-1"}

	progress := plugin.TrackedProgress{
		ProgressResult: resource.ProgressResult{
			Operation:          resource.OperationCreate,
			OperationStatus:    resource.OperationStatusSuccess,
			NativeID:           nativeID,
			ResourceProperties: json.RawMessage(`{"Name":"edge-policy"}`),
		},
	}

	proc := newPersistFailingProcess()
	state, _, _, err := handleProgressUpdate(gen.PID{}, StateCreating, data, progress, proc)

	require.NoError(t, err)
	require.Equal(t, StateFinishedWithError, state, "a failed persist must fail the resource update")
	require.Contains(t, strings.Join(proc.log.all(), "\n"), "failed to persist resource update",
		"the intended failure site must be the one that fired")

	message := ru.MostRecentFailureMessage()
	require.NotEmpty(t, message, "a persist failure after a successful cloud write must surface a reason")
	assert.Contains(t, message, nativeID, "the reason must name the surviving cloud object")
	assert.NotContains(t, message, errPersistRefused.Error(),
		"the underlying persist error stays in the agent log, not the operator-facing reason")
}

// Recording a successful create's progress can itself fail (the provider's
// echo does not merge), which strands the cloud object exactly the way a
// failed persist does: created, unrecorded, invisible to destroy. The same
// reason contract applies.
func TestHandleProgressUpdate_RecordProgressFailureAfterCreate_NamesSurvivingObject(t *testing.T) {
	const nativeID = "arn:aws:cloudfront::000000000000:response-headers-policy/def456"

	ru := &ResourceUpdate{
		Operation: OperationCreate,
		DesiredState: pkgmodel.Resource{
			Label:      "edge-policy",
			Type:       "AWS::CloudFront::ResponseHeadersPolicy",
			Stack:      "web",
			Properties: json.RawMessage(`{"Name":"edge-policy"}`),
		},
		ResourceTarget: pkgmodel.Target{Label: "us-east-1"},
	}
	data := ResourceUpdateData{resourceUpdate: ru, commandID: "cmd-1"}

	progress := plugin.TrackedProgress{
		ProgressResult: resource.ProgressResult{
			Operation:       resource.OperationCreate,
			OperationStatus: resource.OperationStatusSuccess,
			NativeID:        nativeID,
			// An echo that cannot be merged into the recorded properties.
			ResourceProperties: json.RawMessage(`{"Name":`),
		},
	}

	proc := newPersistFailingProcess()
	state, _, _, err := handleProgressUpdate(gen.PID{}, StateCreating, data, progress, proc)

	require.NoError(t, err)
	require.Equal(t, StateFinishedWithError, state, "a failed progress record must fail the resource update")

	message := ru.MostRecentFailureMessage()
	require.NotEmpty(t, message, "a progress-record failure after a successful cloud write must surface a reason")
	assert.Contains(t, message, nativeID, "the reason must name the surviving cloud object")
}

// Every operation the persist can follow gets its own consequence: only a
// create leaves an unmanaged object behind, an update leaves a stale record,
// a delete leaves a lingering record, and a read loses only the observation.
func TestPersistFailureReason_Categories(t *testing.T) {
	const nativeID = "native-1"

	create := persistFailureReason(resource.OperationCreate, nativeID)
	assert.Contains(t, create, nativeID, "a create's reason must name the surviving object")
	assert.Contains(t, create, "not under formae's management")

	createNoID := persistFailureReason(resource.OperationCreate, "")
	assert.NotContains(t, createNoID, `""`, "a missing native id must not render as an empty quote")
	assert.Contains(t, createNoID, "not under formae's management")

	update := persistFailureReason(resource.OperationUpdate, nativeID)
	assert.Contains(t, update, "stale")
	assert.NotContains(t, update, "not under formae's management", "an update's record still exists")

	del := persistFailureReason(resource.OperationDelete, nativeID)
	assert.Contains(t, del, "still lists it")

	read := persistFailureReason(resource.OperationRead, nativeID)
	assert.Contains(t, read, "next synchronization will retry")
	assert.NotContains(t, read, nativeID, "a read's reason has no surviving object to name")
}
