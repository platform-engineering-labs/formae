// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stats"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

const (
	CommandsTable                  string              = "forma_commands"
	DefaultFormaCommandsQueryLimit                     = 10
	Optional                       QueryItemConstraint = iota
	Required
	Excluded
)

type QueryItemConstraint int

type QueryItem[T any] struct {
	Item       T
	Constraint QueryItemConstraint
}

type StatusQuery struct {
	CommandID *QueryItem[string]
	ClientID  *QueryItem[string]
	Command   *QueryItem[string]
	Status    *QueryItem[string]
	Stack     *QueryItem[string]
	Managed   *QueryItem[bool]
	N         int
}

type ResourceQuery struct {
	Stack            *QueryItem[string]
	Type             *QueryItem[string]
	Label            *QueryItem[string]
	Target           *QueryItem[string]
	LastChangeStatus *QueryItem[string]
	NativeID         *QueryItem[string]
	Managed          *QueryItem[bool]
	N                int
}

type ResourceModification struct {
	Stack     string
	Type      string
	Label     string
	Operation string
}

type Datastore interface {
	StoreFormaCommand(fa *forma_command.FormaCommand, commandID string) error
	LoadFormaCommands() ([]*forma_command.FormaCommand, error)
	LoadIncompleteFormaCommands() ([]*forma_command.FormaCommand, error)
	DeleteFormaCommand(fa *forma_command.FormaCommand, commandID string) error

	GetFormaCommandByCommandID(commandID string) (*forma_command.FormaCommand, error)
	GetMostRecentFormaCommandByClientID(clientID string) (*forma_command.FormaCommand, error)
	GetResourceModificationsSinceLastReconcile(stack string) ([]ResourceModification, error)
	QueryFormaCommands(query *StatusQuery) ([]*forma_command.FormaCommand, error)

	QueryResources(query *ResourceQuery) ([]*pkgmodel.Resource, error)
	StoreResource(resource *pkgmodel.Resource, commandID string) (string, error)
	DeleteResource(resource *pkgmodel.Resource, commandID string) (string, error)
	LoadResource(uri pkgmodel.FormaeURI) (*pkgmodel.Resource, error)
	LoadResourceByNativeID(nativeID string) (*pkgmodel.Resource, error)
	LoadAllResources() ([]*pkgmodel.Resource, error)
	LatestLabelForResource(label string) (string, error)

	StoreStack(stack *pkgmodel.Forma, commandID string) (string, error)
	LoadStack(stackLabel string) (*pkgmodel.Forma, error)
	LoadAllStacks() ([]*pkgmodel.Forma, error)

	CreateTarget(target *pkgmodel.Target) (string, error)
	UpdateTarget(target *pkgmodel.Target) (string, error)
	LoadTarget(targetLabel string) (*pkgmodel.Target, error)
	LoadAllTargets() ([]*pkgmodel.Target, error)
	LoadTargetsByLabels(targetNames []string) ([]*pkgmodel.Target, error)
	LoadDiscoverableTargetsDistinctConfig() ([]*pkgmodel.Target, error)

	Stats() (*stats.Stats, error)

	GetKSUIDByTriplet(stack, label, resourceType string) (string, error)
	BatchGetKSUIDsByTriplets(triplets []pkgmodel.TripletKey) (map[pkgmodel.TripletKey]string, error)
	BatchGetTripletsByKSUIDs(ksuids []string) (map[string]pkgmodel.TripletKey, error)
	LoadResourceById(ksuid string) (*pkgmodel.Resource, error)

	CleanUp() error
	Close()
}

// resourcesAreEqual compares two resources and returns two booleans: the first one indicating whether the
// non-readonly properties of the resources are equal, and the second one indicating whether the readonly i
// properties are equal.
func resourcesAreEqual(resource1, resource2 *pkgmodel.Resource) (bool, bool) {
	readWriteEqual, readOnlyEqual := true, true

	// Compare table fields
	if resource1.NativeID != resource2.NativeID ||
		resource1.Stack != resource2.Stack ||
		resource1.Type != resource2.Type ||
		resource1.Label != resource2.Label {
		readWriteEqual = false
	}

	if !util.JsonEqualRaw(resource1.Properties, resource2.Properties) {
		readWriteEqual = false
	}

	if !util.JsonEqualRaw(resource1.ReadOnlyProperties, resource2.ReadOnlyProperties) {
		readOnlyEqual = false
	}

	return readWriteEqual, readOnlyEqual
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func findFailureMessage(progressResults []resource.ProgressResult) string {
	// Account for non-recoverable errors or max attempts reached
	if msg := filterProgressMessage(progressResults, func(p resource.ProgressResult) bool {
		return p.Failed() && p.StatusMessage != ""
	}); msg != "" {
		return msg
	}

	// Account for recoverable errors
	return filterProgressMessage(progressResults, func(p resource.ProgressResult) bool {
		return p.OperationStatus == resource.OperationStatusFailure && p.StatusMessage != ""
	})
}

func filterProgressMessage(progressResults []resource.ProgressResult, filter func(resource.ProgressResult) bool) string {
	for _, progress := range progressResults {
		if filter(progress) {
			return progress.StatusMessage
		}
	}
	return ""
}
