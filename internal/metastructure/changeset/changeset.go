// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"

	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
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
	IsRateLimited() bool
	IsReady() bool
	IsRunning() bool
	IsSuccess() bool
	IsFailed() bool
	MarkInProgress()
	MarkFailed()
}

type Changeset struct {
	CommandID string
	DAG       *ExecutionDAG
	// Mode is the apply mode the owning command was planned under (reconcile
	// vs patch). Execution-time patch regeneration (a resolvable resolving
	// after planning) must derive its diff under this same mode, or a
	// reconcile-planned removal can silently vanish under patch semantics.
	Mode           pkgmodel.FormaApplyMode
	trackedUpdates map[string]bool
}

type ExecutionDAG struct {
	Nodes map[pkgmodel.FormaeURI]*DAGNode
	// baseResources indexes the create/update ResourceUpdate for each stripped
	// resource URI in the changeset (exactly one per KSUID, since replace is
	// pre-split into delete+create and create/update are mutually exclusive per
	// resource). Delete operations are excluded — childPointsAt uses this
	// index only for the create-phase URI→type lookup.
	baseResources map[pkgmodel.FormaeURI]*resource_update.ResourceUpdate
}

type DAGNode struct {
	URI    pkgmodel.FormaeURI
	Update Update
	// Dependents are nodes that depend on this node (downstream; blocked until this node completes).
	Dependents []*DAGNode
	// Dependencies are nodes that this node depends on (upstream; must complete before this node can start).
	Dependencies []*DAGNode
}

