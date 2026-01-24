// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"fmt"
	"maps"
	"sort"
	"strings"

	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type Changeset struct {
	CommandID      string
	Pipeline       *ResourceUpdatePipeline
	trackedUpdates map[string]bool
}

type ResourceUpdatePipeline struct {
	ResourceUpdateGroups map[pkgmodel.FormaeURI]*ResourceUpdateGroup
}

type ResourceUpdateGroup struct {
	URI              pkgmodel.FormaeURI
	Updates          []*resource_update.ResourceUpdate
	DownstreamGroups []*ResourceUpdateGroup
	UpstreamGroups   []*ResourceUpdateGroup
}

func NewChangesetFromResourceUpdates(
	resourceUpdates []resource_update.ResourceUpdate,
	commandID string,
	command pkgmodel.Command,
) (Changeset, error) {
	changeset := Changeset{
		CommandID:      commandID,
		Pipeline:       NewResourceUpdatePipeline(),
		trackedUpdates: make(map[string]bool),
	}

	if err := changeset.Pipeline.Init(resourceUpdates); err != nil {
		return Changeset{}, err
	}

	return changeset, nil
}

// createOperationURI creates a unique URI that includes the operation type
func createOperationURI(baseURI pkgmodel.FormaeURI, operation resource_update.OperationType) pkgmodel.FormaeURI {
	return pkgmodel.FormaeURI(fmt.Sprintf("%s/%s/%s", string(baseURI.KSUID()), string(baseURI.PropertyPath()), operation))
}

// buildOperationRelationships builds relationships between operation nodes
func (p *ResourceUpdatePipeline) buildOperationRelationships(allOps []resource_update.ResourceUpdate) {
	// Step 1: Build delete dependencies (REVERSED)
	p.buildDeleteDependencies(allOps)

	// Step 2: Build create/update dependencies (NORMAL)
	p.buildCreateUpdateDependencies(allOps)

	// Step 3: Connect same-resource delete to create operations
	p.connectDeleteToCreate(allOps)
}

// buildDeleteDependencies creates REVERSED dependencies for delete operations
func (p *ResourceUpdatePipeline) buildDeleteDependencies(allOps []resource_update.ResourceUpdate) {
	deleteOps := make(map[pkgmodel.FormaeURI]resource_update.ResourceUpdate)

	// Collect all delete operations
	for _, op := range allOps {
		if op.Operation == resource_update.OperationDelete {
			deleteOps[op.URI()] = op
		}
	}

	// Build REVERSED dependencies for deletes
	for _, deleteOp := range deleteOps {
		dependentOpURI := createOperationURI(deleteOp.URI(), resource_update.OperationDelete)
		dependentGroup := p.ResourceUpdateGroups[dependentOpURI]

		for _, resolvableURI := range deleteOp.RemainingResolvables {
			dependencyBaseURI := resolvableURI.Stripped()

			// Skip self-references to avoid infinite blocking
			if dependencyBaseURI == deleteOp.URI() {
				continue
			}

			if _, exists := deleteOps[dependencyBaseURI]; exists {
				dependencyOpURI := createOperationURI(dependencyBaseURI, resource_update.OperationDelete)
				dependencyGroup := p.ResourceUpdateGroups[dependencyOpURI]

				// REVERSE: dependency delete waits for dependent delete to complete
				dependencyGroup.LinkWith(dependentGroup)
			}
		}
	}
}

// buildCreateUpdateDependencies creates NORMAL dependencies for create/update operations
func (p *ResourceUpdatePipeline) buildCreateUpdateDependencies(allOps []resource_update.ResourceUpdate) {
	createUpdateOps := make(map[pkgmodel.FormaeURI]resource_update.ResourceUpdate)

	// Collect all create/update operations
	for _, op := range allOps {
		if op.Operation == resource_update.OperationCreate || op.Operation == resource_update.OperationUpdate {
			createUpdateOps[op.URI()] = op
		}
	}

	// Build NORMAL dependencies for creates/updates
	for _, createOp := range createUpdateOps {
		dependentOpURI := createOperationURI(createOp.URI(), createOp.Operation)
		dependentGroup := p.ResourceUpdateGroups[dependentOpURI]

		for _, resolvableURI := range createOp.RemainingResolvables {
			dependencyBaseURI := resolvableURI.Stripped()

			// Skip self-references to avoid infinite blocking
			if dependencyBaseURI == createOp.URI() {
				continue
			}

			// Check if there's a corresponding create/update operation for this dependency
			if dependencyOp, exists := createUpdateOps[dependencyBaseURI]; exists {
				dependencyOpURI := createOperationURI(dependencyBaseURI, dependencyOp.Operation)
				dependencyGroup := p.ResourceUpdateGroups[dependencyOpURI]

				// NORMAL: dependent waits for dependency to complete
				dependentGroup.LinkWith(dependencyGroup)
			}
		}
	}
}

