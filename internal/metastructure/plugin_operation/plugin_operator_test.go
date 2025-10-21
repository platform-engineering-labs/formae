// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit
// +build unit

package plugin_operation

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

func TestPluginOperator_TerminatesOnShutdownMessage(t *testing.T) {
	operator, sender, err := newPluginOperatorForTest(t, &plugin.ResourcePluginOverrides{}, StateFinishedSuccessfully)
	assert.NoError(t, err, "Failed to spawn plugin operator")

	operator.SendMessage(sender, Shutdown{})

	assert.True(t, operator.IsTerminated())
	assert.Equal(t, gen.TerminateReasonNormal, operator.TerminationReason())
}

func TestPluginOperator_ReadReturnsSuccessfulResultAndShutsDownOperator(t *testing.T) {
	overrides := &plugin.ResourcePluginOverrides{
		Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
			return &resource.ReadResult{
				ResourceType: "FakeAWS::S3::Bucket",
				Properties:   `{"name": "test-bucket", "region": "us-west-2"}`,
				ErrorCode:    "",
			}, nil
		},
	}

	operator, sender, err := newPluginOperatorForTest(t, overrides)
	assert.NoError(t, err, "Failed to spawn plugin operator")

	res := operator.Call(sender, ReadResource{
		Namespace: "FakeAWS",
		NativeID:  "test-native-id",
		ExistingResource: model.Resource{
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name": "test-bucket", "region": "us-west-2"}`),
		},
		Target: model.Target{Label: "test-target-label", Namespace: "test-target-namespace", Config: nil},
	})
	assert.NoError(t, res.Error)

	progress := res.Response.(resource.ProgressResult)
	assert.Equal(t, resource.OperationRead, progress.Operation)
	assert.Equal(t, resource.OperationStatusSuccess, progress.OperationStatus)
	assert.Equal(t, 1, progress.Attempts)
	assert.Equal(t, json.RawMessage(`{"name": "test-bucket", "region": "us-west-2"}`), progress.ResourceProperties)
	assert.Equal(t, resource.OperationErrorCodeNotSet, progress.ErrorCode)

	operator.ShouldSend().To(operator.PID()).Message(Shutdown{}).Once().Assert()
}

func TestPluginOperator_CreateReturnsSuccessfulResultAndShutsDownOperator(t *testing.T) {
	overrides := &plugin.ResourcePluginOverrides{
		Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
			return &resource.CreateResult{
				ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					NativeID:           "test-native-id",
					RequestID:          "1234",
					ResourceType:       request.Resource.Type,
					ResourceProperties: json.RawMessage(`{"name": "test-bucket", "region": "us-west-2"}`),
				},
			}, nil
		},
	}

	operator, sender, err := newPluginOperatorForTest(t, overrides)
	assert.NoError(t, err, "Failed to spawn plugin operator")

	res := operator.Call(sender, CreateResource{
		Namespace: "FakeAWS",
		Resource: model.Resource{
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name": "test-bucket", "region": "us-west-2"}`),
		},
		Target: model.Target{
			Label:     "test-target-label",
			Namespace: "test-target-namespace",
			Config:    nil,
		},
	})

	progress := res.Response.(resource.ProgressResult)
	assert.Equal(t, resource.OperationCreate, progress.Operation)
	assert.Equal(t, resource.OperationStatusSuccess, progress.OperationStatus)
	assert.Equal(t, "FakeAWS::S3::Bucket", progress.ResourceType)
	assert.Equal(t, "test-native-id", progress.NativeID)
	assert.Equal(t, "1234", progress.RequestID)
	assert.Equal(t, json.RawMessage(`{"name": "test-bucket", "region": "us-west-2"}`), progress.ResourceProperties)

	operator.ShouldSend().To(operator.PID()).Message(Shutdown{}).Once().Assert()
}

func TestPluginOperator_UpdateReturnsSuccessfulResultAndShutsDownOperator(t *testing.T) {
	overrides := &plugin.ResourcePluginOverrides{
		Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
			return &resource.UpdateResult{
				ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationUpdate,
					OperationStatus:    resource.OperationStatusSuccess,
					NativeID:           "test-native-id",
					RequestID:          "1234",
					ResourceType:       request.Resource.Type,
					ResourceProperties: json.RawMessage(`{"name": "updated-bucket", "region": "us-west-2"}`),
				},
			}, nil
		},
	}

	operator, sender, err := newPluginOperatorForTest(t, overrides)
	assert.NoError(t, err, "Failed to spawn plugin operator")

	res := operator.Call(sender, UpdateResource{
		Namespace: "FakeAWS",
		NativeID:  "test-native-id",
		Resource: model.Resource{
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name": "test-bucket", "region": "us-west-2"}`),
		},
		PatchDocument: `{"op": "replace", "path": "/name", "value": "updated-bucket"}`,
		Target: model.Target{
			Label:     "test-target-label",
			Namespace: "test-target-namespace",
			Config:    nil,
		},
	})

	progress := res.Response.(resource.ProgressResult)
	assert.Equal(t, resource.OperationUpdate, progress.Operation)
	assert.Equal(t, resource.OperationStatusSuccess, progress.OperationStatus)
	assert.Equal(t, "FakeAWS::S3::Bucket", progress.ResourceType)
	assert.Equal(t, "test-native-id", progress.NativeID)
	assert.Equal(t, "1234", progress.RequestID)
	assert.Equal(t, json.RawMessage(`{"name": "updated-bucket", "region": "us-west-2"}`), progress.ResourceProperties)

	operator.ShouldSend().To(operator.PID()).Message(Shutdown{}).Once().Assert()
}

