// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package helper

import (
	"errors"

	cctypes "github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	"github.com/aws/smithy-go"
)

// HandleCloudControlError checks if the provided error is a known AWS Cloud Control API exception
// and returns the corresponding HandlerErrorCode, a boolean indicating if it was identified,
// and the original error for further processing if needed.
// E.g. TypeNotFoundException is mapped to NotFound
func HandleCloudControlError(err error) (cctypes.HandlerErrorCode, bool) {
	if err == nil {
		return "", false
	}

	var opErr *smithy.OperationError
	if !errors.As(err, &opErr) {
		// Not an AWS operation error
		return "", false
	}

	underlyingErr := opErr.Unwrap()
	if underlyingErr == nil {
		// OperationError without an underlying cause, return original
		return "", false
	}

	// --- Check for specific Cloud Control API exception types ---
	var alreadyExists *cctypes.AlreadyExistsException
	var clientTokenConflict *cctypes.ClientTokenConflictException
	var concurrentModification *cctypes.ConcurrentModificationException
	var concurrentOperation *cctypes.ConcurrentOperationException
	var generalService *cctypes.GeneralServiceException
	var handlerFailure *cctypes.HandlerFailureException
	var handlerInternalFailure *cctypes.HandlerInternalFailureException
	var invalidCredentials *cctypes.InvalidCredentialsException
	var invalidRequest *cctypes.InvalidRequestException
	var networkFailure *cctypes.NetworkFailureException
	var notStabilized *cctypes.NotStabilizedException
	var notUpdatable *cctypes.NotUpdatableException
	var privateType *cctypes.PrivateTypeException
	var requestTokenNotFound *cctypes.RequestTokenNotFoundException
	var resourceConflict *cctypes.ResourceConflictException
	var resourceNotFound *cctypes.ResourceNotFoundException
	var serviceInternalError *cctypes.ServiceInternalErrorException
	var serviceLimitExceeded *cctypes.ServiceLimitExceededException
	var throttling *cctypes.ThrottlingException
	var typeNotFound *cctypes.TypeNotFoundException
	var unsupportedAction *cctypes.UnsupportedActionException

	switch {
	case errors.As(underlyingErr, &resourceNotFound):
		return cctypes.HandlerErrorCodeNotFound, true
	case errors.As(underlyingErr, &alreadyExists):
		return cctypes.HandlerErrorCodeAlreadyExists, true
	case errors.As(underlyingErr, &clientTokenConflict):
		// Often maps to ResourceConflict or InvalidRequest
		return cctypes.HandlerErrorCodeResourceConflict, true
	case errors.As(underlyingErr, &concurrentModification):
		// Often maps to ResourceConflict or Throttling
		return cctypes.HandlerErrorCodeResourceConflict, true
	case errors.As(underlyingErr, &concurrentOperation):
		// Often maps to ResourceConflict or Throttling
		return cctypes.HandlerErrorCodeResourceConflict, true
	case errors.As(underlyingErr, &generalService):
		return cctypes.HandlerErrorCodeGeneralServiceException, true
	case errors.As(underlyingErr, &handlerFailure):
		// Often maps to InternalFailure or GeneralServiceException
		return cctypes.HandlerErrorCodeInternalFailure, true
	case errors.As(underlyingErr, &handlerInternalFailure):
		return cctypes.HandlerErrorCodeInternalFailure, true
	case errors.As(underlyingErr, &invalidCredentials):
		return cctypes.HandlerErrorCodeInvalidCredentials, true
	case errors.As(underlyingErr, &invalidRequest):
		return cctypes.HandlerErrorCodeInvalidRequest, true
	case errors.As(underlyingErr, &networkFailure):
		return cctypes.HandlerErrorCodeNetworkFailure, true
	case errors.As(underlyingErr, &notStabilized):
		return cctypes.HandlerErrorCodeNotStabilized, true
	case errors.As(underlyingErr, &notUpdatable):
		return cctypes.HandlerErrorCodeNotUpdatable, true
	case errors.As(underlyingErr, &privateType):
		// Often maps to AccessDenied or InvalidRequest
		return cctypes.HandlerErrorCodeAccessDenied, true
	case errors.As(underlyingErr, &requestTokenNotFound):
		// Often maps to NotFound or InvalidRequest
		return cctypes.HandlerErrorCodeNotFound, true
	case errors.As(underlyingErr, &resourceConflict):
		return cctypes.HandlerErrorCodeResourceConflict, true
	case errors.As(underlyingErr, &serviceInternalError):
		return cctypes.HandlerErrorCodeServiceInternalError, true
	case errors.As(underlyingErr, &serviceLimitExceeded):
		return cctypes.HandlerErrorCodeServiceLimitExceeded, true
	case errors.As(underlyingErr, &throttling):
		return cctypes.HandlerErrorCodeThrottling, true
	case errors.As(underlyingErr, &typeNotFound):
		// Often maps to NotFound or InvalidRequest
		return cctypes.HandlerErrorCodeNotFound, true
	case errors.As(underlyingErr, &unsupportedAction):
		return cctypes.HandlerErrorCodeInvalidRequest, true
	default:
		// Underlying error type is not one of the specifically handled Cloud Control exceptions
		return "", false // Indicate it wasn't a known CC type
	}
}