// connectDeleteToCreate connects delete operations to their corresponding create operations for the same resource
func (p *ResourceUpdatePipeline) connectDeleteToCreate(allOps []resource_update.ResourceUpdate) {
	deleteOps := make(map[string]*resource_update.ResourceUpdate) // Use stack+label as key
	createOps := make(map[string]*resource_update.ResourceUpdate) // Use stack+label as key

	// Collect delete and create operations by stack+label (ignoring type)
	for i := range allOps {
		op := &allOps[i]
		// Create key based only on stack label and type
		resourceKey := fmt.Sprintf("%s/%s/%s", op.DesiredState.Stack, op.DesiredState.Label, op.DesiredState.Type)
		if op.Operation == resource_update.OperationDelete {
			deleteOps[resourceKey] = op
		}
		if op.Operation == resource_update.OperationCreate {
			createOps[resourceKey] = op
		}
	}

	// Connect delete to create for same resource (stack+label match)
	for resourceKey, deleteOp := range deleteOps {
		if createOp, exists := createOps[resourceKey]; exists {
			deleteOpURI := createOperationURI(deleteOp.URI(), resource_update.OperationDelete)
			createOpURI := createOperationURI(createOp.URI(), resource_update.OperationCreate)

			deleteGroup := p.ResourceUpdateGroups[deleteOpURI]
			createGroup := p.ResourceUpdateGroups[createOpURI]

			if deleteGroup != nil && createGroup != nil {
				// Create waits for delete to complete (replacement order)
				createGroup.LinkWith(deleteGroup)
			}
		}
	}
}

func NewResourceUpdatePipeline() *ResourceUpdatePipeline {
	return &ResourceUpdatePipeline{
		ResourceUpdateGroups: make(map[pkgmodel.FormaeURI]*ResourceUpdateGroup),
	}
}

func (p *ResourceUpdatePipeline) Init(resourceUpdates []resource_update.ResourceUpdate) error {
	// Step 1: Create individual nodes for each operation (including split replace operations)
	var allOps []resource_update.ResourceUpdate

	for i := range resourceUpdates {
		update := &resourceUpdates[i]
		switch update.Operation {
		case resource_update.OperationReplace:
			// Split replace into separate delete + create operations
			deleteOp := *update
			deleteOp.Operation = resource_update.OperationDelete
			allOps = append(allOps, deleteOp)

			createOp := *update
			createOp.Operation = resource_update.OperationCreate
			allOps = append(allOps, createOp)
		default:
			allOps = append(allOps, *update)
		}
	}

	// Step 2: Create resource update groups with operation-specific URIs
	for i := range allOps {
		update := &allOps[i]
		// Create unique identifier that includes operation
		operationURI := createOperationURI(update.URI(), update.Operation)

		p.ResourceUpdateGroups[operationURI] = &ResourceUpdateGroup{
			URI:              operationURI,
			Updates:          []*resource_update.ResourceUpdate{update},
			DownstreamGroups: []*ResourceUpdateGroup{},
			UpstreamGroups:   []*ResourceUpdateGroup{},
		}
	}

	// Step 3: Build relationships between operation nodes
	p.buildOperationRelationships(allOps)

	// Step 4: Check for cycles
	if p.HasCycles() {
		return apimodel.FormaCyclesDetectedError{}
	}

	return nil
}

func (p *ResourceUpdatePipeline) HasCycles() bool {
	for _, group := range p.ResourceUpdateGroups {
		visited := make(map[pkgmodel.FormaeURI]struct{})
		if dfs(group, visited) {
			return true
		}
	}

	return false
}

