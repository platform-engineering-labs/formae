// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package prov

import (
	"context"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// Provisioner interface for Azure resources
// Same interface as K8S plugin - all operations per resource type
type Provisioner interface {
	Create(context context.Context, request *resource.CreateRequest) (*resource.CreateResult, error)
	Update(context context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error)
	Delete(context context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error)
	Status(context context.Context, request *resource.StatusRequest) (*resource.StatusResult, error)
	Read(context context.Context, request *resource.ReadRequest) (*resource.ReadResult, error)
	List(context context.Context, request *resource.ListRequest) (*resource.ListResult, error)
}
