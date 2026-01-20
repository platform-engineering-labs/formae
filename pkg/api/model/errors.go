// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type APIError string

const (
	ConflictingCommands          APIError = "ConflictingCommands"
	PatchRejected                APIError = "PatchRejected"
	ReconcileRejected            APIError = "ReconcileRejected"
	CyclesDetected               APIError = "CyclesDetected"
	TargetAlreadyExists          APIError = "TargetAlreadyExists"
	ReferencedResourcesNotFound  APIError = "ReferencedResourcesNotFound"
	RequiredFieldMissingOnCreate APIError = "RequiredFieldMissingOnCreate"
	StackReferenceNotFound       APIError = "StackReferenceNotFound"
	TargetReferenceNotFound      APIError = "TargetReferenceNotFound"
	InvalidQuery                 APIError = "InvalidQueryError"
)

type ErrorResponse[T any] struct {
	ErrorType APIError `json:"error"`
	Data      T        `json:"data"`
}

// Error allows ErrorResponse satisfy the error interface
func (e ErrorResponse[T]) Error() string {
	return string(e.ErrorType)
}

type FormaConflictingCommandsError struct {
	ConflictingCommands []Command `json:"ConflictingCommands"`
}

func (e FormaConflictingCommandsError) Error() string {
	return "conflicting resource commands detected"
}

type FormaReconcileRejectedError struct {
	ModifiedStacks map[string]ModifiedStack `json:"ModifiedStacks"`
}

func (e FormaReconcileRejectedError) Error() string {
	return "forma rejected because the stack has been modified since the last reconcile"
}

type ResourceModification struct {
	Stack     string `json:"Stack"`
	Type      string `json:"Type"`
	Label     string `json:"Label"`
	Operation string `json:"Operation"`
}

type ModifiedStack struct {
	ModifiedResources []ResourceModification `json:"ModifiedResources"`
}

type FormaCyclesDetectedError struct{}

func (e FormaCyclesDetectedError) Error() string {
	return "forma contains cycles"
}

type FormaPatchRejectedError struct {
	UnknownStacks []*pkgmodel.Stack `json:"UnknownStacks"`
}

func (e FormaPatchRejectedError) Error() string {
	return "forma command rejected because an unknown stack cannot be patched"
}

type FormaReferencedResourcesNotFoundError struct {
	MissingResources []*pkgmodel.Resource `json:"MissingResources"`
}

func (e FormaReferencedResourcesNotFoundError) Error() string {
	return "forma rejected because one or more resolvables were not found"
}

type TargetAlreadyExistsError struct {
	TargetLabel       string          `json:"TargetLabel"`
	MismatchType      string          `json:"MismatchType"` // namespace | config
	ExistingNamespace string          `json:"ExistingNamespace,omitempty"`
	FormaNamespace    string          `json:"FormaNamespace,omitempty"`
	ExistingConfig    json.RawMessage `json:"ExistingConfig,omitempty"`
	FormaConfig       json.RawMessage `json:"FormaConfig,omitempty"`
}

func (e TargetAlreadyExistsError) Error() string {
	switch e.MismatchType {
	case "namespace":
		return fmt.Sprintf("target '%s' namespace mismatch: existing='%s', forma='%s'",
			e.TargetLabel, e.ExistingNamespace, e.FormaNamespace)
	case "config":
		return fmt.Sprintf("target '%s' has different configuration than specified in forma", e.TargetLabel)
	default:
		return fmt.Sprintf("target '%s' already exists", e.TargetLabel)
	}
}

type RequiredFieldMissingOnCreateError struct {
	MissingFields []string `json:"MissingFields"`
	Stack         string   `json:"Stack"`
	Label         string   `json:"Label"`
	Type          string   `json:"Type"`
}

func (e RequiredFieldMissingOnCreateError) Error() string {
	if len(e.MissingFields) == 1 {
		return fmt.Sprintf("resource %s (type: %s, stack: %s) cannot be created - missing required field: %s",
			e.Label, e.Type, e.Stack, e.MissingFields[0])
	}
	return fmt.Sprintf("resource %s (type: %s, stack: %s) cannot be created - missing required fields: %v",
		e.Label, e.Type, e.Stack, e.MissingFields)
}

type InvalidQueryError struct {
	Reason string `json:"Reason"`
}

func (e InvalidQueryError) Error() string {
	return fmt.Sprintf("The provided query is invalid: %s", e.Reason)
}

type StackReferenceNotFoundError struct {
	StackLabel string `json:"StackLabel"`
}

func (e StackReferenceNotFoundError) Error() string {
	return fmt.Sprintf("stack res provided: %s does not exist", e.StackLabel)
}

type TargetReferenceNotFoundError struct {
	TargetLabel string `json:"TargetLabel"`
}

func (e TargetReferenceNotFoundError) Error() string {
	return fmt.Sprintf("target %s does not exist in existing targets and added targets", e.TargetLabel)
}