// NewChangeset builds the execution DAG from the given resource, target and
// generator updates. It is a pure graph-builder: the synthetic ops the command
// needs are generated in the Update-construction phase and passed in — Resolve
// target ops (for unchanged targets carrying opaque $ref config) by
// target_update.SynthesizeResolveTargetUpdates, and generator draws by
// generator_update.SynthesizeDrawGeneratorUpdates — so this constructor needs
// no datastore.
//
// generatorUpdates carries draw ops only. A generator's own row is created,
// updated or deleted before the changeset starts, so those operations never
// appear here.
func NewChangeset(
	resourceUpdates []resource_update.ResourceUpdate,
	targetUpdates []target_update.TargetUpdate,
	generatorUpdates []generator_update.GeneratorUpdate,
	commandID string,
	command pkgmodel.Command,
	mode pkgmodel.FormaApplyMode,
) (Changeset, error) {
	changeset := Changeset{
		CommandID:      commandID,
		DAG:            NewExecutionDAG(),
		Mode:           mode,
		trackedUpdates: make(map[string]bool),
	}

	if err := changeset.DAG.Init(resourceUpdates, command); err != nil {
		return Changeset{}, err
	}

	// Copy target updates into a local slice so the DAG owns its own memory.
	// Without this, DAG nodes would point into the FormaCommand's TargetUpdates
	// backing array, which the FormaCommandPersister also mutates — violating
	// actor isolation and causing state corruption.
	var allTargetOps []target_update.TargetUpdate
	type replacePair struct{ deleteIdx, createIdx int }
	var replacePairs []replacePair

	for i := range targetUpdates {
		if targetUpdates[i].Operation == target_update.TargetOperationReplace {
			deleteTU := targetUpdates[i]
			deleteTU.Operation = target_update.TargetOperationDelete
			createTU := targetUpdates[i]
			createTU.Operation = target_update.TargetOperationCreate
			replacePairs = append(replacePairs, replacePair{
				deleteIdx: len(allTargetOps),
				createIdx: len(allTargetOps) + 1,
			})
			allTargetOps = append(allTargetOps, deleteTU, createTU)
		} else {
			allTargetOps = append(allTargetOps, targetUpdates[i])
		}
	}

	for i := range allTargetOps {
		tu := &allTargetOps[i]
		changeset.DAG.Nodes[tu.NodeURI()] = &DAGNode{
			URI:          tu.NodeURI(),
			Update:       tu,
			Dependents:   []*DAGNode{},
			Dependencies: []*DAGNode{},
		}
	}

	// Wire delete → create for split replace targets
	for _, pair := range replacePairs {
		deleteNode := changeset.DAG.Nodes[allTargetOps[pair.deleteIdx].NodeURI()]
		createNode := changeset.DAG.Nodes[allTargetOps[pair.createIdx].NodeURI()]
		createNode.LinkWith(deleteNode)
	}

	// Build implicit edges between target and resource nodes
	changeset.DAG.buildTargetResourceEdges(targetUpdates)

	// Copy the generator draws into a local slice for the same reason the
	// target ops are copied: DAG nodes must not point into a caller's
	// backing array.
	allGeneratorOps := make([]generator_update.GeneratorUpdate, len(generatorUpdates))
	copy(allGeneratorOps, generatorUpdates)

	for i := range allGeneratorOps {
		gu := &allGeneratorOps[i]
		changeset.DAG.Nodes[gu.NodeURI()] = &DAGNode{
			URI:          gu.NodeURI(),
			Update:       gu,
			Dependents:   []*DAGNode{},
			Dependencies: []*DAGNode{},
		}
	}

	if err := changeset.DAG.buildGeneratorResourceEdges(allGeneratorOps); err != nil {
		return Changeset{}, err
	}

	// Re-run cycle detection over the FULL graph, generator edges included.
	// DAG.Init runs its cycle check before any target node, target-resolvable
	// edge or generator node exists, so a cycle formed purely by
	// target-resolvable edges — two in-command targets whose configs reference
	// each other's secrets, or a target referencing a secret hosted on itself —
	// would otherwise slip through and hang the executor. This second pass
	// turns any such cycle into a clean build-time error.
	if changeset.DAG.HasCycles() {
		return Changeset{}, fmt.Errorf("changeset has a dependency cycle involving target-resolvable references")
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

// buildTargetResourceEdges creates implicit ordering edges between target and resource nodes:
//   - Replace: resource deletes → target delete → target create → resource creates
//   - Delete:  resource deletes → target delete
//   - Resolvables: target create/update → depends on resource creates it references
//
// For a target create/update/resolve node, every create/update resource op on that
// target depends on it. A delete op on that target also depends on it, but only when
// the target update re-resolves config ($ref → $value): the delete must dispatch with
// the resolved config rather than the stale $ref-only snapshot. That delete edge is
// gated on resolvables and skipped when it would close a cycle. A synthetic Resolve
// node is treated exactly like a create/update here.
func (p *ExecutionDAG) buildTargetResourceEdges(targetUpdates []target_update.TargetUpdate) {
	// Build resolvable-based dependency edges for target create/update operations.
	// When a target config contains $ref to a resource property, the target node
	// must wait for that resource's create/update to complete first.
	p.buildTargetResolvableEdges()

	// Build implicit edges so resources on a target wait for its create/update node.
	// This ensures the target is persisted (and resolved, if it has resolvables) before
	// the dependent op dispatches its config to the plugin.
	for _, tu := range targetUpdates {
		if tu.Operation == target_update.TargetOperationCreate ||
			tu.Operation == target_update.TargetOperationUpdate ||
			tu.Operation == target_update.TargetOperationResolve {
			targetNode := p.Nodes[tu.NodeURI()]
			if targetNode == nil {
				continue
			}
			// Whether this target update re-resolves config ($ref → $value). Only
			// then must deletes wait — a plain-config update needs no re-resolution,
			// so deletes need no new ordering constraint.
			reResolvesConfig := len(tu.RemainingResolvables) > 0
			for _, node := range p.Nodes {
				ru, ok := node.Update.(*resource_update.ResourceUpdate)
				if !ok || ru.DesiredState.Target != tu.Target.Label {
					continue
				}
				switch ru.Operation {
				case resource_update.OperationCreate, resource_update.OperationUpdate:
					node.LinkWith(targetNode)
				case resource_update.OperationDelete:
					// A delete on a target whose config is being re-resolved must
					// also wait, or it would dispatch the stale $ref-only config.
					// Skip when the target node already (transitively) depends on
					// this delete, since the edge would close a cycle.
					if reResolvesConfig && !dependsOnTransitively(targetNode, node.URI) {
						node.LinkWith(targetNode)
					}
				case resource_update.OperationRead:
					// A read on a target whose config is being resolved — the
					// synthetic Resolve op, e.g. a sync/OOB read of a resource on a
					// secret-authed target — must wait for that Resolve so it
					// dispatches the resolved credential instead of the raw opaque
					// $ref (which the plugin cannot authenticate with, hanging or
					// failing the read). Only the Resolve op warrants this edge: a
					// plain target create/update already carries a usable config, and
					// sync reads are otherwise deliberately edge-free (independent of
					// one another) to avoid one failing read cascading a whole
					// subtree. Guard against a cycle exactly as deletes do.
					if tu.Operation == target_update.TargetOperationResolve &&
						reResolvesConfig && !dependsOnTransitively(targetNode, node.URI) {
						node.LinkWith(targetNode)
					}
				}
			}
		}
	}

	for _, tu := range targetUpdates {
		switch tu.Operation {
		case target_update.TargetOperationReplace:
			targetDeleteURI := pkgmodel.FormaeURI("target://" + tu.Target.Label + "/delete")
			targetCreateURI := pkgmodel.FormaeURI("target://" + tu.Target.Label + "/create")
			targetDeleteNode := p.Nodes[targetDeleteURI]
			targetCreateNode := p.Nodes[targetCreateURI]
			if targetDeleteNode == nil || targetCreateNode == nil {
				continue
			}

			for _, node := range p.Nodes {
				ru, ok := node.Update.(*resource_update.ResourceUpdate)
				if !ok || ru.DesiredState.Target != tu.Target.Label {
					continue
				}
				if ru.Operation == resource_update.OperationDelete {
					targetDeleteNode.LinkWith(node)
				}
				if ru.Operation == resource_update.OperationCreate {
					node.LinkWith(targetCreateNode)
				}
			}

		case target_update.TargetOperationDelete:
			targetDeleteNode := p.Nodes[tu.NodeURI()]
			if targetDeleteNode == nil {
				continue
			}

			for _, node := range p.Nodes {
				ru, ok := node.Update.(*resource_update.ResourceUpdate)
				if !ok {
					continue
				}
				if ru.DesiredState.Target != tu.Target.Label && ru.PriorState.Target != tu.Target.Label {
					continue
				}
				if ru.Operation == resource_update.OperationDelete {
					// Target delete waits for resource deletes
					targetDeleteNode.LinkWith(node)
				}
			}
		}
	}
}

// buildGeneratorResourceEdges wires every resource op that still needs a value
// from a generator to that generator's draw node, so the op cannot dispatch
// before the value exists.
//
// EVERY live destination is wired, whatever its $gen occurrence classified at
// planning. Stability decides whether the generator draws at all
// (resource_update.GeneratorsNeedingDraw); once a draw is in the graph, every
// destination of it must receive the value, so every destination must also
// wait for it. Wiring only the unstable ones would let a stable destination
// dispatch before the draw and keep the old generation while its sibling took
// the new one, which is how two consumers of one credential end up holding
// different values.
//
// A destination being torn down is skipped, matching the rule that decides
// whether to draw at all and the one that delivers: a delete's DesiredState is
// the stored resource, so it carries the stored envelope, writes nothing, and
// must not pull a fresh credential into a row on its way out.
//
// A draw node is a sink: it has no dependencies of its own, so no edge added
// here can close a cycle. The full-graph re-check still runs over these edges,
// which is what keeps that property honest if a draw ever gains an upstream.
func (p *ExecutionDAG) buildGeneratorResourceEdges(generatorUpdates []generator_update.GeneratorUpdate) error {
	for i := range generatorUpdates {
		gu := &generatorUpdates[i]
		generatorNode := p.Nodes[gu.NodeURI()]
		if generatorNode == nil {
			continue
		}

		// The generator's KSUID is what a translated $gen envelope names it
		// by. Without one the draw cannot be matched to any destination, so
		// every destination bound to it would dispatch its envelope undrawn
		// and be rejected at the provider boundary — on this apply and on
		// every one after. Refuse to build such a changeset.
		var generatorKsuid string
		if gu.Generator != nil {
			generatorKsuid = gu.Generator.GetID()
		}
		if generatorKsuid == "" {
			return fmt.Errorf(
				"generator update %s carries no generator identity, so the destinations bound to it cannot be found",
				gu.NodeURI())
		}

		for _, node := range p.Nodes {
			ru, ok := node.Update.(*resource_update.ResourceUpdate)
			if !ok {
				continue
			}
			if ru.Operation == resource_update.OperationDelete || ru.Operation == resource_update.OperationReaped {
				continue
			}
			for _, gen := range pkgmodel.FindGenObjectsFromProperties(ru.DesiredState.Properties) {
				if gen.Generator != generatorKsuid {
					continue
				}
				node.LinkWith(generatorNode)
				break
			}
		}
	}

	return nil
}

// buildDeleteDependencies creates dependencies for delete operations.
// The edge direction is selected per-reference by the field hint's EdgeKind:
//   - EdgeKindDefault: construction-reversed — the dependency is deleted
//     after the consumer (consumer first, producer second).
//   - EdgeKindAttachesTo: reachability order — the producer is deleted first,
//     then the consumer (consumer waits for producer).
//   - EdgeKindRuntimeDependency: same default consumer→producer edge, PLUS
//     edges from the consumer to every containment child of the producer
//     whose parent identity matches *this* producer instance. The consumer
//     must complete before the children tear down, so the children become
//     downstream of the consumer.
func (p *ExecutionDAG) buildDeleteDependencies(allOps []resource_update.ResourceUpdate) {
	deleteOps := make(map[pkgmodel.FormaeURI]resource_update.ResourceUpdate)
	for _, op := range allOps {
		if op.Operation == resource_update.OperationDelete {
			deleteOps[op.URI()] = op
		}
	}

	for _, deleteOp := range deleteOps {
		dependentOpURI := createOperationURI(deleteOp.URI(), resource_update.OperationDelete)
		dependentGroup := p.Nodes[dependentOpURI]
		if dependentGroup == nil {
			continue
		}

		// Build per-ksuid lists of TargetPaths from the resource's resolvable
		// refs. A consumer may reference the same producer at multiple paths
		// (e.g., once with edgeKind=default and once with
		// edgeKind=runtimeDependency); each path carries its own hint and
		// must be honored independently. Collapsing to a single
		// path-per-ksuid would let map iteration order pick which hint wins.
		pathsByKsuid := make(map[string][]string)
		for _, ref := range resolver.ExtractResolvableRefs(deleteOp.DesiredState) {
			ksuid := strings.TrimPrefix(string(ref.URI), "formae://")
			if ksuid == "" {
				continue
			}
			pathsByKsuid[ksuid] = append(pathsByKsuid[ksuid], ref.TargetPath)
		}

		for _, resolvableURI := range deleteOp.RemainingResolvables {
			depBase := resolvableURI.Stripped()
			depResource, exists := deleteOps[depBase]
			if !exists {
				continue
			}
			depNode := p.Nodes[createOperationURI(depBase, resource_update.OperationDelete)]
			if depNode == nil {
				continue
			}

			// If no paths recorded (e.g., callers that only populate
			// RemainingResolvables without the matching Properties JSON, as
			// some unit tests do), fall back to the single-path default
			// hint shape.
			paths := pathsByKsuid[depBase.KSUID()]
			if len(paths) == 0 {
				paths = []string{""}
			}

			for _, targetPath := range paths {
				// Look up the field hint for this specific reference path.
				hint := fieldHintForPath(deleteOp.DesiredState.Schema, targetPath)

				// Resolve the effective EdgeKind. Schemas published before
				// the EdgeKind field landed only set the deprecated
				// AttachesTo bool; JSON unmarshalling normalizes that into
				// EdgeKind, but in-Go struct-literal callers may still rely
				// on the alias.
				edgeKind := hint.EdgeKind
				if edgeKind == "" {
					if hint.AttachesTo {
						edgeKind = pkgmodel.EdgeKindAttachesTo
					} else {
						edgeKind = pkgmodel.EdgeKindDefault
					}
				}

				switch edgeKind {
				case pkgmodel.EdgeKindAttachesTo:
					// Reachability edge: the hosting resource waits for the hosted-on
					// resource's delete. Destroy order: hosted-on first, hosting second.
					dependentGroup.LinkWith(depNode)

				case pkgmodel.EdgeKindRuntimeDependency:
					// Default construction-reversed edge to the producer: consumer
					// first, producer second.
					depNode.LinkWith(dependentGroup)

					// PLUS edges to the producer's containment children that point
					// at *this* producer instance — each child must wait for the
					// consumer's delete before tearing down (since the consumer
					// may still be using the producer's runtime state via the
					// children). Destroy order: consumer first, then children,
					// then producer.
					producerType := depResource.DesiredState.Type
					for _, k := range deleteOps {
						if k.DesiredState.Schema.Parent != producerType {
							continue
						}
						if k.URI() == deleteOp.URI() {
							continue
						}
						if !childPointsAt(&k, &depResource, resource_update.OperationDelete, nil) {
							continue
						}
						// buildDeleteDependencies only enrols ops with Operation
						// == OperationDelete, so k.Operation is always
						// OperationDelete here — but use the actual field for
						// symmetry with the create-side counterpart.
						kNode := p.Nodes[createOperationURI(k.URI(), k.Operation)]
						if kNode == nil {
							continue
						}
						// Child waits for the consumer (consumer first, child second).
						kNode.LinkWith(dependentGroup)
					}

				default: // EdgeKindDefault
					// Construction-reversed edge (existing behavior): B.delete waits
					// for A.delete. Destroy order: consumer first, producer second.
					depNode.LinkWith(dependentGroup)
				}
			}
		}
	}
}

// buildCreateUpdateDependencies creates NORMAL dependencies for create/update
// operations. The natural edge per reference is consumer → producer (consumer
// waits for producer).
//
// For runtimeDependency edges, the natural edge is augmented with edges from
// every containment child of the producer whose parent identity matches *this*
// producer instance into the consumer. Create order becomes: producer first,
// then children, then consumer.
//
// EdgeKindAttachesTo carries no extra meaning on the create phase (it inverts
// destroy order but agrees with the default on construction), so we leave it
// to the default branch.
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

		// Build per-ksuid lists of TargetPaths from the resource's resolvable
		// refs. A consumer may reference the same producer at multiple paths
		// (e.g., once with edgeKind=default and once with
		// edgeKind=runtimeDependency); each path carries its own hint and
		// must be honored independently. Collapsing to a single
		// path-per-ksuid would let map iteration order pick which hint wins.
		pathsByKsuid := make(map[string][]string)
		for _, ref := range resolver.ExtractResolvableRefs(createOp.DesiredState) {
			ksuid := strings.TrimPrefix(string(ref.URI), "formae://")
			if ksuid == "" {
				continue
			}
			pathsByKsuid[ksuid] = append(pathsByKsuid[ksuid], ref.TargetPath)
		}

		for _, resolvableURI := range createOp.RemainingResolvables {
			dependencyBaseURI := resolvableURI.Stripped()

			// Check if there's a corresponding create/update operation for this dependency
			dependencyOp, exists := createUpdateOps[dependencyBaseURI]
			if !exists {
				continue
			}
			dependencyOpURI := createOperationURI(dependencyBaseURI, dependencyOp.Operation)
			dependencyGroup := p.Nodes[dependencyOpURI]

			// NORMAL: dependent waits for dependency to complete
			dependentGroup.LinkWith(dependencyGroup)

			// If no paths recorded (e.g., callers that only populate
			// RemainingResolvables without the matching Properties JSON, as
			// some unit tests do), fall back to the single-path default
			// hint shape.
			paths := pathsByKsuid[dependencyBaseURI.KSUID()]
			if len(paths) == 0 {
				paths = []string{""}
			}

			for _, targetPath := range paths {
				// Resolve the effective EdgeKind for this specific reference
				// path. Normalize the deprecated AttachesTo alias for
				// consistency with the destroy branch even though only
				// RuntimeDependency triggers extra wiring on the create
				// phase.
				hint := fieldHintForPath(createOp.DesiredState.Schema, targetPath)
				edgeKind := hint.EdgeKind
				if edgeKind == "" {
					if hint.AttachesTo {
						edgeKind = pkgmodel.EdgeKindAttachesTo
					} else {
						edgeKind = pkgmodel.EdgeKindDefault
					}
				}

				if edgeKind != pkgmodel.EdgeKindRuntimeDependency {
					continue
				}

				// runtimeDependency: also wire each containment child of the
				// producer that points at *this* producer instance into the
				// consumer. The consumer must wait for the children so it
				// observes fully-realised runtime state. Create order:
				// producer → children → consumer.
				producerType := dependencyOp.DesiredState.Type
				for _, k := range createUpdateOps {
					if k.DesiredState.Schema.Parent != producerType {
						continue
					}
					if k.URI() == createOp.URI() {
						continue
					}
					if !childPointsAt(&k, &dependencyOp, resource_update.OperationCreate, p.baseResources) {
						continue
					}
					// Use the child's actual Operation rather than
					// hardcoding OperationCreate: an existing resource
					// updated in the same changeset will be registered under
					// (uri, OperationUpdate), not (uri, OperationCreate),
					// and the hardcoded lookup would silently miss it —
					// dropping the runtimeDependency child edge.
					kNode := p.Nodes[createOperationURI(k.URI(), k.Operation)]
					if kNode == nil {
						continue
					}
					// Consumer waits for K (K first, consumer second).
					dependentGroup.LinkWith(kNode)
				}
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

// buildTargetResolvableEdges links target nodes that have resolvables to the
// resource nodes they depend on.
//
// For create/update/resolve: target waits for the resource it depends on (normal
// order) — a synthetic Resolve node whose $ref points at a same-command source
// resource waits for that resource's create/update just like a real update.
// For delete: the resource delete waits for the target delete (reversed order),
// ensuring resources on a dependent target are destroyed before the resource
// that provides the target's config (e.g., Grafana dashboards deleted before
// the Compose stack that hosts Grafana).
func (p *ExecutionDAG) buildTargetResolvableEdges() {
	// Build maps of resource KSUID → DAG node by operation type
	createUpdateNodes := make(map[string]*DAGNode)
	deleteNodes := make(map[string]*DAGNode)
	for _, node := range p.Nodes {
		ru, ok := node.Update.(*resource_update.ResourceUpdate)
		if !ok {
			continue
		}
		switch ru.Operation {
		case resource_update.OperationCreate, resource_update.OperationUpdate:
			createUpdateNodes[ru.DesiredState.Ksuid] = node
		case resource_update.OperationDelete:
			deleteNodes[ru.DesiredState.Ksuid] = node
		}
	}

	for _, node := range p.Nodes {
		tu, ok := node.Update.(*target_update.TargetUpdate)
		if !ok {
			continue
		}

		// For delete operations, extract resolvable URIs from the existing
		// target config rather than RemainingResolvables. Delete target updates
		// intentionally don't set RemainingResolvables to avoid the target
		// updater attempting resolution during destroy.
		resolvables := tu.RemainingResolvables
		if tu.Operation == target_update.TargetOperationDelete && tu.ExistingTarget != nil {
			resolvables = resolver.ExtractResolvableURIsFromJSON(tu.ExistingTarget.Config)
		}

		for _, uri := range resolvables {
			ksuid := uri.KSUID()
			if tu.Operation == target_update.TargetOperationDelete {
				// REVERSED: resource delete waits for target delete
				// (target delete already waits for its own resource deletes
				// via buildTargetResourceEdges, so this creates the full chain:
				// compose stack delete ← grafana target delete ← dashboard deletes)
				if depNode, exists := deleteNodes[ksuid]; exists {
					depNode.LinkWith(node)
				}
			} else {
				// NORMAL: target create/update waits for resource create/update
				if depNode, exists := createUpdateNodes[ksuid]; exists {
					node.LinkWith(depNode)
				}
			}
		}
	}
}

func NewExecutionDAG() *ExecutionDAG {
	return &ExecutionDAG{
		Nodes:         make(map[pkgmodel.FormaeURI]*DAGNode),
		baseResources: make(map[pkgmodel.FormaeURI]*resource_update.ResourceUpdate),
	}
}

func (p *ExecutionDAG) Init(resourceUpdates []resource_update.ResourceUpdate, command pkgmodel.Command) error {
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

	// Step 2: Create resource update groups with operation-specific URIs.
	// allOps must not be appended to past this point — &allOps[i] pointers
	// are stored in p.Nodes and p.baseResources and would be invalidated
	// by a backing-array reallocation.
	for i := range allOps {
		update := &allOps[i]
		// Create unique identifier that includes operation
		operationURI := createOperationURI(update.URI(), update.Operation)

		if existing, collision := p.Nodes[operationURI]; collision {
			ru := existing.Update.(*resource_update.ResourceUpdate)
			slog.Error("BUG: changeset DAG URI collision — two resource updates map to the same operationURI, one will be silently dropped",
				"operationURI", operationURI,
				"existingLabel", ru.DesiredState.Label,
				"existingStack", ru.DesiredState.Stack,
				"existingKsuid", ru.DesiredState.Ksuid,
				"newLabel", update.DesiredState.Label,
				"newStack", update.DesiredState.Stack,
				"newKsuid", update.DesiredState.Ksuid,
				"operation", update.Operation,
			)
		}

		p.Nodes[operationURI] = &DAGNode{
			URI:          operationURI,
			Update:       update,
			Dependents:   []*DAGNode{},
			Dependencies: []*DAGNode{},
		}

		// Register create/update ops in the base-resource index so consumers
		// (childPointsAt create phase) can resolve a stripped URI to
		// the latest desired-state ResourceUpdate in O(1). Delete ops are
		// intentionally excluded because the index serves DesiredState lookups.
		switch update.Operation {
		case resource_update.OperationCreate, resource_update.OperationUpdate:
			p.baseResources[update.URI().Stripped()] = update
		}
	}

	// Detect node count mismatch (indicates silent URI collision)
	if len(p.Nodes) != len(allOps) {
		slog.Error("BUG: changeset DAG node count mismatch — some resource updates were lost due to URI collisions",
			"expectedNodes", len(allOps),
			"actualNodes", len(p.Nodes),
			"inputResourceUpdates", len(resourceUpdates),
		)
	}

	// Step 3: Build relationships between operation nodes.
	// Sync commands are pure reads over the current inventory; they should not
	// inherit apply-style dependency edges because one failed read must not block
	// unrelated reads in the same sync command.
	if command != pkgmodel.CommandSync {
		p.buildOperationRelationships(allOps)
	}

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

// propagateResolvedTargetConfig copies a target's resolved (plugin-format) config
// onto every resource-update node whose DesiredState.Target matches targetLabel.
// After a target with $ref auth re-resolves on a mutable update, dependent ops must
// dispatch with the resolved value instead of the stale $ref snapshot from
// generation time. Ordering edges (see buildTargetResourceEdges) guarantee this runs
// before any dependent op's dispatch snapshot is taken.
func (p *ExecutionDAG) propagateResolvedTargetConfig(targetLabel string, pluginConfig json.RawMessage) {
	for _, node := range p.Nodes {
		if ru, ok := node.Update.(*resource_update.ResourceUpdate); ok {
			if ru.DesiredState.Target == targetLabel {
				ru.ResourceTarget.Config = pluginConfig
			}
		}
	}
}

// propagateDrawnGeneratorValue delivers a generator's freshly drawn value to
// every resource-update node holding a destination bound to it. It is the
// generator analogue of propagateResolvedTargetConfig, and it is safe for the
// same reason: the ordering edges buildGeneratorResourceEdges added guarantee
// no destination has dispatched yet, and startResourceUpdate takes a value
// copy of the update at dispatch, so writing into the live node here is seen
// by the dispatch that follows and by nothing that already happened.
//
// Delivery goes through ResourceUpdate.ResolveGeneratorValue rather than a
// raw write for two reasons. The value must land inside the $gen envelope, so
// the $visibility:"Opaque" marker that makes it hash at rest survives; and
// mutating DesiredState.Properties invalidates the derived PatchDocument,
// which must be re-derived under mode — the changeset's own apply mode, the
// one planning used — or a reconcile-planned removal silently vanishes.
//
// mode is a parameter rather than DAG state because the ExecutionDAG does not
// carry the command's configuration; the Changeset does, and the executor
// passes changeset.Mode.
//
// A destination being torn down is skipped, matching the rule that decides
// whether to draw at all (resource_update.GeneratorsNeedingDraw): a delete's
// DesiredState is the stored resource, so it carries the stored envelope and
// writes nothing, and delivering there would put a live credential into a row
// on its way out.
//
// Every other destination receives it, whatever its occurrence classified.
// The invariant is: once a generator draws, every live destination of it IN
// THIS CHANGESET holds the same value and is stamped with the same
// generation. The boundary is the changeset itself. A destination that is not
// a node here — planned by another command, or suppressed by this one because
// nothing about it moved — cannot be reached, and cannot be caught up later
// either, because formae stores only a hash of a generated value and never
// the value. Such a destination keeps an older generation and diverges from
// its siblings; closing that needs co-planning, not a wider delivery.
//
// generatorKsuid is the caller's to guarantee non-empty. It is matched against
// each occurrence's own $generator, and an AUTHORED (not yet translated)
// envelope carries none — so an empty ksuid here would match every
// untranslated envelope in the changeset and deliver the credential into all
// of them. buildGeneratorResourceEdges already refuses to build a changeset
// whose draw carries no identity, but that safety property lives in another
// file and this is the path that writes credentials, so it is restated here
// where it applies.
//
// generationID is the generation the value was drawn under. It is what every
// destination receiving the value is stamped with, so an unnamed generation
// is refused for the same reason an unnamed generator is: the value would be
// delivered with no provenance, every later apply would read the destination
// as unknown movement, and the credential would silently rotate on each one.
//
// An error means some destination did not receive its value. The caller must
// fail the draw closed rather than let a destination dispatch its undrawn
// envelope.
func (p *ExecutionDAG) propagateDrawnGeneratorValue(generatorKsuid string, values map[string]string, generationID string, mode pkgmodel.FormaApplyMode) error {
	if generatorKsuid == "" {
		return fmt.Errorf("cannot deliver a drawn value: the draw names no generator")
	}
	if len(values) == 0 {
		// A success carrying no values cannot be delivered.
		return fmt.Errorf("generator %s reported a successful draw with no value", generatorKsuid)
	}
	for output, value := range values {
		if value == "" {
			// An empty output would write a blank credential into its
			// destination and nothing downstream would flag it.
			return fmt.Errorf("generator %s reported a successful draw with an empty %q output", generatorKsuid, output)
		}
	}
	if generationID == "" {
		return fmt.Errorf("generator %s reported a successful draw naming no generation", generatorKsuid)
	}

	// Two phases: validate and compute every destination's rewritten update,
	// then commit only once all of them prepared. A refusal at the Nth
	// destination must not leave the first N-1 rewritten in memory: DAG
	// ordering happens to keep such nodes undispatchable, but credential
	// delivery does not lean on that. Patch re-derivation during commit can
	// still fail; the caller then fails the draw and every destination is
	// cascade-failed, so a partially committed node is never dispatched.
	type pendingDelivery struct {
		ru       *resource_update.ResourceUpdate
		prepared *resource_update.PreparedGenDelivery
	}
	var deliveries []pendingDelivery
	for _, node := range p.Nodes {
		ru, ok := node.Update.(*resource_update.ResourceUpdate)
		if !ok {
			continue
		}
		if ru.Operation == resource_update.OperationDelete || ru.Operation == resource_update.OperationReaped {
			continue
		}
		prepared, err := ru.PrepareGeneratorValues(generatorKsuid, values)
		if err != nil {
			// The error names paths and identities only, never a value.
			return fmt.Errorf("failed to deliver the value drawn for generator %s to %s: %w",
				generatorKsuid, ru.URI(), err)
		}
		if prepared == nil {
			continue
		}
		deliveries = append(deliveries, pendingDelivery{ru: ru, prepared: prepared})
	}

	for _, d := range deliveries {
		if err := d.ru.CommitGeneratorValues(d.prepared, generatorKsuid, generationID, mode); err != nil {
			return fmt.Errorf("failed to deliver the value drawn for generator %s to %s: %w",
				generatorKsuid, d.ru.URI(), err)
		}
	}

	return nil
}

// clearTargetIncarnationOnResources drops the target-incarnation expectation
// from every resource-update node bound to targetLabel. It runs after a reaped
// target recovers: the recover target update mints a fresh incarnation and
// un-reaps the target's resource rows (stamping them with that fresh
// incarnation), but the pending resource updates were generated from the target
// as it was loaded BEFORE recovery, so they still carry the stale (reaped)
// incarnation. Left in place, the resource-write guard would reject the recovery
// command's own re-adopts (stale expected != fresh current). Clearing the
// expectation lets these writes through (the row is no longer reaped, so the
// tombstone check passes and the incarnation check is skipped), while a genuinely
// stale in-flight sync write from a different command still carries the old
// incarnation and is still correctly rejected. The incarnation is not persisted
// with resource_updates rows, so a resume after crash rebuilds these updates with
// no expectation at all — making the clear effectively durable without a separate
// persisted re-stamp.
func (p *ExecutionDAG) clearTargetIncarnationOnResources(targetLabel string) {
	for _, node := range p.Nodes {
		if ru, ok := node.Update.(*resource_update.ResourceUpdate); ok {
			if ru.DesiredState.Target == targetLabel && ru.ResourceTarget.Health != nil {
				ru.ResourceTarget.Health = nil
			}
		}
	}
}

// dependsOnTransitively reports whether node depends, directly or transitively,
// on the node identified by targetURI by walking the Dependencies graph. Used to
// keep new ordering edges cycle-safe: adding node→targetURI would close a cycle
// exactly when targetURI already depends on node.
func dependsOnTransitively(node *DAGNode, targetURI pkgmodel.FormaeURI) bool {
	visited := make(map[pkgmodel.FormaeURI]struct{})
	var walk func(n *DAGNode) bool
	walk = func(n *DAGNode) bool {
		for _, dep := range n.Dependencies {
			if dep.URI == targetURI {
				return true
			}
			if _, seen := visited[dep.URI]; seen {
				continue
			}
			visited[dep.URI] = struct{}{}
			if walk(dep) {
				return true
			}
		}
		return false
	}
	return walk(node)
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
	rateLimitedCount := 0

	for _, node := range c.DAG.Nodes {
		if !node.IsReady() || len(node.Dependencies) > 0 {
			continue
		}

		updateKey := getUpdateIdentifier(node.URI)
		if node.Update.Namespace() == namespace && !c.trackedUpdates[updateKey] {
			if node.Update.IsRateLimited() {
				if rateLimitedCount >= max {
					continue
				}
				rateLimitedCount++
			}
			executable = append(executable, node.Update)
			c.trackedUpdates[updateKey] = true
			node.Update.MarkInProgress()
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
			if node.Update.IsRateLimited() {
				result[ns]++
			} else {
				if _, exists := result[ns]; !exists {
					result[ns] = 0
				}
			}
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