func TestPluginOperator_DeleteReturnsSuccessfulResultAndShutsDownOperator(t *testing.T) {
	overrides := &plugin.ResourcePluginOverrides{
		Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
			return &resource.DeleteResult{
				ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusSuccess,
					NativeID:        *request.NativeID,
					RequestID:       "1234",
					ResourceType:    request.ResourceType,
				},
			}, nil
		},
	}

	operator, sender, err := newPluginOperatorForTest(t, overrides)
	assert.NoError(t, err, "Failed to spawn plugin operator")

	res := operator.Call(sender, DeleteResource{
		Namespace:    "FakeAWS",
		NativeID:     "test-native-id",
		ResourceType: "FakeAWS::S3::Bucket",
		Metadata:     json.RawMessage("test-metadata"),
		Target:       model.Target{Label: "test-target-label", Namespace: "test-target-namespace", Config: nil},
	})

	progress := res.Response.(resource.ProgressResult)
	assert.Equal(t, resource.OperationDelete, progress.Operation)
	assert.Equal(t, resource.OperationStatusSuccess, progress.OperationStatus)
	assert.Equal(t, "test-native-id", progress.NativeID)
	assert.Equal(t, "1234", progress.RequestID)

	operator.ShouldSend().To(operator.PID()).Message(Shutdown{}).Once().Assert()
}

func TestPluginOperator_ReportsProgressAndShutsDownOperatorAfterSuccessfulStatusCheck(t *testing.T) {
	overrides := &plugin.ResourcePluginOverrides{
		Status: func(request *resource.StatusRequest) (*resource.StatusResult, error) {
			return &resource.StatusResult{
				ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          "1234",
					NativeID:           "test-native-id",
					ResourceType:       request.ResourceType,
					ResourceProperties: json.RawMessage(`{"name": "test-bucket", "region": "us-west-2"}`),
				},
			}, nil
		},
	}

	operator, sender, err := newPluginOperatorForTest(t, overrides, StateWaitingForResource)
	assert.NoError(t, err, "Failed to spawn plugin operator")

	operator.SendMessage(sender, CheckStatus{
		Namespace:    "FakeAWS",
		RequestID:    "test-native-id",
		ResourceType: "FakeAWS::S3::Bucket",
		Metadata:     json.RawMessage("test-metadata"),
		Target:       model.Target{Label: "test-target-label", Namespace: "test-target-namespace", Config: nil},
	})

	operator.ShouldSend().To(sender).MessageMatching(func(msg any) bool {
		progress, ok := msg.(resource.ProgressResult)
		if !ok {
			return false
		}
		return progress.Operation == resource.OperationCreate &&
			progress.OperationStatus == resource.OperationStatusSuccess &&
			progress.RequestID == "1234" &&
			progress.NativeID == "test-native-id" &&
			progress.ResourceType == "FakeAWS::S3::Bucket" &&
			reflect.DeepEqual(progress.ResourceProperties, json.RawMessage(`{"name": "test-bucket", "region": "us-west-2"}`))
	}).Once().Assert()
}

func TestPluginOperator_DoesNotRetryUnrecoverableError(t *testing.T) {
	overrides := &plugin.ResourcePluginOverrides{
		Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
			return &resource.CreateResult{
				ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusFailure,
					ResourceType:    request.Resource.Type,
					ErrorCode:       resource.OperationErrorCodeInvalidCredentials,
				},
			}, nil
		},
	}

	operator, sender, err := newPluginOperatorForTest(t, overrides)
	assert.NoError(t, err, "Failed to spawn plugin operator")

	res := operator.Call(sender, CreateResource{
		Namespace: "FakeAWS",
		Resource: model.Resource{
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name": "test-bucket", "region": "us-west-2"}`),
		},
		Target: model.Target{
			Label:     "test-target-label",
			Namespace: "test-target-namespace",
			Config:    nil,
		},
	})
	assert.NoError(t, res.Error)

	progress := res.Response.(resource.ProgressResult)
	assert.Equal(t, 1, progress.Attempts)
	operator.ShouldNotSend().To(operator.PID()).Message(CreateResource{}).Assert()

	operator.ShouldSend().To(operator.PID()).Message(Shutdown{}).Once().Assert()
}

