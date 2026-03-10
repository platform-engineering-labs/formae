// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/masterminds/semver"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

// TestPlugin is a minimal resource plugin for blackbox property-based testing.
type TestPlugin struct {
	cloudState      *CloudState
	injections      *InjectionState
	responseQueue   *ResponseQueue
	opLog           *OperationLog
	nativeIDCounter atomic.Int64
	gate            <-chan struct{}
}

// Compile-time checks to satisfy protocol
var _ plugin.Plugin = (*TestPlugin)(nil)
var _ plugin.ResourcePlugin = (*TestPlugin)(nil)

func (p *TestPlugin) Name() string {
	return "test-plugin"
}

func (p *TestPlugin) Version() *semver.Version {
	return semver.MustParse("0.0.1")
}

func (p *TestPlugin) Type() plugin.Type {
	return plugin.Resource
}

func (p *TestPlugin) Namespace() string {
	return "Test"
}

func (p *TestPlugin) SupportedResources() []plugin.ResourceDescriptor {
	return []plugin.ResourceDescriptor{
		{
			Type:         "Test::Generic::Resource",
			Discoverable: true,
		},
		{
			Type:         "Test::Generic::ChildResource",
			Discoverable: true,
			ParentResourceTypesWithMappingProperties: map[string][]plugin.ListParameter{
				"Test::Generic::Resource": {
					{ParentProperty: "Name", ListProperty: "ParentId", QueryPath: "$.Name"},
				},
			},
		},
		{
			Type:         "Test::Generic::GrandchildResource",
			Discoverable: true,
			ParentResourceTypesWithMappingProperties: map[string][]plugin.ListParameter{
				"Test::Generic::ChildResource": {
					{ParentProperty: "Name", ListProperty: "ParentId", QueryPath: "$.Name"},
				},
			},
		},
	}
}

func (p *TestPlugin) RateLimit() plugin.RateLimitConfig {
	return plugin.RateLimitConfig{
		Scope:                            plugin.RateLimitScopeNamespace,
		MaxRequestsPerSecondForNamespace: 100,
	}
}

func (p *TestPlugin) SchemaForResourceType(resourceType string) (model.Schema, error) {
	switch resourceType {
	case "Test::Generic::Resource":
		return model.Schema{
			Identifier: "Name",
			Fields:     []string{"Name", "Value", "SetTags", "EntityTags", "OrderedItems"},
			Hints: map[string]model.FieldHint{
				"EntityTags": {
					UpdateMethod: model.FieldUpdateMethodEntitySet,
					IndexField:   "Key",
				},
				"OrderedItems": {
					UpdateMethod: model.FieldUpdateMethodArray,
				},
			},
		}, nil
	case "Test::Generic::ChildResource", "Test::Generic::GrandchildResource":
		return model.Schema{
			Identifier: "Name",
			Fields:     []string{"Name", "ParentId", "Value"},
		}, nil
	default:
		return model.Schema{}, fmt.Errorf("unknown resource type: %s", resourceType)
	}
}

func (p *TestPlugin) Create(_ context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	<-p.gate
	// Check response queue first (per-resource programmed responses).
	if p.responseQueue != nil {
		if step := p.responseQueue.CheckCreate(request.Properties); step != nil {
			if step.ErrorCode != "" {
				p.recordOp("Create", request.ResourceType, "")
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						OperationStatus: resource.OperationStatusFailure,
						ErrorCode:       resource.OperationErrorCode(step.ErrorCode),
						StatusMessage:   "injected error",
					},
				}, nil
			}
			// ErrorCode empty -> proceed to normal success path below.
		}
	}

	// Fall through to existing global injection logic.
	if p.injections != nil {
		if delay := p.injections.CheckLatency("Create", request.ResourceType); delay > 0 {
			time.Sleep(delay)
		}
		if err := p.injections.CheckError("Create", request.ResourceType); err != nil {
			p.recordOp("Create", request.ResourceType, "")
			return nil, err
		}
	}

	nativeID := fmt.Sprintf("test-%d", p.nativeIDCounter.Add(1))
	p.cloudState.Put(nativeID, request.ResourceType, string(request.Properties))
	p.recordOp("Create", request.ResourceType, nativeID)

	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationCreate,
			OperationStatus:    resource.OperationStatusSuccess,
			NativeID:           nativeID,
			ResourceProperties: request.Properties,
		},
	}, nil
}

func (p *TestPlugin) Read(_ context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	<-p.gate
	// Check response queue first (per-resource programmed responses).
	if p.responseQueue != nil {
		if step := p.responseQueue.CheckRead(request.NativeID); step != nil {
			if step.ErrorCode != "" {
				p.recordOp("Read", request.ResourceType, request.NativeID)
				return &resource.ReadResult{
					ErrorCode: resource.OperationErrorCode(step.ErrorCode),
				}, nil
			}
			// ErrorCode empty -> proceed to normal success path below.
		}
	}

	// Fall through to existing global injection logic.
	if p.injections != nil {
		if delay := p.injections.CheckLatency("Read", request.ResourceType); delay > 0 {
			time.Sleep(delay)
		}
		if err := p.injections.CheckError("Read", request.ResourceType); err != nil {
			p.recordOp("Read", request.ResourceType, request.NativeID)
			return nil, err
		}
	}

	entry, ok := p.cloudState.Get(request.NativeID)
	if !ok {
		p.recordOp("Read", request.ResourceType, request.NativeID)
		return &resource.ReadResult{
			ErrorCode: resource.OperationErrorCodeNotFound,
		}, nil
	}

	p.recordOp("Read", entry.ResourceType, request.NativeID)
	return &resource.ReadResult{
		ResourceType: entry.ResourceType,
		Properties:   entry.Properties,
	}, nil
}

