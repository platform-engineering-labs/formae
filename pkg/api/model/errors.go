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
	EmptyStackRejected           APIError = "EmptyStackRejected"
	TargetAlreadyExists          APIError = "TargetAlreadyExists"
	TargetReaped                 APIError = "TargetReaped"
	ReferencedResourcesNotFound  APIError = "ReferencedResourcesNotFound"
	ReferencedGeneratorsNotFound APIError = "ReferencedGeneratorsNotFound"
	RequiredFieldMissingOnCreate APIError = "RequiredFieldMissingOnCreate"
	StackReferenceNotFound       APIError = "StackReferenceNotFound"
	TargetReferenceNotFound      APIError = "TargetReferenceNotFound"
	InvalidQuery                 APIError = "InvalidQueryError"
	StackDeletedDuringApply      APIError = "StackDeletedDuringApply"
	ReconcilePolicyRequired      APIError = "ReconcilePolicyRequired"
	NonPortableResources         APIError = "NonPortableResources"
	PluginNotFound               APIError = "PluginNotFound"
	PluginVersionNotFound        APIError = "PluginVersionNotFound"
	PluginDependencyConflict     APIError = "PluginDependencyConflict"
	PluginRepositoryUnreachable  APIError = "PluginRepositoryUnreachable"
	PluginSignatureInvalid       APIError = "PluginSignatureInvalid"
	TargetHasDependents          APIError = "TargetHasDependents"
	ResourceHasDependents        APIError = "ResourceHasDependents"
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
	Stack         string          `json:"Stack"`
	Type          string          `json:"Type"`
	Label         string          `json:"Label"`
	Operation     string          `json:"Operation"`
	PatchDocument json.RawMessage `json:"PatchDocument,omitempty"` // JSON-patch diff between OldProperties and Properties — update ops only
	Properties    json.RawMessage `json:"Properties,omitempty"`    // current (cloud) properties — update ops only
	OldProperties json.RawMessage `json:"OldProperties,omitempty"` // properties at last reconcile — update ops only
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

type FormaEmptyStackRejectedError struct {
	EmptyStacks []string `json:"EmptyStacks"`
}

func (e FormaEmptyStackRejectedError) Error() string {
	return "forma rejected because creating empty stacks is not allowed"
}

type FormaReferencedResourcesNotFoundError struct {
	MissingResources []*pkgmodel.Resource `json:"MissingResources"`
}

func (e FormaReferencedResourcesNotFoundError) Error() string {
	return "forma rejected because one or more resolvables were not found"
}

// FormaReferencedGeneratorsNotFoundError names every $gen that resolved to
// no live generator, or that named an output its generator does not
// produce. A dangling generator reference is a hard error, never silently
// absorbed — PKL cannot reject a $gen naming a generator the forma never
// declares (a bare `local` generator still renders a well-formed envelope),
// so this is the only check standing between such a forma and an apply.
type FormaReferencedGeneratorsNotFoundError struct {
	Missing []pkgmodel.MissingGenerator `json:"Missing"`
}

