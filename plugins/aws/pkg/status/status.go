// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package status

import (
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func FromProgress(progress *types.ProgressEvent) (resource.Operation, resource.OperationStatus) {
	return FromOperation(progress.Operation), FromOperationStatus(progress.OperationStatus)
}

func FromOperation(operation types.Operation) resource.Operation {
	var result resource.Operation

	switch operation {
	case types.OperationCreate:
		result = resource.OperationCreate
	case types.OperationUpdate:
		result = resource.OperationUpdate
	case types.OperationDelete:
		result = resource.OperationDelete
	}

	return result
}

func FromOperationStatus(operationStatus types.OperationStatus) resource.OperationStatus {
	var result resource.OperationStatus

	switch operationStatus {
	case types.OperationStatusSuccess:
		result = resource.OperationStatusSuccess
	case types.OperationStatusFailed:
		result = resource.OperationStatusFailure
	case types.OperationStatusInProgress:
		result = resource.OperationStatusInProgress
	case types.OperationStatusPending:
		result = resource.OperationStatusPending
	}

	return result
}