func TestPluginOperator_RetriesRecoverableError(t *testing.T) {
	overrides := &plugin.ResourcePluginOverrides{
		Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
			return &resource.CreateResult{
				ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusFailure,
					ResourceType:    request.Resource.Type,
					ErrorCode:       resource.OperationErrorCodeNetworkFailure,
				},
			}, nil
		},
	}

	operator, sender, err := newPluginOperatorForTest(t, overrides)
	assert.NoError(t, err, "Failed to spawn plugin operator")

	res := operator.Call(sender, CreateResource{
		Namespace: "FakeAWS",
		Resource: model.Resource{
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name": "test-bucket", "region": "us-west-2"}`),
		},
		Target: model.Target{
			Label:     "test-target-label",
			Namespace: "test-target-namespace",
			Config:    nil,
		},
	})
	assert.NoError(t, res.Error)

	operator.ShouldSend().To(operator.PID()).MessageMatching(func(msg any) bool {
		retry, ok := msg.(Retry)
		if !ok {
			return false
		}
		return retry.ResourceOperation == resource.OperationCreate &&
			reflect.DeepEqual(CreateResource{
				Namespace: "FakeAWS",
				Resource: model.Resource{
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"name": "test-bucket", "region": "us-west-2"}`),
				},
				Target: model.Target{
					Label:     "test-target-label",
					Namespace: "test-target-namespace",
					Config:    nil,
				},
			}, retry.Request)
	}).Once().Assert()

	operator.ShouldNotSend().To(operator.PID()).Message(Shutdown{}).Once().Assert()
}

func TestPluginOperator_SendingStatusCheckInterfaceShouldGetRoutedToTheCorrectHandler(t *testing.T) {
	operator, sender, err := newPluginOperatorForTest(t, &plugin.ResourcePluginOverrides{})
	assert.NoError(t, err, "Failed to spawn plugin operator")

	var StatusCheck = CreateResource{
		Namespace: "FakeAWS",
		Resource: model.Resource{
			Type:       "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{"name": "test-bucket", "region": "us-west-2"}`),
		},
		Target: model.Target{
			Label:     "test-target-label",
			Namespace: "test-target-namespace",
			Config:    nil,
		},
	}
	res := operator.Call(sender, StatusCheck)
	assert.NoError(t, res.Error, "Call to plugin operator failed")
}

func TestPluginOperator_ListPropagatesErrorsFromPlugin(t *testing.T) {
	overrides := &plugin.ResourcePluginOverrides{
		List: func(request *resource.ListRequest) (*resource.ListResult, error) {
			return nil, fmt.Errorf("oops")
		},
	}

	operator, sender, err := newPluginOperatorForTest(t, overrides)
	assert.NoError(t, err, "Failed to spawn plugin operator")

	operator.SendMessage(sender, ListResources{
		Namespace:    "FakeAWS",
		ResourceType: "FakeAWS::S3::Bucket",
		Target:       model.Target{Label: "test-target-label", Namespace: "test-target-namespace", Config: nil},
	})

	operator.ShouldSend().To(sender).MessageMatching(func(msg any) bool {
		listResult, ok := msg.(Listing)
		if !ok {
			return false
		}
		return listResult.Error.Error() == "oops"
	}).Once().Assert()
}

func newPluginOperatorForTest(t *testing.T, overrides *plugin.ResourcePluginOverrides, args ...any) (*unit.TestActor, gen.PID, error) {
	context, _ := testutil.PluginOverridesContext(overrides)

	projectRoot, err := testutil.FindProjectRoot()
	assert.NoError(t, err, "Failed to get project root")
	pluginPath := projectRoot + "/plugins"

	pluginManager := plugin.NewManager(pluginPath)
	pluginManager.Load()

	env := map[gen.Env]any{
		"Context":       context,
		"PluginManager": pluginManager,

		"RetryConfig": model.RetryConfig{
			StatusCheckInterval: 1 * time.Second,
			MaxRetries:          10,
			RetryDelay:          1 * time.Second,
		},
	}

	sender := gen.PID{Node: "test", ID: 100}
	initArgs := []any{sender}
	initArgs = append(initArgs, args...)

	operator, error := unit.Spawn(t, newPluginOperator, unit.WithArgs(initArgs...), unit.WithEnv(env))
	if error != nil {
		return nil, gen.PID{}, error
	}

	return operator, sender, nil
}
