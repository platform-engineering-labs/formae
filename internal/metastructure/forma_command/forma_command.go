// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package forma_command

import (
	"encoding/json"
	"fmt"
	"sort"
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

// CommandStack records explicit stack coverage, including empty stacks.
type CommandStack struct {
	ID    string `json:"ID,omitempty"`
	Label string `json:"Label"`
}

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
	Resolution       *pkgmodel.DriftReview              `json:"Resolution,omitempty"`
	Setup            *SetupBoundary                     `json:"Setup,omitempty"`
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
	// Draw values remain memory-only. Setup metadata persists the exact draw
	// identities/specifications so restart never infers extra draws from
	// resources added by generator co-planning.
	DrawGeneratorUpdates []generator_update.GeneratorUpdate `json:"-"`
	DrawIntentKnown      bool                               `json:"-"`
	Config               config.FormaCommandConfig          `json:"Config"`
	Command              pkgmodel.Command                   `json:"Command"`
	ClientID             string                             `json:"ClientId,omitempty"`
	Subject              string                             `json:"Subject,omitempty"`
	SubjectName          string                             `json:"SubjectName,omitempty"`
	Source               Source                             `json:"Source,omitempty"`
	Message              string                             `json:"Message,omitempty"`
	InputProperties      json.RawMessage                    `json:"InputProperties,omitempty"`
	Stacks               []CommandStack                     `json:"Stacks,omitempty"`
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
	stacks := make([]CommandStack, 0, len(forma.Stacks)+len(forma.Resources))
	for _, stack := range forma.Stacks {
		stacks = append(stacks, CommandStack{Label: stack.Label})
	}
	for _, res := range forma.Resources {
		stacks = append(stacks, CommandStack{Label: res.Stack})
	}
	for i := range resourceUpdates {
		if resourceUpdates[i].IsAcceptance() {
			resourceUpdates[i].State = resource_update.ResourceUpdateStateSuccess
		}
	}
	return &FormaCommand{
		Setup:            &SetupBoundary{Version: 1},
		DrawIntentKnown:  true,
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
		Message:          formaCommandConfig.Message,
		InputProperties:  pkgmodel.SnapshotInputProperties(forma.Properties),
		Stacks:           stacks,
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

// GetStackLabels returns declared and affected stack scope, including empty stacks.
func (fc *FormaCommand) GetStackLabels() []string {
	seen := make(map[string]bool)
	add := func(label string) {
		if label != "" {
			seen[label] = true
		}
	}
	for _, stack := range fc.Stacks {
		add(stack.Label)
	}
	for _, ru := range fc.ResourceUpdates {
		add(ru.StackLabel)
		add(ru.DesiredState.Stack)
	}
	for _, su := range fc.StackUpdates {
		add(su.Stack.Label)
	}
	for _, pu := range fc.PolicyUpdates {
		add(pu.StackLabel)
	}
	for _, gu := range fc.GeneratorUpdates {
		add(gu.StackLabel)
	}
	labels := make([]string, 0, len(seen))
	for label := range seen {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}

// ResolveStackIdentities ignores declaration IDs. Stack update IDs were minted
// by planning; every other identity comes from the current datastore row.
// Call only at initial admission, never during lifecycle persistence: a later
// stack incarnation must not rewrite the historical command's membership.
func (fc *FormaCommand) ResolveStackIdentities(ds interface {
	GetStackByLabel(string) (*pkgmodel.Stack, error)
}) error {
	stacks := make([]CommandStack, 0)
	for _, label := range fc.GetStackLabels() {
		stack, err := ds.GetStackByLabel(label)
		if err != nil {
			return fmt.Errorf("resolve command stack %q: %w", label, err)
		}
		id := ""
		if stack != nil {
			id = stack.ID
		}
		for _, update := range fc.StackUpdates {
			if update.Stack.Label == label && update.Operation == stack_update.StackOperationCreate && stack == nil {
				id = update.Stack.ID
			}
		}
		// Missing rows can occur in legacy/unmanaged inventory. Preserve their
		// display scope without inventing an incarnation; storage ignores empty IDs.
		stacks = append(stacks, CommandStack{ID: id, Label: label})
	}
	fc.Stacks = stacks
	return nil
}

// HasExecutableChanges excludes logical acceptance from provider scheduling.
func (fc *FormaCommand) HasExecutableChanges() bool {
	if len(fc.TargetUpdates) > 0 {
		return true
	}
	for _, ru := range fc.ResourceUpdates {
		if !ru.IsAcceptance() {
			return true
		}
	}
	return false
}
