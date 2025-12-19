// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package messages

import (
	"encoding/json"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

type MarkResourceUpdateAsComplete struct {
	CommandID                  string
	ResourceURI                pkgmodel.FormaeURI
	Operation                  types.OperationType // The ResourceUpdate operation (create, delete, update, etc.)
	FinalState                 types.ResourceUpdateState
	ResourceStartTs            time.Time
	ResourceModifiedTs         time.Time
	ResourceProperties         json.RawMessage
	ResourceReadOnlyProperties json.RawMessage
	Version                    string
}

type UpdateResourceProgress struct {
	CommandID                  string
	ResourceURI                pkgmodel.FormaeURI
	Operation                  types.OperationType // The ResourceUpdate operation (create, delete, update, etc.)
	ResourceStartTs            time.Time
	ResourceModifiedTs         time.Time
	ResourceState              types.ResourceUpdateState
	Progress                   resource.ProgressResult
	ResourceProperties         json.RawMessage
	ResourceReadOnlyProperties json.RawMessage
	Version                    string
}

type UpdateTargetStates struct {
	CommandID     string
	TargetUpdates []target_update.TargetUpdate
}