func dfs(group *ResourceUpdateGroup, visited map[pkgmodel.FormaeURI]struct{}) bool {
	if _, exists := visited[group.URI]; exists {
		return true
	}
	visited[group.URI] = struct{}{}

	if len(group.DownstreamGroups) == 0 {
		return false
	}

	for _, g := range group.DownstreamGroups {
		visitedCopied := maps.Clone(visited)
		if dfs(g, visitedCopied) {
			return true
		}
	}

	return false
}

func (rug *ResourceUpdateGroup) LinkWith(upstream *ResourceUpdateGroup) {
	// Avoid duplicate links
	for _, existing := range rug.UpstreamGroups {
		if existing.URI == upstream.URI {
			return
		}
	}

	rug.UpstreamGroups = append(rug.UpstreamGroups, upstream)
	upstream.DownstreamGroups = append(upstream.DownstreamGroups, rug)
}

func (rug *ResourceUpdateGroup) UnlinkWith(upstream *ResourceUpdateGroup) {
	// Remove from upstream groups
	for i, group := range rug.UpstreamGroups {
		if group.URI == upstream.URI {
			rug.UpstreamGroups = append(rug.UpstreamGroups[:i], rug.UpstreamGroups[i+1:]...)
			break
		}
	}

	// Remove from downstream groups of upstream
	for i, group := range upstream.DownstreamGroups {
		if group.URI == rug.URI {
			upstream.DownstreamGroups = append(upstream.DownstreamGroups[:i], upstream.DownstreamGroups[i+1:]...)
			break
		}
	}
}

func (rug *ResourceUpdateGroup) RemoveUpstreamGroup(upstream *ResourceUpdateGroup) {
	for i, group := range rug.UpstreamGroups {
		if group.URI == upstream.URI {
			rug.UpstreamGroups = append(rug.UpstreamGroups[:i], rug.UpstreamGroups[i+1:]...)
			break
		}
	}
}

func (rug *ResourceUpdateGroup) HasRunningUpdate() bool {
	for _, update := range rug.Updates {
		if update.State == resource_update.ResourceUpdateStateInProgress {
			return true
		}
	}
	return false
}

func (rug *ResourceUpdateGroup) GetRunningUpdate() *resource_update.ResourceUpdate {
	for _, update := range rug.Updates {
		if update.State == resource_update.ResourceUpdateStateInProgress {
			return update
		}
	}
	return nil
}

func (rug *ResourceUpdateGroup) GetRunningUpdates() []*resource_update.ResourceUpdate {
	var updates []*resource_update.ResourceUpdate
	for _, update := range rug.Updates {
		if update.State == resource_update.ResourceUpdateStateInProgress {
			updates = append(updates, update)
		}
	}
	return updates
}

func (rug *ResourceUpdateGroup) NextUpdate() (*resource_update.ResourceUpdate, string) {
	for _, update := range rug.Updates {
		if update.State == resource_update.ResourceUpdateStateNotStarted {
			key := getResourceUpdateIdentifier(update)
			return update, key
		}
	}
	return nil, ""
}

func (rug *ResourceUpdateGroup) Pop(update *resource_update.ResourceUpdate) {
	for i, u := range rug.Updates {
		if u.URI() == update.URI() && u.Operation == update.Operation {
			rug.Updates = append(rug.Updates[:i], rug.Updates[i+1:]...)
			break
		}
	}
}

func (rug *ResourceUpdateGroup) Done() bool {
	return len(rug.Updates) == 0
}

// GetInProgressUpdates returns all updates that are in InProgress state.
// These may be orphaned updates from a previous run that need to be resumed.
func (c *Changeset) GetInProgressUpdates() []*resource_update.ResourceUpdate {
	var updates []*resource_update.ResourceUpdate
	for _, group := range c.Pipeline.ResourceUpdateGroups {
		updates = append(updates, group.GetRunningUpdates()...)
	}
	return updates
}