func (e FormaReferencedGeneratorsNotFoundError) Error() string {
	return "forma rejected because one or more generator references were not found"
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

// TargetReapedError is returned when an apply touches one or more reaped
// targets without re-declaring them. A reaped target has been tombstoned after
// prolonged unreachability; a resource-only or stale apply that references it
// (but does not re-declare the target) would silently resurrect it against a
// target the agent believes is dead. Re-declaring the target in the forma
// recovers it instead — that path is allowed.
type TargetReapedError struct {
	TargetLabels []string `json:"TargetLabels"`
}

func (e TargetReapedError) Error() string {
	if len(e.TargetLabels) == 1 {
		return fmt.Sprintf("target %q is reaped; re-declare it in your forma to recover it before applying resources to it", e.TargetLabels[0])
	}
	return fmt.Sprintf("targets %v are reaped; re-declare them in your forma to recover them before applying resources to them", e.TargetLabels)
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

type StackDeletedDuringApplyError struct {
	StackLabel string `json:"StackLabel"`
}

func (e StackDeletedDuringApplyError) Error() string {
	return fmt.Sprintf("stack %q was deleted during apply setup", e.StackLabel)
}

type ReconcilePolicyRequiredError struct {
	StackLabel string `json:"StackLabel"`
}

func (e ReconcilePolicyRequiredError) Error() string {
	return fmt.Sprintf("stack '%s' does not have an auto-reconcile policy attached; force-reconcile is not allowed without one", e.StackLabel)
}

type NonPortableResourcesError struct {
	TargetLabel string   `json:"TargetLabel"`
	Resources   []string `json:"Resources"` // e.g. "test-stack/AWS::S3::Bucket/test-bucket"
}

func (e NonPortableResourcesError) Error() string {
	return fmt.Sprintf("cannot replace target %q: %d non-portable resource(s) cannot be recreated on a different target",
		e.TargetLabel, len(e.Resources))
}

type TargetHasResourcesError struct {
	TargetLabel   string `json:"TargetLabel"`
	ResourceCount int    `json:"ResourceCount"`
}

func (e TargetHasResourcesError) Error() string {
	return fmt.Sprintf("target %s cannot be deleted: has %d deployed resources", e.TargetLabel, e.ResourceCount)
}

// TargetDependent names a target whose config references a resource being
// deleted, along with the source resource that triggers the cascade.
type TargetDependent struct {
	TargetLabel   string `json:"TargetLabel"`
	CascadeSource string `json:"CascadeSource"`
}

// FormaTargetHasDependentsError is returned when a destroy would cascade onto one
// or more targets whose config references a resource being deleted (e.g. a secret),
// but the command was not run with on-dependents=cascade. The default is to abort
// so the user does not unknowingly tear down dependent targets and their resources.
type FormaTargetHasDependentsError struct {
	Dependents []TargetDependent `json:"Dependents"`
}

func (e FormaTargetHasDependentsError) Error() string {
	if len(e.Dependents) == 1 {
		return fmt.Sprintf("deleting %q would cascade-delete dependent target %q; re-run with --on-dependents=cascade to proceed",
			e.Dependents[0].CascadeSource, e.Dependents[0].TargetLabel)
	}
	labels := make([]string, len(e.Dependents))
	for i, d := range e.Dependents {
		labels[i] = d.TargetLabel
	}
	return fmt.Sprintf("this delete would cascade-delete %d dependent target(s) %v; re-run with --on-dependents=cascade to proceed",
		len(e.Dependents), labels)
}

// ResourceDependent names a resource whose config references (on a CreateOnly
// field) a resource being deleted, along with the source resource that triggers
// the cascade. The dependent may live in a different stack.
type ResourceDependent struct {
	ResourceLabel string `json:"ResourceLabel"`
	ResourceType  string `json:"ResourceType"`
	Stack         string `json:"Stack"`
	CascadeSource string `json:"CascadeSource"`
}

// FormaResourceHasDependentsError is returned when a destroy would cascade-delete
// one or more dependent resources (a resource whose CreateOnly field references a
// resource being deleted, possibly across stacks) but the command was not run with
// on-dependents=cascade. The default is to abort so the user does not unknowingly
// tear down dependent resources, mirroring the target-cascade default.
type FormaResourceHasDependentsError struct {
	Dependents []ResourceDependent `json:"Dependents"`
}

func (e FormaResourceHasDependentsError) Error() string {
	if len(e.Dependents) == 1 {
		return fmt.Sprintf("deleting %q would cascade-delete dependent resource %q; re-run with --on-dependents=cascade to proceed",
			e.Dependents[0].CascadeSource, e.Dependents[0].ResourceLabel)
	}
	labels := make([]string, len(e.Dependents))
	for i, d := range e.Dependents {
		labels[i] = d.ResourceLabel
	}
	return fmt.Sprintf("this delete would cascade-delete %d dependent resource(s) %v; re-run with --on-dependents=cascade to proceed",
		len(e.Dependents), labels)
}

type PluginNotFoundError struct {
	Name string `json:"Name"`
}

func (e PluginNotFoundError) Error() string {
	return fmt.Sprintf("plugin %q not found", e.Name)
}

type PluginDependencyConflictError struct {
	Message string `json:"Message"`
}

func (e PluginDependencyConflictError) Error() string {
	return fmt.Sprintf("plugin dependency conflict: %s", e.Message)
}
