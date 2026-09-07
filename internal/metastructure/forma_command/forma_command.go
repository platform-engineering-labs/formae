// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package forma_command

import (
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/policy_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type CommandState string

const (
	CommandStateUnknown    CommandState = "Unknown"
	CommandStateNotStarted CommandState = "NotStarted"
	CommandStatePending    CommandState = "Pending"
	CommandStateInProgress CommandState = "InProgress"
	CommandStateFailed     CommandState = "Failed"
	CommandStateSuccess    CommandState = "Success"
	CommandStateCanceling  CommandState = "Canceling"
	CommandStateCanceled   CommandState = "Canceled"
)

// Source identifies who initiated a FormaCommand. User-facing surfaces hide
// commands whose source is not user (internal agent bookkeeping).
type Source string

const (
	SourceUser             Source = "user"
	SourceSynchronizer     Source = "synchronizer"
	SourceDiscovery        Source = "discovery"
	SourceAutoReconciler   Source = "auto-reconciler"
	SourceStackExpirer     Source = "stack-expirer"
	SourceGeneratorRotator Source = "generator-rotator"
)

type FormaCommand struct {
	ID               string                             `json:"ID"`
	Description      pkgmodel.Description               `json:"Description"`
	State            CommandState                       `json:"State"`
	StartTs          time.Time                          `json:"StartTs"`
	ModifiedTs       time.Time                          `json:"ModifiedTs"`
	ResourceUpdates  []resource_update.ResourceUpdate   `json:"ResourceUpdates,omitempty"`
	TargetUpdates    []target_update.TargetUpdate       `json:"TargetUpdates,omitempty"`
	StackUpdates     []stack_update.StackUpdate         `json:"StackUpdates,omitempty"`
	PolicyUpdates    []policy_update.PolicyUpdate       `json:"PolicyUpdates,omitempty"`
	GeneratorUpdates []generator_update.GeneratorUpdate `json:"GeneratorUpdates,omitempty"`
	// DrawGeneratorUpdates are the synthetic draws the changeset schedules,
	// one per generator whose value some destination in this command still
	// needs. They are not part of the generator diff above: a draw writes no
	// generator row, and a generator whose spec never changed still gets one
	// when a resource is newly bound to it.
	//
	// Deliberately not serialized. A draw produces a value in memory for the
	// destinations in the same changeset and is meaningless outside it, so a
	// command recovered from storage re-derives its draws from the surviving
	// destinations rather than replaying a stale set.
	DrawGeneratorUpdates []generator_update.GeneratorUpdate `json:"-"`
	Config               config.FormaCommandConfig          `json:"Config"`
	Command              pkgmodel.Command                   `json:"Command"`
	ClientID             string                             `json:"ClientId,omitempty"`
	Subject              string                             `json:"Subject,omitempty"`
	SubjectName          string                             `json:"SubjectName,omitempty"`
	Source               Source                             `json:"Source,omitempty"`
}

type FormaCommandResult struct {
	Command *FormaCommand `json:"Command"`
	State   CommandState  `json:"State"`
}

type FormaCommandsStatusResult struct {
	Commands []FormaCommandResult `json:"Commands"`
}

func NewFormaCommand(
	forma *pkgmodel.Forma,
	formaCommandConfig *config.FormaCommandConfig,
	command pkgmodel.Command,
	resourceUpdates []resource_update.ResourceUpdate,
	targetUpdates []target_update.TargetUpdate,
	stackUpdates []stack_update.StackUpdate,
	policyUpdates []policy_update.PolicyUpdate,
	generatorUpdates []generator_update.GeneratorUpdate,
	clientID string,
	subject string,
	subjectName string,
	source Source,
) *FormaCommand {
	return &FormaCommand{
		ID:               util.NewID(),
		StartTs:          util.TimeNow(),
		ModifiedTs:       util.TimeNow(),
		ResourceUpdates:  resourceUpdates,
		TargetUpdates:    targetUpdates,
		StackUpdates:     stackUpdates,
		PolicyUpdates:    policyUpdates,
		GeneratorUpdates: generatorUpdates,
		Config:           *formaCommandConfig,
		Command:          command,
		Description:      forma.Description,
		State:            CommandStateNotStarted,
		ClientID:         clientID,
		Subject:          subject,
		SubjectName:      subjectName,
		Source:           source,
	}
}

// HasChanges returns true if the command has any resource, target, stack,
// policy, or generator updates
func (fc *FormaCommand) HasChanges() bool {
	return len(fc.ResourceUpdates) > 0 || len(fc.TargetUpdates) > 0 || len(fc.StackUpdates) > 0 ||
		len(fc.PolicyUpdates) > 0 || len(fc.GeneratorUpdates) > 0
}

// IsInFinalState returns true if the command is in a final state (Success, Failed, or Canceled)
func (fc *FormaCommand) IsInFinalState() bool {
	return fc.State == CommandStateSuccess ||
		fc.State == CommandStateFailed ||
		fc.State == CommandStateCanceled
}

// HasResourceVersions returns true if any resource update has a version set
func (fc *FormaCommand) HasResourceVersions() bool {
	for _, res := range fc.ResourceUpdates {
		if res.Version != "" {
			return true
		}
	}
	return false
}

// GetStackLabels returns unique stack labels from all ResourceUpdates.
// This derives stack information from ResourceUpdates rather than storing it separately.
func (fc *FormaCommand) GetStackLabels() []string {
	seen := make(map[string]bool)
	var labels []string
	for _, ru := range fc.ResourceUpdates {
		if !seen[ru.StackLabel] {
			seen[ru.StackLabel] = true
			labels = append(labels, ru.StackLabel)
		}
	}
	return labels
}