func (c *Changeset) GetExecutableUpdates(namespace string, max int) []*resource_update.ResourceUpdate {
	var executable []*resource_update.ResourceUpdate
	total := 0

	for _, group := range c.Pipeline.ResourceUpdateGroups {
		// Skip if group has running updates (blocking)
		if group.HasRunningUpdate() {
			continue
		}

		// Check if all upstream dependencies are satisfied
		if len(group.UpstreamGroups) == 0 && !group.Done() && len(group.Updates) > 0 {
			nextUpdate, updateKey := group.NextUpdate()
			if total < max && nextUpdate != nil && nextUpdate.DesiredState.Namespace() == namespace &&
				nextUpdate.State == resource_update.ResourceUpdateStateNotStarted {
				// Check if we haven't already tracked this update
				if !c.trackedUpdates[updateKey] {
					executable = append(executable, nextUpdate)
					c.trackedUpdates[updateKey] = true
					nextUpdate.State = resource_update.ResourceUpdateStateInProgress
					total++
				}
			}
		}
	}

	// Sort by URI for consistent ordering
	sort.Slice(executable, func(i, j int) bool {
		return string(executable[i].URI()) < string(executable[j].URI())
	})

	return executable
}

func (c *Changeset) AvailableExecutableUpdates() map[string]int {
	result := make(map[string]int)
	for _, group := range c.Pipeline.ResourceUpdateGroups {
		// Skip if group has running updates (blocking)
		if group.HasRunningUpdate() {
			continue
		}

		// Check if all upstream dependencies are satisfied
		if len(group.UpstreamGroups) == 0 && !group.Done() && len(group.Updates) > 0 {
			nextUpdate, updateKey := group.NextUpdate()
			if nextUpdate != nil && nextUpdate.State == resource_update.ResourceUpdateStateNotStarted {
				// Check if we haven't already tracked this update
				if !c.trackedUpdates[updateKey] {
					ns := string(nextUpdate.DesiredState.Namespace())
					if _, ok := result[ns]; !ok {
						result[ns] = 1
					} else {
						result[ns]++
					}
				}
			}
		}
	}

	return result
}

func (c *Changeset) UpdatePipeline(update *resource_update.ResourceUpdate) ([]*resource_update.ResourceUpdate, error) {
	uri := createOperationURI(update.URI(), update.Operation)
	upstream, exists := c.Pipeline.ResourceUpdateGroups[uri]
	if !exists {
		return nil, fmt.Errorf("resource group not found for URI: %s", uri)
	}

	if update.State == resource_update.ResourceUpdateStateSuccess {
		upstream.Pop(update)

		// Unblock downstream dependencies for create/update operations
		if update.Operation == resource_update.OperationCreate || update.Operation == resource_update.OperationUpdate {
			for _, downstream := range upstream.DownstreamGroups {
				// Check if this downstream was waiting on this specific resource
				for _, downstreamUpdate := range downstream.Updates {
					for _, resolvableURI := range downstreamUpdate.RemainingResolvables {
						if resolvableURI.Stripped() == update.URI() {
							downstream.UnlinkWith(upstream)
							break
						}
					}
				}
			}
		}

		// Remove completed groups
		if upstream.Done() {
			// Unlink all downstream relationships
			for _, downstream := range upstream.DownstreamGroups {
				downstream.RemoveUpstreamGroup(upstream)
			}
			delete(c.Pipeline.ResourceUpdateGroups, uri)
		}

		return nil, nil
	}

	if update.State == resource_update.ResourceUpdateStateFailed || update.State == resource_update.ResourceUpdateStateRejected {
		upstream.Pop(update)

		// Cascade the failure to all downstream dependencies
		failedUpdates := c.failResourceUpdate(update)

		if len(failedUpdates) > 1 {
			slog.Debug("Cascading failure detected",
				"originalFailure", update.DesiredState.Label,
				"cascadingCount", len(failedUpdates)-1)
		}

		// Pop each cascading failed resource from its group
		for _, failedUpdate := range failedUpdates {
			failedURI := createOperationURI(failedUpdate.URI(), failedUpdate.Operation)
			if failedGroup, exists := c.Pipeline.ResourceUpdateGroups[failedURI]; exists {
				failedGroup.Pop(failedUpdate)

				if failedGroup.Done() {
					// Unlink all downstream relationships
					for _, downstream := range failedGroup.DownstreamGroups {
						downstream.RemoveUpstreamGroup(failedGroup)
					}
					delete(c.Pipeline.ResourceUpdateGroups, failedURI)
				}
			}
		}

		// Remove the original failed resource's group if it's now empty
		if upstream.Done() {
			// Unlink all downstream relationships
			for _, downstream := range upstream.DownstreamGroups {
				downstream.RemoveUpstreamGroup(upstream)
			}
			delete(c.Pipeline.ResourceUpdateGroups, uri)
		}

		return failedUpdates, nil
	}

	return nil, nil
}