func (p *TestPlugin) Update(_ context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	<-p.gate
	// Check response queue first (per-resource programmed responses).
	if p.responseQueue != nil {
		if step := p.responseQueue.CheckUpdate(request.NativeID); step != nil {
			if step.ErrorCode != "" {
				p.recordOp("Update", request.ResourceType, request.NativeID)
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						OperationStatus: resource.OperationStatusFailure,
						ErrorCode:       resource.OperationErrorCode(step.ErrorCode),
						StatusMessage:   "injected error",
					},
				}, nil
			}
			// ErrorCode empty -> proceed to normal success path below.
		}
	}

	// Fall through to existing global injection logic.
	if p.injections != nil {
		if delay := p.injections.CheckLatency("Update", request.ResourceType); delay > 0 {
			time.Sleep(delay)
		}
		if err := p.injections.CheckError("Update", request.ResourceType); err != nil {
			p.recordOp("Update", request.ResourceType, request.NativeID)
			return nil, err
		}
	}

	p.cloudState.Put(request.NativeID, request.ResourceType, string(request.DesiredProperties))
	p.recordOp("Update", request.ResourceType, request.NativeID)

	return &resource.UpdateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationUpdate,
			OperationStatus:    resource.OperationStatusSuccess,
			NativeID:           request.NativeID,
			ResourceProperties: request.DesiredProperties,
		},
	}, nil
}

func (p *TestPlugin) Delete(_ context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	<-p.gate
	// Check response queue first (per-resource programmed responses).
	if p.responseQueue != nil {
		if step := p.responseQueue.CheckDelete(request.NativeID); step != nil {
			if step.ErrorCode != "" {
				p.recordOp("Delete", request.ResourceType, request.NativeID)
				return &resource.DeleteResult{
					ProgressResult: &resource.ProgressResult{
						OperationStatus: resource.OperationStatusFailure,
						ErrorCode:       resource.OperationErrorCode(step.ErrorCode),
						StatusMessage:   "injected error",
					},
				}, nil
			}
			// ErrorCode empty -> proceed to normal success path below.
		}
	}

	// Fall through to existing global injection logic.
	if p.injections != nil {
		if delay := p.injections.CheckLatency("Delete", request.ResourceType); delay > 0 {
			time.Sleep(delay)
		}
		if err := p.injections.CheckError("Delete", request.ResourceType); err != nil {
			p.recordOp("Delete", request.ResourceType, request.NativeID)
			return nil, err
		}
	}

	p.cloudState.Delete(request.NativeID)
	p.recordOp("Delete", request.ResourceType, request.NativeID)

	return &resource.DeleteResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationDelete,
			OperationStatus: resource.OperationStatusSuccess,
			NativeID:        request.NativeID,
		},
	}, nil
}

func (p *TestPlugin) Status(_ context.Context, _ *resource.StatusRequest) (*resource.StatusResult, error) {
	return nil, nil
}

func (p *TestPlugin) List(_ context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	<-p.gate
	if p.injections != nil {
		if delay := p.injections.CheckLatency("List", request.ResourceType); delay > 0 {
			time.Sleep(delay)
		}
		if err := p.injections.CheckError("List", request.ResourceType); err != nil {
			p.recordOp("List", request.ResourceType, "")
			return nil, err
		}
	}

	var ids []string
	if len(request.AdditionalProperties) > 0 {
		for field, value := range request.AdditionalProperties {
			ids = p.cloudState.ListNativeIDsFiltered(request.ResourceType, field, value)
			break
		}
	} else {
		ids = p.cloudState.ListNativeIDs(request.ResourceType)
	}
	if ids == nil {
		ids = []string{}
	}

	p.recordOp("List", request.ResourceType, "")
	return &resource.ListResult{
		NativeIDs: ids,
	}, nil
}

func (p *TestPlugin) DiscoveryFilters() []plugin.MatchFilter {
	return nil
}

func (p *TestPlugin) LabelConfig() plugin.LabelConfig {
	return plugin.LabelConfig{
		DefaultQuery:      "$.Name",
		ResourceOverrides: map[string]string{},
	}
}

// recordOp records an operation in the operation log, if one is configured.
func (p *TestPlugin) recordOp(operation, resourceType, nativeID string) {
	if p.opLog != nil {
		p.opLog.Record(testcontrol.OperationLogEntry{
			Operation:    operation,
			ResourceType: resourceType,
			NativeID:     nativeID,
			Timestamp:    time.Now(),
		})
	}
}