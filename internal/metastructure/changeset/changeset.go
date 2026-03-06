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
	DAG            *ExecutionDAG
	trackedUpdates map[string]bool
}

type ExecutionDAG struct {
	Nodes map[pkgmodel.FormaeURI]*DAGNode
}

type DAGNode struct {
	URI        pkgmodel.FormaeURI
	Updates    []*resource_update.ResourceUpdate
	Downstream []*DAGNode
	Upstream   []*DAGNode
}

func NewChangesetFromResourceUpdates(
	resourceUpdates []resource_update.ResourceUpdate,
	commandID string,
	command pkgmodel.Command,
) (Changeset, error) {
	changeset := Changeset{
		CommandID:      commandID,
		DAG:            NewExecutionDAG(),
		trackedUpdates: make(map[string]bool),
	}

	if err := changeset.DAG.Init(resourceUpdates); err != nil {
		return Changeset{}, err
	}

	return changeset, nil
}

// createOperationURI creates a unique URI that includes the operation type
func createOperationURI(baseURI pkgmodel.FormaeURI, operation resource_update.OperationType) pkgmodel.FormaeURI {
	return pkgmodel.FormaeURI(fmt.Sprintf("%s/%s/%s", string(baseURI.KSUID()), string(baseURI.PropertyPath()), operation))
}

// buildOperationRelationships builds relationships between operation nodes
func (p *ExecutionDAG) buildOperationRelationships(allOps []resource_update.ResourceUpdate) {
	// Step 1: Build delete dependencies (REVERSED)
	p.buildDeleteDependencies(allOps)

	// Step 2: Build create/update dependencies (NORMAL)
	p.buildCreateUpdateDependencies(allOps)

	// Step 3: Connect same-resource delete to create operations
	p.connectDeleteToCreate(allOps)
}

// buildDeleteDependencies creates REVERSED dependencies for delete operations
func (p *ExecutionDAG) buildDeleteDependencies(allOps []resource_update.ResourceUpdate) {
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
		dependentGroup := p.Nodes[dependentOpURI]

		for _, resolvableURI := range deleteOp.RemainingResolvables {
			dependencyBaseURI := resolvableURI.Stripped()

			if _, exists := deleteOps[dependencyBaseURI]; exists {
				dependencyOpURI := createOperationURI(dependencyBaseURI, resource_update.OperationDelete)
				dependencyGroup := p.Nodes[dependencyOpURI]

				// REVERSE: dependency delete waits for dependent delete to complete
				dependencyGroup.LinkWith(dependentGroup)
			}
		}
	}
}

// buildCreateUpdateDependencies creates NORMAL dependencies for create/update operations
func (p *ExecutionDAG) buildCreateUpdateDependencies(allOps []resource_update.ResourceUpdate) {
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
		dependentGroup := p.Nodes[dependentOpURI]

		for _, resolvableURI := range createOp.RemainingResolvables {
			dependencyBaseURI := resolvableURI.Stripped()

			// Check if there's a corresponding create/update operation for this dependency
			if dependencyOp, exists := createUpdateOps[dependencyBaseURI]; exists {
				dependencyOpURI := createOperationURI(dependencyBaseURI, dependencyOp.Operation)
				dependencyGroup := p.Nodes[dependencyOpURI]

				// NORMAL: dependent waits for dependency to complete
				dependentGroup.LinkWith(dependencyGroup)
			}
		}
	}
}

// connectDeleteToCreate connects delete operations to their corresponding create operations for the same resource
func (p *ExecutionDAG) connectDeleteToCreate(allOps []resource_update.ResourceUpdate) {
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

			deleteGroup := p.Nodes[deleteOpURI]
			createGroup := p.Nodes[createOpURI]

			if deleteGroup != nil && createGroup != nil {
				// Create waits for delete to complete (replacement order)
				createGroup.LinkWith(deleteGroup)
			}
		}
	}
}