func (c *Changeset) failResourceUpdate(update *resource_update.ResourceUpdate) []*resource_update.ResourceUpdate {
	var failedUpdates []*resource_update.ResourceUpdate
	visited := make(map[pkgmodel.FormaeURI]bool)

	c.recursivelyFailDependencies(update, &failedUpdates, visited)

	// Include original failed update
	failedUpdates = append(failedUpdates, update)
	return failedUpdates
}

func (c *Changeset) recursivelyFailDependencies(failedUpdate *resource_update.ResourceUpdate, failedUpdates *[]*resource_update.ResourceUpdate, visited map[pkgmodel.FormaeURI]bool) {
	uri := createOperationURI(failedUpdate.URI(), failedUpdate.Operation)

	if visited[uri] {
		return
	}
	visited[uri] = true

	upstream, exists := c.Pipeline.ResourceUpdateGroups[uri]
	if !exists {
		return
	}

	for _, downstreamGroup := range upstream.DownstreamGroups {
		for _, downstreamUpdate := range downstreamGroup.Updates {
			if downstreamUpdate.State == resource_update.ResourceUpdateStateNotStarted {
				downstreamUpdate.State = resource_update.ResourceUpdateStateFailed
				*failedUpdates = append(*failedUpdates, downstreamUpdate)

				c.recursivelyFailDependencies(downstreamUpdate, failedUpdates, visited)
			}
		}
	}
}

func (c *Changeset) getExecutableUpdates() []*resource_update.ResourceUpdate {
	var executable []*resource_update.ResourceUpdate

	for _, group := range c.Pipeline.ResourceUpdateGroups {
		if group.HasRunningUpdate() {
			continue
		}

		if len(group.UpstreamGroups) == 0 && !group.Done() && len(group.Updates) > 0 {
			nextUpdate, updateKey := group.NextUpdate()
			if nextUpdate != nil && nextUpdate.State == resource_update.ResourceUpdateStateNotStarted {
				if !c.trackedUpdates[updateKey] {
					executable = append(executable, nextUpdate)
					c.trackedUpdates[updateKey] = true
					nextUpdate.State = resource_update.ResourceUpdateStateInProgress
				}
			}
		}
	}

	sort.Slice(executable, func(i, j int) bool {
		return string(executable[i].URI()) < string(executable[j].URI())
	})

	return executable
}

func (c *Changeset) PrintPipeline() string {

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Changeset Pipeline: %s\n", c.CommandID))
	result.WriteString("========================\n")

	for uri, group := range c.Pipeline.ResourceUpdateGroups {
		result.WriteString(fmt.Sprintf("Group: %s\n", uri))
		result.WriteString("  Updates:\n")
		for _, update := range group.Updates {
			result.WriteString(fmt.Sprintf("    - %s (%s)\n", update.Operation, update.State))
		}

		result.WriteString("  Upstream Dependencies:\n")
		for _, dep := range group.UpstreamGroups {
			result.WriteString(fmt.Sprintf("    - %s\n", dep.URI))
		}

		result.WriteString("  Downstream Dependents:\n")
		for _, dep := range group.DownstreamGroups {
			result.WriteString(fmt.Sprintf("    - %s\n", dep.URI))
		}

		result.WriteString("\n")
	}

	return result.String()
}

func (c *Changeset) IsComplete() bool {
	// If no groups remain, changeset is complete
	if len(c.Pipeline.ResourceUpdateGroups) == 0 {
		return true
	}

	// Check if all remaining updates are in failed state
	allFailed := true
	for _, group := range c.Pipeline.ResourceUpdateGroups {
		for _, update := range group.Updates {
			if update.State != resource_update.ResourceUpdateStateFailed && update.State != resource_update.ResourceUpdateStateRejected {
				allFailed = false
				break
			}
		}
		if !allFailed {
			break
		}
	}

	return allFailed
}

func getResourceUpdateIdentifier(update *resource_update.ResourceUpdate) string {
	return fmt.Sprintf("%s-%s", update.URI(), update.Operation)
}
