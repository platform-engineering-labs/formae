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

// Update is the interface for items that can be scheduled in the ExecutionDAG.
// Both ResourceUpdate and (future) TargetUpdate implement this interface.
//
// Operation type is intentionally NOT part of this interface. It is baked into
// DAGNode.URI at construction time via createOperationURI(). After construction,
// the DAG treats node URIs as opaque keys.
type Update interface {
	NodeURI() pkgmodel.FormaeURI
	Resolvables() []pkgmodel.FormaeURI
	Namespace() string
	IsReady() bool
	IsRunning() bool
	IsSuccess() bool
	IsFailed() bool
	MarkInProgress()
	MarkFailed()
}

type Changeset struct {
	CommandID      string
	DAG            *ExecutionDAG
	trackedUpdates map[string]bool
}

type ExecutionDAG struct {
	Nodes map[pkgmodel.FormaeURI]*DAGNode
}

type DAGNode struct {
	URI    pkgmodel.FormaeURI
	Update Update
	// Dependents are nodes that depend on this node (downstream; blocked until this node completes).
	Dependents []*DAGNode
	// Dependencies are nodes that this node depends on (upstream; must complete before this node can start).
	Dependencies []*DAGNode
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
			URI:          operationURI,
			Update:       update,
			Dependents:   []*DAGNode{},
			Dependencies: []*DAGNode{},
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

	if len(group.Dependents) == 0 {
		return false
	}

	for _, g := range group.Dependents {
		visitedCopied := maps.Clone(visited)
		if dfs(g, visitedCopied) {
			return true
		}
	}

	return false
}

func (n *DAGNode) LinkWith(upstream *DAGNode) {
	for _, existing := range n.Dependencies {
		if existing.URI == upstream.URI {
			return
		}
	}

	n.Dependencies = append(n.Dependencies, upstream)
	upstream.Dependents = append(upstream.Dependents, n)
}

func (n *DAGNode) Unlink(upstream *DAGNode) {
	for i, node := range n.Dependencies {
		if node.URI == upstream.URI {
			n.Dependencies = append(n.Dependencies[:i], n.Dependencies[i+1:]...)
			break
		}
	}
	for i, node := range upstream.Dependents {
		if node.URI == n.URI {
			upstream.Dependents = append(upstream.Dependents[:i], upstream.Dependents[i+1:]...)
			break
		}
	}
}

func (n *DAGNode) IsRunning() bool {
	return n.Update.IsRunning()
}

func (n *DAGNode) IsReady() bool {
	return n.Update.IsReady()
}

func (c *Changeset) GetExecutableUpdates(namespace string, max int) []Update {
	var executable []Update
	total := 0

	for _, node := range c.DAG.Nodes {
		if !node.IsReady() || len(node.Dependencies) > 0 {
			continue
		}

		updateKey := getUpdateIdentifier(node.URI)
		if total < max && node.Update.Namespace() == namespace && !c.trackedUpdates[updateKey] {
			executable = append(executable, node.Update)
			c.trackedUpdates[updateKey] = true
			node.Update.MarkInProgress()
			total++
		}
	}

	sort.Slice(executable, func(i, j int) bool {
		return string(executable[i].NodeURI()) < string(executable[j].NodeURI())
	})

	return executable
}

func (c *Changeset) AvailableExecutableUpdates() map[string]int {
	result := make(map[string]int)
	for _, node := range c.DAG.Nodes {
		if !node.IsReady() || len(node.Dependencies) > 0 {
			continue
		}

		updateKey := getUpdateIdentifier(node.URI)
		if !c.trackedUpdates[updateKey] {
			ns := node.Update.Namespace()
			result[ns]++
		}
	}

	return result
}

func (c *Changeset) UpdateDAG(nodeURI pkgmodel.FormaeURI, update Update) ([]Update, error) {
	node, exists := c.DAG.Nodes[nodeURI]
	if !exists {
		return nil, fmt.Errorf("DAG node not found for URI: %s", nodeURI)
	}

	if update.IsSuccess() {
		c.removeNode(node, nodeURI)
		return nil, nil
	}

	if update.IsFailed() {
		failedNodes := c.failDependents(node)

		if len(failedNodes) > 0 {
			slog.Debug("Cascading failure detected",
				"originalFailure", update.NodeURI(),
				"cascadingCount", len(failedNodes))
		}

		// Remove each cascading failed node from the DAG
		for _, failedNode := range failedNodes {
			c.removeNode(failedNode, failedNode.URI)
		}

		// Remove the original failed node
		c.removeNode(node, nodeURI)

		// Collect the updates from the failed nodes for the caller
		var failedUpdates []Update
		for _, fn := range failedNodes {
			failedUpdates = append(failedUpdates, fn.Update)
		}
		failedUpdates = append(failedUpdates, update)
		return failedUpdates, nil
	}

	// Any other state reaching here is unexpected — treat as failure to
	// prevent the changeset from getting permanently stuck.
	slog.Warn("Unexpected update state in UpdateDAG, treating as failure",
		"uri", update.NodeURI())
	update.MarkFailed()
	return c.UpdateDAG(nodeURI, update)
}

func (c *Changeset) removeNode(node *DAGNode, uri pkgmodel.FormaeURI) {
	// Copy the slice — Unlink modifies node.Dependents during iteration.
	dependents := make([]*DAGNode, len(node.Dependents))
	copy(dependents, node.Dependents)
	for _, dependent := range dependents {
		dependent.Unlink(node)
	}
	delete(c.DAG.Nodes, uri)
}

func (c *Changeset) failDependents(node *DAGNode) []*DAGNode {
	var failedNodes []*DAGNode
	visited := make(map[pkgmodel.FormaeURI]bool)

	c.recursivelyFailDependents(node, &failedNodes, visited)

	return failedNodes
}

func (c *Changeset) recursivelyFailDependents(node *DAGNode, failedNodes *[]*DAGNode, visited map[pkgmodel.FormaeURI]bool) {
	if visited[node.URI] {
		return
	}
	visited[node.URI] = true

	for _, downstream := range node.Dependents {
		if downstream.Update.IsReady() {
			downstream.Update.MarkFailed()
			*failedNodes = append(*failedNodes, downstream)

			c.recursivelyFailDependents(downstream, failedNodes, visited)
		}
	}
}

func (c *Changeset) PrintDAG() string {
	var result strings.Builder
	fmt.Fprintf(&result, "Changeset DAG: %s\n", c.CommandID)
	result.WriteString("========================\n")

	for uri, node := range c.DAG.Nodes {
		fmt.Fprintf(&result, "Node: %s\n", uri)

		result.WriteString("  Dependencies:\n")
		for _, dep := range node.Dependencies {
			fmt.Fprintf(&result, "    - %s\n", dep.URI)
		}

		result.WriteString("  Dependents:\n")
		for _, dep := range node.Dependents {
			fmt.Fprintf(&result, "    - %s\n", dep.URI)
		}

		result.WriteString("\n")
	}

	return result.String()
}

func (c *Changeset) IsComplete() bool {
	if len(c.DAG.Nodes) == 0 {
		return true
	}

	for _, node := range c.DAG.Nodes {
		if !node.Update.IsFailed() {
			return false
		}
	}

	return true
}

func getUpdateIdentifier(nodeURI pkgmodel.FormaeURI) string {
	return string(nodeURI)
}