func NewExecutionDAG() *ExecutionDAG {
	return &ExecutionDAG{
		Nodes: make(map[pkgmodel.FormaeURI]*DAGNode),
	}
}

func (p *ExecutionDAG) Init(resourceUpdates []resource_update.ResourceUpdate) error {
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

		p.Nodes[operationURI] = &DAGNode{
			URI:        operationURI,
			Updates:    []*resource_update.ResourceUpdate{update},
			Downstream: []*DAGNode{},
			Upstream:   []*DAGNode{},
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

func (p *ExecutionDAG) HasCycles() bool {
	for _, group := range p.Nodes {
		visited := make(map[pkgmodel.FormaeURI]struct{})
		if dfs(group, visited) {
			return true
		}
	}

	return false
}

func dfs(group *DAGNode, visited map[pkgmodel.FormaeURI]struct{}) bool {
	if _, exists := visited[group.URI]; exists {
		return true
	}
	visited[group.URI] = struct{}{}

	if len(group.Downstream) == 0 {
		return false
	}

	for _, g := range group.Downstream {
		visitedCopied := maps.Clone(visited)
		if dfs(g, visitedCopied) {
			return true
		}
	}

	return false
}

func (n *DAGNode) LinkWith(upstream *DAGNode) {
	// Avoid duplicate links
	for _, existing := range n.Upstream {
		if existing.URI == upstream.URI {
			return
		}
	}

	n.Upstream = append(n.Upstream, upstream)
	upstream.Downstream = append(upstream.Downstream, n)
}

func (n *DAGNode) UnlinkWith(upstream *DAGNode) {
	// Remove from upstream groups
	for i, group := range n.Upstream {
		if group.URI == upstream.URI {
			n.Upstream = append(n.Upstream[:i], n.Upstream[i+1:]...)
			break
		}
	}

	// Remove from downstream groups of upstream
	for i, group := range upstream.Downstream {
		if group.URI == n.URI {
			upstream.Downstream = append(upstream.Downstream[:i], upstream.Downstream[i+1:]...)
			break
		}
	}
}

func (n *DAGNode) RemoveUpstreamGroup(upstream *DAGNode) {
	for i, group := range n.Upstream {
		if group.URI == upstream.URI {
			n.Upstream = append(n.Upstream[:i], n.Upstream[i+1:]...)
			break
		}
	}
}

func (n *DAGNode) HasRunningUpdate() bool {
	for _, update := range n.Updates {
		if update.State == resource_update.ResourceUpdateStateInProgress {
			return true
		}
	}
	return false
}

func (n *DAGNode) GetRunningUpdate() *resource_update.ResourceUpdate {
	for _, update := range n.Updates {
		if update.State == resource_update.ResourceUpdateStateInProgress {
			return update
		}
	}
	return nil
}

func (n *DAGNode) NextUpdate() (*resource_update.ResourceUpdate, string) {
	for _, update := range n.Updates {
		if update.State == resource_update.ResourceUpdateStateNotStarted {
			key := getResourceUpdateIdentifier(update)
			return update, key
		}
	}
	return nil, ""
}

func (n *DAGNode) Pop(update *resource_update.ResourceUpdate) {
	for i, u := range n.Updates {
		if u.URI() == update.URI() && u.Operation == update.Operation {
			n.Updates = append(n.Updates[:i], n.Updates[i+1:]...)
			break
		}
	}
}

func (n *DAGNode) Done() bool {
	return len(n.Updates) == 0
}

func (c *Changeset) GetExecutableUpdates(namespace string, max int) []*resource_update.ResourceUpdate {
	var executable []*resource_update.ResourceUpdate
	total := 0

	for _, group := range c.DAG.Nodes {
		// Skip if group has running updates (blocking)
		if group.HasRunningUpdate() {
			continue
		}

		// Check if all upstream dependencies are satisfied
		if len(group.Upstream) == 0 && !group.Done() && len(group.Updates) > 0 {
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
	for _, group := range c.DAG.Nodes {
		// Skip if group has running updates (blocking)
		if group.HasRunningUpdate() {
			continue
		}

		// Check if all upstream dependencies are satisfied
		if len(group.Upstream) == 0 && !group.Done() && len(group.Updates) > 0 {
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

func (c *Changeset) UpdateDAG(update *resource_update.ResourceUpdate) ([]*resource_update.ResourceUpdate, error) {
	uri := createOperationURI(update.URI(), update.Operation)
	upstream, exists := c.DAG.Nodes[uri]
	if !exists {
		return nil, fmt.Errorf("resource group not found for URI: %s", uri)
	}

	if update.State == resource_update.ResourceUpdateStateSuccess {
		upstream.Pop(update)

		// Unblock downstream dependencies for create/update operations
		if update.Operation == resource_update.OperationCreate || update.Operation == resource_update.OperationUpdate {
			for _, downstream := range upstream.Downstream {
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
			for _, downstream := range upstream.Downstream {
				downstream.RemoveUpstreamGroup(upstream)
			}
			delete(c.DAG.Nodes, uri)
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
			if failedGroup, exists := c.DAG.Nodes[failedURI]; exists {
				failedGroup.Pop(failedUpdate)

				if failedGroup.Done() {
					// Unlink all downstream relationships
					for _, downstream := range failedGroup.Downstream {
						downstream.RemoveUpstreamGroup(failedGroup)
					}
					delete(c.DAG.Nodes, failedURI)
				}
			}
		}

		// Remove the original failed resource's group if it's now empty
		if upstream.Done() {
			// Unlink all downstream relationships
			for _, downstream := range upstream.Downstream {
				downstream.RemoveUpstreamGroup(upstream)
			}
			delete(c.DAG.Nodes, uri)
		}

		return failedUpdates, nil
	}

	// Any other state reaching here is unexpected — treat as failure to
	// prevent the changeset from getting permanently stuck.
	slog.Warn("Unexpected resource update state in UpdateDAG, treating as failure",
		"uri", update.URI(), "state", update.State)
	update.State = resource_update.ResourceUpdateStateFailed
	return c.UpdateDAG(update)
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

	upstream, exists := c.DAG.Nodes[uri]
	if !exists {
		return
	}

	for _, downstreamGroup := range upstream.Downstream {
		for _, downstreamUpdate := range downstreamGroup.Updates {
			if downstreamUpdate.State == resource_update.ResourceUpdateStateNotStarted {
				downstreamUpdate.State = resource_update.ResourceUpdateStateFailed
				*failedUpdates = append(*failedUpdates, downstreamUpdate)

				c.recursivelyFailDependencies(downstreamUpdate, failedUpdates, visited)
			}
		}
	}
}

func (c *Changeset) PrintDAG() string {

	var result strings.Builder
	fmt.Fprintf(&result, "Changeset DAG: %s\n", c.CommandID)
	result.WriteString("========================\n")

	for uri, group := range c.DAG.Nodes {
		fmt.Fprintf(&result, "Group: %s\n", uri)
		result.WriteString("  Updates:\n")
		for _, update := range group.Updates {
			fmt.Fprintf(&result, "    - %s (%s)\n", update.Operation, update.State)
		}

		result.WriteString("  Upstream Dependencies:\n")
		for _, dep := range group.Upstream {
			fmt.Fprintf(&result, "    - %s\n", dep.URI)
		}

		result.WriteString("  Downstream Dependents:\n")
		for _, dep := range group.Downstream {
			fmt.Fprintf(&result, "    - %s\n", dep.URI)
		}

		result.WriteString("\n")
	}

	return result.String()
}

func (c *Changeset) IsComplete() bool {
	// If no groups remain, changeset is complete
	if len(c.DAG.Nodes) == 0 {
		return true
	}

	// Check if all remaining updates are in failed state
	allFailed := true
	for _, group := range c.DAG.Nodes {
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
