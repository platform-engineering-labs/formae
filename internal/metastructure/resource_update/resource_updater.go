// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"
	"github.com/google/uuid"
	"github.com/theory/jsonpath"
	"github.com/theory/jsonpath/registry"
	"go.opentelemetry.io/otel"
	otelmetric "go.opentelemetry.io/otel/metric"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/patch"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// jsonpathParser is a package-level parser with RFC 9535 function extensions (match, search, etc.)
var jsonpathParser = jsonpath.NewParser(jsonpath.WithRegistry(registry.New()))

// convertResourceForPlugin converts a resource's properties to plugin format
// by extracting $value from opaque value structures (e.g., {"$value": "secret", "$visibility": "Opaque"})
// becomes just "secret". This must be done before sending to the plugin since the resolver
// lives in the agent and plugins may be remote.
//
// It also strips nested empty collections ([]/{}  artifacts from PKL's null rendering
// of nullable Listing/Mapping fields) to prevent cloud API rejections for fields like K8S probes
// that require handler types when non-empty.
func convertResourceForPlugin(res pkgmodel.Resource) (pkgmodel.Resource, error) {
	converted, err := convertResourceForPluginWith(res, resolver.ConvertToPluginFormat)
	if err != nil {
		return res, err
	}
	// The provider boundary: this is the last point at which the properties
	// are still formae's, and the only place that knows they are about to be
	// written rather than diffed.
	if err := resolver.GuardNoUnresolvedGenerators(converted.Properties); err != nil {
		return res, err
	}
	return converted, nil
}

// convertResourceForPluginRead is the Read-context counterpart of
// convertResourceForPlugin: it prepares DesiredState/PriorState as context for a
// plugin Read call (sync, discovery, or the pre-update out-of-band check), which
// never writes those values to the cloud. Unlike convertResourceForPlugin it does
// NOT reject an already-hashed schema-opaque field — that hash is the steady state
// once a secret has been persisted, and rejecting it here would make
// every sync/OOB-check Read of a secret-bearing resource fail permanently.
func convertResourceForPluginRead(res pkgmodel.Resource) (pkgmodel.Resource, error) {
	return convertResourceForPluginWith(res, resolver.ConvertExistingStateForRead)
}

// convertResourceForPluginWith converts a resource's properties to plugin format
// by extracting $value from opaque value structures (e.g., {"$value": "secret", "$visibility": "Opaque"})
// becomes just "secret". This must be done before sending to the plugin since the resolver
// lives in the agent and plugins may be remote.
//
// It also strips nested empty collections ([]/{}  artifacts from PKL's null rendering
// of nullable Listing/Mapping fields) to prevent cloud API rejections for fields like K8S probes
// that require handler types when non-empty.
func convertResourceForPluginWith(res pkgmodel.Resource, convert func(json.RawMessage) (json.RawMessage, error)) (pkgmodel.Resource, error) {
	if res.Properties == nil {
		return res, nil
	}

	convertedProps, err := convert(res.Properties)
	if err != nil {
		return res, err
	}

	// Strip nested empty collections from PKL null rendering artifacts.
	// Top-level empty collections are preserved (may be intentional clears),
	// and preserveEmptyValues-hinted fields keep their subtrees verbatim in
	// every plugin-bound context: their empties are values, not artifacts.
	cleanedProps, err := patch.StripNestedEmptyCollectionsExcept(convertedProps, patch.PreserveEmptyRootFields(res.Schema))
	if err != nil {
		return res, err
	}

	// Return a copy with converted and cleaned properties
	result := res
	result.Properties = cleanedProps
	return result, nil
}

// ResourceUpdater is the Ergo state machine responsible for executing resource updates in the metastructure.
// A resource update is a sequence of plugin operations that are applied to a resource in the cloud. Different
// resource updates go through different states depending on the type of operation being performed. The supported
// operation types are Create, Update, Delete, and Synchronize. Before every Delete and Update operation, the
// affected resource is synchronized to ensure that the latest state of the resource in the cloud is known. IF
// we detect that the resource state in the cloud is different from the state in the metastructure (stack), we
// reject the resource update.
//
// The state transitions are as follows:
//
//	                   +-----------------------+                 +-----------------------+
//	                   |     Initializing      | --------------> |     Synchronizing     |----
//	                   +-----------------------+                 +-----------------------+    |
//	                               |                                   |     |                |
//	                               |                                   |     |                |
//	                               |                                   |     |                |
//	                               |                                   |     |                |
//	                               |                                   |     |                |
//	                               v                                   |     |                |
//	                   +-----------------------+                       |     |                |
//	  -----------------|       Resolving       |<----------------------      |                |
//	|                  +-----------------------+                             |                |
//	|                        |           |                                   |                |
//	|                        |           |                                   |                |
//	|                        |           |                                   |                |
//	|                        |           |                                   |                |
//	|                        |           |                                   |                |
//	|                        v           v                                   v                |
//	|    +-----------------------+   +-----------------------+   +-----------------------+    |
//	|    |       Creating        |   |       Updating        |   |       Deleting        |    |
//	|    +-----------------------+   +-----------------------+   +-----------------------+    |
//	|                   |  |               |           |                           |  |       |
//	|                   |  |               |           |                           |  |       |
//	|                   |  |        -------             ----------------------     |  |       |
//	|                   |   --------------------------------------------      |    |  |       |
//	|                   |          |     ---------------------------------------------        |
//	|                   v          v    v                               v     v    v          |
//	|                  +----------------------+                   +----------------------+    |
//	|                  | FinishedSuccessFully |                   |  FinishedWithErrors  |<---
//	|                  +----------------------+                   +----------------------+
//	|                                                                         ^
//	|                                                                         |
//	 -------------------------------------------------------------------------

type ResourceUpdater struct {
	statemachine.StateMachine[ResourceUpdateData]
}

func newResourceUpdater() gen.ProcessBehavior {
	return &ResourceUpdater{}
}

// NewResourceUpdater is the exported factory for spawning a ResourceUpdater process.
// It is used by ChangesetExecutor to spawn child ResourceUpdaters with LinkParent.
func NewResourceUpdater() gen.ProcessBehavior {
	return newResourceUpdater()
}

type ResourceUpdateFinished struct {
	Uri   pkgmodel.FormaeURI
	State ResourceUpdateState
}

type StartResourceUpdate struct {
	ResourceUpdate ResourceUpdate
	CommandID      string
	UpdateId       string
	// Mode is the apply mode the owning command was planned under (reconcile
	// vs patch). It flows into ResourceUpdateData.applyMode so a resolvable
	// that resolves after planning regenerates its patch under the same
	// semantics the command was planned with.
	Mode pkgmodel.FormaApplyMode
}

type PluginOperatorMissingInAction struct{}

type ResolveTimedOut struct{}

type Shutdown struct{}

// PersistResourceUpdate is sent to the ResourcePersister actor to store a resource update
// in the datastore after a successful plugin operation.
type PersistResourceUpdate struct {
	CommandID         string
	ResourceOperation OperationType
	PluginOperation   resource.Operation
	ResourceUpdate    ResourceUpdate
}

const (
	StateInitializing         = gen.Atom("initializing")
	StateSynchronizing        = gen.Atom("synchronizing")
	StateResolving            = gen.Atom("resolving")
	StateDeleting             = gen.Atom("deleting")
	StateCreating             = gen.Atom("creating")
	StateUpdating             = gen.Atom("updating")
	StateExiting              = gen.Atom("exiting")
	StateFinishedSuccessfully = gen.Atom("finished_successfully")
	StateFinishedWithError    = gen.Atom("finished_with_error")
	StateRejected             = gen.Atom("rejected")
)

// PluginCallTimeout is the deadline the agent hands each plugin operator for a
// single watched plugin call. It matches the operator's own compiled fallback,
// so the watchdog window derived from it holds whether the operator runs on the
// supplied deadline or on its fallback. Exposed as a variable so tests can
// reduce it.
var PluginCallTimeout = 60 * time.Second

// PluginOperationCallTimeout is the maximum time (in seconds) to wait for a
// plugin operator to respond to a resource operation. It outlasts
// PluginCallTimeout so the operator's own deadline expires first and its
// attributable failure progress wins the race with this call. Exposed as a
// variable so tests can reduce it.
var PluginOperationCallTimeout = int((PluginCallTimeout + 10*time.Second) / time.Second)

type ResourceUpdateData struct {
	resourceUpdate  *ResourceUpdate
	commandID       string
	labelConfig     pkgmodel.LabelConfig // JSONPath-based label extraction config from plugin
	labelTagKeys    []string             // Legacy tag-based label keys for backwards compatibility
	resourceLabeler *ResourceLabeler
	retryConfig     pkgmodel.RetryConfig
	requestedBy     gen.PID
	commandSource   FormaCommandSource

	// applyMode is the apply mode the owning command was planned under
	// (reconcile vs patch), set from StartResourceUpdate.Mode in start().
	// resourceResolved passes it to ResolveValue so execution-time patch
	// regeneration derives its diff under the same semantics planning used.
	applyMode pkgmodel.FormaApplyMode

	// operatorRetryConfig is the retry config the PluginCoordinator spawned the
	// watched plugin operator with, which is the per-plugin override wherever
	// one is configured. It is nil until a spawn result reports one.
	operatorRetryConfig *pkgmodel.RetryConfig

	// Because the discovery process can alter the resource URI (by changing the resource label), we need to keep
	// track of the original resource URI for notifying both the forma_command_persister as well as the changeset
	// executor.
	// Once we switch out the URI for a stable KSUID we can remove this field.
	originalResourceKsuidURI pkgmodel.FormaeURI

	// operationFailures counts resource updates that reach terminal failure.
	// Nil when instrument creation failed, which must not fail the update.
	operationFailures otelmetric.Int64Counter

	// stage is the deepest state the update reached. A single resource update
	// runs its whole synchronize/resolve/operate chain inside one message
	// handler, so the intermediate states are never committed to the state
	// machine — the enter callback's oldState stays 'initializing'. Each stage
	// function records the state it represents here so a terminal failure can
	// be attributed to the step that actually failed.
	stage gen.Atom
}

func (r *ResourceUpdater) Init(args ...any) (statemachine.StateMachineSpec[ResourceUpdateData], error) {
	data := ResourceUpdateData{
		requestedBy: args[0].(gen.PID),
	}
	initialState := StateInitializing
	if len(args) > 1 {
		initialState = args[1].(gen.Atom)
	}

	pluginCfg, ok := r.Env("RetryConfig")
	if !ok {
		r.Log().Error("ResourceUpdater: missing 'RetryConfig' environment variable")
		return statemachine.StateMachineSpec[ResourceUpdateData]{}, fmt.Errorf("resourceUpdater: missing 'RetryConfig' environment variable")
	}
	data.retryConfig = pluginCfg.(pkgmodel.RetryConfig)

	discoveryCfg, ok := r.Env("DiscoveryConfig")
	if !ok {
		r.Log().Error("ResourceUpdater: missing 'DiscoveryConfig' environment variable")
		return statemachine.StateMachineSpec[ResourceUpdateData]{}, fmt.Errorf("resourceUpdater: missing 'DiscoveryConfig' environment variable")
	}
	data.labelTagKeys = discoveryCfg.(pkgmodel.DiscoveryConfig).LabelTagKeys

	ds, ok := r.Env("Datastore")
	if !ok {
		r.Log().Error("ResourceUpdater: missing 'Datastore' environment variable")
		return statemachine.StateMachineSpec[ResourceUpdateData]{}, fmt.Errorf("resourceUpdater: missing 'Datastore' environment variable")
	}
	data.resourceLabeler = NewResourceLabeler(ds.(ResourceDataLookup))

	// The MeterProvider is injected so tests get their own reader; production
	// resolves the global here, at spawn time, rather than capturing it at
	// metastructure construction — the OTel provider is installed later, during
	// API server startup.
	meterProvider := otel.GetMeterProvider()
	if mp, ok := r.Env("MeterProvider"); ok {
		if provider, ok := mp.(otelmetric.MeterProvider); ok {
			meterProvider = provider
		}
	}
	if err := setupResourceUpdateMetrics(&data, meterProvider); err != nil {
		r.Log().Error("Failed to setup resource update metrics: %v", err)
		// Don't fail initialization if metrics setup fails
	}

	r.Log().Debug("ResourceUpdater %s initialized", r.Name())

	return statemachine.NewStateMachineSpec(initialState,

		statemachine.WithData(data),

		statemachine.WithStateEnterCallback(onStateChange),

		statemachine.WithStateMessageHandler(StateInitializing, start),
		statemachine.WithStateMessageHandler(StateDeleting, handleProgressUpdate),
		statemachine.WithStateMessageHandler(StateDeleting, pluginOperationMissingInAction),
		statemachine.WithStateMessageHandler(StateDeleting, shutdown),
		statemachine.WithStateMessageHandler(StateCreating, handleProgressUpdate),
		statemachine.WithStateMessageHandler(StateCreating, pluginOperationMissingInAction),
		statemachine.WithStateMessageHandler(StateCreating, shutdown),
		statemachine.WithStateMessageHandler(StateUpdating, handleProgressUpdate),
		statemachine.WithStateMessageHandler(StateUpdating, pluginOperationMissingInAction),
		statemachine.WithStateMessageHandler(StateUpdating, shutdown),
		statemachine.WithStateMessageHandler(StateResolving, resourceResolved),
		statemachine.WithStateMessageHandler(StateResolving, resolveTimedOut),
		statemachine.WithStateMessageHandler(StateResolving, resourceFailedToResolve),
		statemachine.WithStateMessageHandler(StateResolving, shutdown),
		statemachine.WithStateMessageHandler(StateSynchronizing, handleProgressUpdate),
		statemachine.WithStateMessageHandler(StateSynchronizing, pluginOperationMissingInAction),
		statemachine.WithStateMessageHandler(StateSynchronizing, shutdown),
		statemachine.WithStateMessageHandler(StateFinishedSuccessfully, shutdown),
		statemachine.WithStateMessageHandler(StateFinishedWithError, shutdown),
		statemachine.WithStateMessageHandler(StateRejected, shutdown),
	), nil
}

// Returns the address (ProcessID) of the global resource persister.
func resourcePersisterProcess(proc gen.Process) gen.ProcessID {
	return gen.ProcessID{
		Name: actornames.ResourcePersister,
		Node: proc.Node().Name(),
	}
}

// Returns the address (ProcessID) of the global forma command persister.
func formaCommandPersisterProcess(proc gen.Process) gen.ProcessID {
	return gen.ProcessID{
		Name: gen.Atom("FormaCommandPersister"),
		Node: proc.Node().Name(),
	}
}

func shutdown(from gen.PID, state gen.Atom, data ResourceUpdateData, shutdown Shutdown, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	return state, data, nil, gen.TerminateReasonNormal
}

func onStateChange(oldState gen.Atom, newState gen.Atom, data ResourceUpdateData, proc gen.Process) (gen.Atom, ResourceUpdateData, error) {
	// Counted before the blocking persister call below: the failure has already
	// happened by the time this callback runs, and that call tolerates its own
	// failure by logging, so the metric must not be more fragile than the
	// bookkeeping beside it. A rejection is deliberately not counted here — the
	// update's state becomes Rejected, not Failed.
	if newState == StateFinishedWithError {
		recordOperationFailure(oldState, data, proc)
	}

	if newState == StateFinishedSuccessfully || newState == StateFinishedWithError || newState == StateRejected {
		proc.Log().Debug("ResourceUpdater: sending completion message to forma command persister state=%s commandID=%s", newState, data.commandID)
		_, err := proc.Call(
			formaCommandPersisterProcess(proc),
			messages.MarkResourceUpdateAsComplete{
				CommandID:                  data.commandID,
				ResourceURI:                data.originalResourceKsuidURI,
				Operation:                  data.resourceUpdate.Operation,
				FinalState:                 data.resourceUpdate.State,
				ResourceStartTs:            data.resourceUpdate.StartTs,
				ResourceModifiedTs:         data.resourceUpdate.ModifiedTs,
				ResourceProperties:         data.resourceUpdate.DesiredState.Properties,
				ResourceReadOnlyProperties: data.resourceUpdate.DesiredState.ReadOnlyProperties,
				Version:                    data.resourceUpdate.Version,
				FailureReason:              data.resourceUpdate.FailureReason,
			},
		)
		if err != nil {
			proc.Log().Error("Failed to send MarkAsComplete message to forma command persister commandID=%s ksuid=%s operation=%s: %v",
				data.commandID, data.originalResourceKsuidURI.KSUID(), data.resourceUpdate.Operation, err)
		} else {
			proc.Log().Debug("ResourceUpdater: MarkAsComplete call succeeded commandID=%s ksuid=%s operation=%s",
				data.commandID, data.originalResourceKsuidURI.KSUID(), data.resourceUpdate.Operation)
		}

		// Send a ResourceUpdateFinished message to the requester to inform it about the final state of the resource update.
		proc.Log().Debug("ResourceUpdater: sending ResourceUpdateFinished message to requester state=%s uri=%v", newState, data.originalResourceKsuidURI)
		err = proc.Send(
			data.requestedBy,
			ResourceUpdateFinished{
				Uri:   data.originalResourceKsuidURI,
				State: data.resourceUpdate.State,
			})
		if err != nil {
			proc.Log().Error("failed to send ResourceUpdateFinished message to requester: %v", err)
		}

		// Send ourselves a shutdown message to terminate the process.
		proc.Log().Debug("ResourceUpdater: sending shutdown message to self state=%s", newState)
		err = proc.Send(proc.PID(), Shutdown{})
		if err != nil {
			proc.Log().Error("ResourceUpdater: failed to send terminate message: %v", err)
		}
	}
	return newState, data, nil
}

func start(from gen.PID, state gen.Atom, data ResourceUpdateData, message StartResourceUpdate, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	data.requestedBy = from
	data.resourceUpdate = &message.ResourceUpdate
	data.commandID = message.CommandID
	data.commandSource = message.ResourceUpdate.Source
	data.applyMode = message.Mode
	data.resourceUpdate.StartTs = util.TimeNow()
	data.resourceUpdate.ModifiedTs = data.resourceUpdate.StartTs
	data.originalResourceKsuidURI = data.resourceUpdate.DesiredState.URI()

	// Convert target config to plugin format: strip $ref/$value metadata so plugins
	// receive plain values. This is needed when the target config was resolved via
	// target resolvables and the stored config still contains $ref/$value objects.
	if data.resourceUpdate.ResourceTarget.Config != nil {
		pluginConfig, err := resolver.ConvertToPluginFormat(data.resourceUpdate.ResourceTarget.Config)
		if err == nil {
			data.resourceUpdate.ResourceTarget.Config = pluginConfig
		}
		// The provider boundary for the target's config. It rides along on every
		// plugin operation this update performs — reads and deletes included, both
		// of which need the target's real credentials — so the guard runs on
		// whatever config was settled on above, converted or not. A generator
		// reference here is a credential that was never drawn; handing the
		// envelope to the plugin puts a JSON object where a token belongs.
		if err := resolver.GuardNoUnresolvedGenerators(data.resourceUpdate.ResourceTarget.Config); err != nil {
			proc.Log().Error("target config is not writable to a plugin target=%s: %v",
				data.resourceUpdate.ResourceTarget.Label, err)
			data.resourceUpdate.FailureReason = failureReasonUndrawnGeneratorValueInTargetConfig
			data.resourceUpdate.MarkAsFailed()
			return StateFinishedWithError, data, nil, nil
		}
	}

	// Get LabelConfig from PluginCoordinator (handles both external and local plugins)
	namespace := data.resourceUpdate.DesiredState.Namespace()
	result, err := proc.Call(
		gen.ProcessID{Name: actornames.PluginCoordinator, Node: proc.Node().Name()},
		messages.GetPluginInfo{Namespace: namespace})
	if err == nil {
		if infoResp, ok := result.(messages.PluginInfoResponse); ok && infoResp.Found {
			data.labelConfig = infoResp.LabelConfig
		}
	}

	return nextState(state, data, proc)
}

func synchronize(state gen.Atom, data ResourceUpdateData, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	data.stage = state
	// When a resource's target has been deleted (e.g., unmanaged resources
	// orphaned after a destroy), the target config will be nil. We can't
	// call the plugin without a target config, so short-circuit to NotFound.
	// The "deleted OOB" path in the ResourcePersister will then remove the
	// resource from the DB.
	if data.resourceUpdate.ResourceTarget.Config == nil {
		proc.Log().Debug("ResourceUpdater: target config is nil for resource %v (target %s was likely deleted), treating as NotFound",
			data.resourceUpdate.DesiredState.URI(), data.resourceUpdate.ResourceTarget.Label)
		now := util.TimeNow()
		notFound := plugin.TrackedProgress{
			ProgressResult: resource.ProgressResult{
				Operation:       resource.OperationRead,
				OperationStatus: resource.OperationStatusSuccess,
				ErrorCode:       resource.OperationErrorCodeNotFound,
			},
			ResourceType: data.resourceUpdate.DesiredState.Type,
			StartTs:      now,
			ModifiedTs:   now,
		}
		return handleProgressUpdate(gen.PID{}, state, data, notFound, proc)
	}

	// Convert properties to plugin format (extracts $value from opaque structures).
	// This state only ever prepares a Read call — it never writes DesiredState/PriorState
	// to the cloud — so use the Read-safe conversion: a schema-opaque field already hashed
	// at rest (the steady state for a secret) must not be rejected here, or every
	// sync/OOB-check Read of a secret-bearing resource would fail permanently.
	convertedResource, err := convertResourceForPluginRead(data.resourceUpdate.DesiredState)
	if err != nil {
		proc.Log().Error("failed to convert resource properties for plugin: %v", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	convertedExisting, err := convertResourceForPluginRead(data.resourceUpdate.PriorState)
	if err != nil {
		proc.Log().Error("failed to convert existing resource properties for plugin: %v", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	progress, operatorRetryConfig, err := doPluginOperation(data.resourceUpdate.DesiredState.URI(), plugin.ReadResource{
		Namespace:         convertedExisting.Namespace(),
		ResourceType:      convertedResource.Type,
		ResourceNamespace: convertedResource.Namespace(),
		ExistingResource:  convertedExisting,
		Resource:          convertedResource,
		TargetConfig:      data.resourceUpdate.ResourceTarget.Config,
		NativeID:          data.resourceUpdate.DesiredState.NativeID,
		IsSync:            data.resourceUpdate.IsSync(),
		IsDelete:          data.resourceUpdate.IsDelete(),
	}, proc)
	if err != nil {
		proc.Log().Error("failed to synchronize resource resourceURI=%v: %v", data.resourceUpdate.DesiredState.URI(), err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	data.operatorRetryConfig = operatorRetryConfig

	return handleProgressUpdate(gen.PID{}, state, data, *progress, proc)
}

func delete(state gen.Atom, data ResourceUpdateData, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	data.stage = state
	// Convert properties to plugin format (extracts $value from opaque structures).
	// A delete only ever needs identity (NativeID/TargetConfig) — it never writes
	// DesiredState's property values to the cloud — so use the Read-safe (unguarded)
	// conversion. A schema-opaque field can still carry its stored $hashed marker
	// here: the pre-delete synchronize() Read merges in only what the plugin's Read
	// actually returns, so a non-enriching secret (one the plugin's Read never
	// returns) leaves the stored hash untouched. The guarded converter would reject
	// that hash even though nothing is ever written from it, permanently failing
	// destroy for any resource with a non-enriching hashed secret.
	convertedResource, err := convertResourceForPluginRead(data.resourceUpdate.DesiredState)
	if err != nil {
		proc.Log().Error("failed to convert resource properties for plugin: %v", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	deleteOperation := plugin.DeleteResource{
		Namespace:    convertedResource.Namespace(),
		NativeID:     convertedResource.NativeID,
		Resource:     convertedResource,
		ResourceType: convertedResource.Type,
		TargetConfig: data.resourceUpdate.ResourceTarget.Config,
	}

	// First we check if progress already was made on the delete operation. This can happen for example if the node crashed while the
	// delete operation was in progress. If so, we try to recover from the previous progress.
	if found, lastKnownProgress := data.resourceUpdate.FindProgress(resource.OperationDelete); found {
		return recoverFromPreviousProgress(StateDeleting, data, lastKnownProgress, deleteOperation, proc)
	}

	// If no progress was made yet, we start a new delete operation.
	result, operatorRetryConfig, err := doPluginOperation(
		data.resourceUpdate.DesiredState.URI(),
		deleteOperation,
		proc)
	if err != nil {
		proc.Log().Error("failed to start delete operation resourceURI=%v: %v", data.resourceUpdate.DesiredState.URI(), err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	data.operatorRetryConfig = operatorRetryConfig

	return handleProgressUpdate(gen.PID{}, state, data, *result, proc)
}

// resolvingTimeout sizes the ResolveCache timeout to outlive the cache's
// worst-case resolve wall time: MaxRetries+1 reads (the initial read plus
// MaxRetries retries, each up to the plugin call timeout) plus the exponential
// backoff budget the ResolveCache schedules with (RetryStrategy.MaxTotalDelay),
// plus a margin. The backoff term is derived from the same RetryStrategy the
// cache retries with, so a tuned or exponential policy cannot make the two
// drift: a flat MaxRetries*RetryDelay estimate would under-cover exponential
// throttling backoff and trip this timeout mid-retry.
func resolvingTimeout(cfg pkgmodel.RetryConfig) time.Duration {
	const resolveCacheMargin = 30 * time.Second
	perAttempt := time.Duration(PluginOperationCallTimeout) * time.Second
	strategy := resource.RetryStrategy{MaxRetries: cfg.MaxRetries, BaseDelay: cfg.RetryDelay}
	return time.Duration(cfg.MaxRetries+1)*perAttempt + strategy.MaxTotalDelay() + resolveCacheMargin
}

// missingInActionMargin covers everything the watchdog window must absorb on
// top of the operator's own cadence.
const missingInActionMargin = 10 * time.Second

// missingInActionTimeout sizes the plugin-operator watchdog to outlive the
// longest legitimate gap between two progress reports: one scheduled sleep plus
// one plugin call plus a margin. The sleep is whichever of the operator's own
// delays is longest — the status-check interval it parks on while an operation
// is in progress, the flat RetryDelay it sleeps after a recoverable error, or
// the largest exponential backoff it can schedule for a throttled attempt. That
// backoff is Backoff(MaxRetries+1) because attempts are 1-based and a retry is
// still scheduled on the last allowed attempt, and the flat RetryDelay stays a
// term of its own because Backoff returns the base delay uncapped for the first
// attempt, so a RetryDelay above the backoff cap outlasts every backoff. The
// zero term keeps a negative duration in config from shrinking the window.
// The margin budgets the ResourceUpdater's mailbox delay, actor scheduling on
// both sides, cross-node transport, and the synchronous UpdateResourceProgress
// persister call the updater makes before it processes the re-arm action.
// RetryStrategy.MaxTotalDelay is deliberately not used: it budgets the whole
// retry ladder, which is right for an end-to-end wait but would delay detecting
// a genuinely dead operator by the sum of every backoff rather than one gap.
func missingInActionTimeout(cfg pkgmodel.RetryConfig) time.Duration {
	strategy := resource.RetryStrategy{MaxRetries: cfg.MaxRetries, BaseDelay: cfg.RetryDelay}
	longestSleep := max(cfg.StatusCheckInterval, cfg.RetryDelay, strategy.Backoff(cfg.MaxRetries+1), 0)
	return longestSleep + PluginCallTimeout + missingInActionMargin
}

// watchdogRetryConfig returns the config the watchdog window is derived from:
// the one the watched plugin operator was spawned with, which is the per-plugin
// override wherever one is configured. The node-global config is only a
// fallback for a spawn result that reported none — both arming sites run after
// an operator has been spawned and its result received, so that fallback is
// defensive rather than a normal path.
func (data ResourceUpdateData) watchdogRetryConfig() pkgmodel.RetryConfig {
	if data.operatorRetryConfig != nil {
		return *data.operatorRetryConfig
	}
	return data.retryConfig
}

func resolve(state gen.Atom, data ResourceUpdateData, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	data.stage = state
	if len(data.resourceUpdate.RemainingResolvables) == 0 {
		return nextState(state, data, proc)
	}

	first := data.resourceUpdate.RemainingResolvables[0]
	data.resourceUpdate.RemainingResolvables = data.resourceUpdate.RemainingResolvables[1:]

	err := proc.Send(
		gen.ProcessID{
			Node: proc.Node().Name(),
			Name: actornames.ResolveCache(data.commandID),
		},
		messages.ResolveValue{
			ResourceURI: first,
		})
	if err != nil {
		return StateResolving, data, nil, fmt.Errorf("failed to send ResolveValue message to resolve cache: %w", err)
	}

	timeout := statemachine.StateTimeout{
		Duration: resolvingTimeout(data.retryConfig),
		Message:  ResolveTimedOut{},
	}

	return StateResolving, data, []statemachine.Action{timeout}, nil
}

func resourceResolved(from gen.PID, state gen.Atom, data ResourceUpdateData, message messages.ValueResolved, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	if message.SourceRootDigest != "" {
		if data.resourceUpdate.ResolvedRootDigests == nil {
			data.resourceUpdate.ResolvedRootDigests = make(map[string]string)
		}
		data.resourceUpdate.ResolvedRootDigests[string(message.ResourceURI)] = message.SourceRootDigest
	}
	err := data.resourceUpdate.ResolveValue(message.ResourceURI, message.Value, data.applyMode)
	if err != nil {
		proc.Log().Error("failed to resolve value for resource update resourceURI=%v: %v", message.ResourceURI, err)
		// LateCreateOnlyChangeError is already a fixed, redacted text (it names
		// only the changed field, never a value) — record it verbatim. Every
		// other resolve/regen failure can carry error detail built from
		// user-authored property paths (see updateRequestFailureReason's
		// doc), so it must route through the same redaction mapping the rest
		// of the update path uses rather than recording the raw error.
		var late LateCreateOnlyChangeError
		if errors.As(err, &late) {
			data.resourceUpdate.FailureReason = late.Error()
		} else {
			data.resourceUpdate.FailureReason = updateRequestFailureReason(err)
		}
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	return resolve(state, data, proc)
}

func create(state gen.Atom, data ResourceUpdateData, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	data.stage = state
	// A reason recorded by an earlier attempt must not outlive it: MarkAsFailed
	// does not clear it, so a retried or resumed create would otherwise report a
	// failure that no longer describes it.
	data.resourceUpdate.FailureReason = ""
	// Convert properties to plugin format (extracts $value from opaque structures)
	convertedResource, err := convertResourceForPlugin(data.resourceUpdate.DesiredState)
	if err != nil {
		proc.Log().Error("failed to convert resource properties for plugin: %v", err)
		data.resourceUpdate.FailureReason = createRequestFailureReason(err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	createOperation := plugin.CreateResource{
		Namespace:    convertedResource.Namespace(),
		ResourceType: convertedResource.Type,
		Label:        convertedResource.Label,
		Properties:   convertedResource.Properties,
		TargetConfig: data.resourceUpdate.ResourceTarget.Config,
	}

	// First we check if progress already was made on the create operation. This can happen for example if the node crashed while the
	// create operation was in progress. If so, we try to recover from the previous progress.
	if found, lastKnownProgress := data.resourceUpdate.FindProgress(resource.OperationCreate); found {
		return recoverFromPreviousProgress(StateCreating, data, lastKnownProgress, createOperation, proc)
	}

	result, operatorRetryConfig, err := doPluginOperation(
		data.resourceUpdate.DesiredState.URI(),
		createOperation,
		proc)
	if err != nil {
		proc.Log().Error("failed to start create operation: %v", err)
		data.resourceUpdate.FailureReason = failureReasonPluginDispatchOnCreate
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	data.operatorRetryConfig = operatorRetryConfig

	return handleProgressUpdate(gen.PID{}, state, data, *result, proc)
}

// Failures while building or dispatching a plugin request precede any recorded
// plugin progress, so the resource update would otherwise report an empty
// ErrorMessage — leaving the reason discoverable only in the agent's own log.
// These are the operator-facing texts for that boundary, worded per operation.
//
// They are fixed, never the underlying error: it names the property that failed,
// and a property path can carry user-authored map keys.
const (
	failureReasonUnrecoverableOpaqueValueOnUpdate = "cannot update this resource: formae holds only a stored hash of one of its secret properties, so it cannot send that value to the provider. Re-supply the value in your forma, or leave the provider's current value in place."
	failureReasonPluginRequestPreparationOnUpdate = "cannot update this resource: formae could not build the provider request from its recorded state."
	failureReasonUndrawnGeneratorValueOnUpdate    = "cannot update this resource: one of its properties is bound to a generator whose value has not been drawn, so formae has nothing to send to the provider. Declare the value directly instead of binding it to a generator."

	failureReasonUnrecoverableOpaqueValueOnCreate = "cannot create this resource: the desired value of one of its secret properties is a stored hash, which formae cannot send to the provider as the live value. Re-supply the value in your forma."
	failureReasonUndrawnGeneratorValueOnCreate    = "cannot create this resource: one of its properties is bound to a generator whose value has not been drawn, so formae has nothing to send to the provider. Declare the value directly instead of binding it to a generator."

	// Worded for the target rather than the operation: the target's config
	// rides along on every plugin call this update makes, so the same text is
	// right whether the update was creating, updating, reading or deleting.
	failureReasonUndrawnGeneratorValueInTargetConfig = "cannot reach the provider for this resource: its target's configuration is bound to a generator whose value has not been drawn, so formae has nothing to authenticate with. Declare the value directly instead of binding it to a generator."
	failureReasonPluginRequestPreparationOnCreate    = "cannot create this resource: formae could not build the provider request for it."
	// Dispatching covers both a coordinator that never returned an operator and
	// a call that did not complete after the create was handed to the plugin, so
	// the text asserts neither that a plugin was reached nor that the create
	// never started.
	failureReasonPluginDispatchOnCreate = "cannot create this resource: formae could not complete the request to the provider plugin, so the resource may or may not have been created — check the provider before retrying."
)

// isUnrecoverableOpaqueValue reports whether preparing a plugin request failed
// because formae holds only a stored hash of an opaque value.
func isUnrecoverableOpaqueValue(err error) bool {
	return errors.Is(err, resolver.ErrHashedValueNotWritable)
}

// isUndrawnGeneratorValue reports whether preparing a plugin request failed
// because a property still holds a generator reference rather than a value.
func isUndrawnGeneratorValue(err error) bool {
	return errors.Is(err, resolver.ErrUnresolvedGeneratorReferenceNotWritable)
}

// updateRequestFailureReason maps a plugin-request preparation error to the
// fixed reason recorded on the resource update.
func updateRequestFailureReason(err error) string {
	if isUnrecoverableOpaqueValue(err) {
		return failureReasonUnrecoverableOpaqueValueOnUpdate
	}
	if isUndrawnGeneratorValue(err) {
		return failureReasonUndrawnGeneratorValueOnUpdate
	}
	return failureReasonPluginRequestPreparationOnUpdate
}

// createRequestFailureReason is updateRequestFailureReason's counterpart for a
// create, whose remedies differ: there is no provider-side current value to
// leave in place.
func createRequestFailureReason(err error) string {
	if isUnrecoverableOpaqueValue(err) {
		return failureReasonUnrecoverableOpaqueValueOnCreate
	}
	if isUndrawnGeneratorValue(err) {
		return failureReasonUndrawnGeneratorValueOnCreate
	}
	return failureReasonPluginRequestPreparationOnCreate
}

func update(state gen.Atom, data ResourceUpdateData, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	data.stage = state
	// A reason recorded by an earlier attempt must not outlive it: MarkAsFailed
	// does not clear it, so a retried or resumed update would otherwise report a
	// failure that no longer describes it.
	data.resourceUpdate.FailureReason = ""
	// When bringing a resource under management without property changes, skip the plugin
	// Update call since there are no actual changes to make in the cloud. Instead, create
	// a synthetic ProgressResult using the existing resource's properties.
	hasEmptyPatch := data.resourceUpdate.DesiredState.PatchDocument == nil ||
		string(data.resourceUpdate.DesiredState.PatchDocument) == "[]" ||
		string(data.resourceUpdate.DesiredState.PatchDocument) == ""

	isBringingUnderManagement := data.resourceUpdate.PriorState.Stack == constants.UnmanagedStack &&
		data.resourceUpdate.DesiredState.Stack != constants.UnmanagedStack

	// A label-only change (label differs, same stack, same target,
	// no property delta) is a metadata-only update. Skip the plugin call for
	// the same reason "bringing under management without property changes"
	// does — there is nothing for the cloud to do.
	isLabelOnlyChange := data.resourceUpdate.PriorState.Label != data.resourceUpdate.DesiredState.Label &&
		data.resourceUpdate.PriorState.Stack == data.resourceUpdate.DesiredState.Stack &&
		data.resourceUpdate.PriorState.Target == data.resourceUpdate.DesiredState.Target

	// A record-only update carries no property, stack, label, or target
	// delta — planning built it solely to commit a shifted ownership record
	// (see NewResourceUpdateForExisting). Its patch is always empty by
	// construction, so hasEmptyPatch is included here only for symmetry with
	// the other two predicates, never as a distinguishing condition.
	isRecordOnly := data.resourceUpdate.RecordOnly

	if (isBringingUnderManagement || isLabelOnlyChange || isRecordOnly) && hasEmptyPatch {
		switch {
		case isLabelOnlyChange:
			proc.Log().Debug("Renaming resource without property changes resourceURI=%v oldLabel=%s newLabel=%s",
				data.resourceUpdate.DesiredState.URI(), data.resourceUpdate.PriorState.Label, data.resourceUpdate.DesiredState.Label)
		case isRecordOnly:
			proc.Log().Debug("Committing ownership record without property changes resourceURI=%v",
				data.resourceUpdate.DesiredState.URI())
		default:
			proc.Log().Debug("Bringing resource under management without property changes resourceURI=%v oldStack=%s newStack=%s",
				data.resourceUpdate.DesiredState.URI(), data.resourceUpdate.PriorState.Stack, data.resourceUpdate.DesiredState.Stack)
		}

		// Merge Properties and ReadOnlyProperties to get complete cloud state
		completeProperties, err := util.MergeJSON(
			data.resourceUpdate.PriorState.Properties,
			data.resourceUpdate.PriorState.ReadOnlyProperties,
		)
		if err != nil {
			proc.Log().Error("failed to merge properties when handling metadata-only update: %v", err)
			data.resourceUpdate.MarkAsFailed()
			return StateFinishedWithError, data, nil, nil
		}

		statusMessage := "Brought under management without property changes"
		switch {
		case isLabelOnlyChange:
			statusMessage = "Renamed without property changes"
		case isRecordOnly:
			statusMessage = "Committed ownership record without property changes"
		}

		// Create synthetic ProgressResult with existing resource data
		// No ConvertToPluginFormat needed - properties are already in plain JSON format
		syntheticResult := plugin.TrackedProgress{
			ProgressResult: resource.ProgressResult{
				Operation:          resource.OperationUpdate,
				OperationStatus:    resource.OperationStatusSuccess,
				StatusMessage:      statusMessage,
				NativeID:           data.resourceUpdate.PriorState.NativeID,
				ResourceProperties: completeProperties,
			},
			Attempts:    1,
			MaxAttempts: 1,
		}

		return handleProgressUpdate(proc.PID(), state, data, syntheticResult, proc)
	}

	// setOnce keeps a value by substituting the STORED one into the desired
	// properties, which for an opaque field is a digest. Swap such a leaf for a
	// present-but-unusable sentinel before the guarded conversion below, so the
	// guard that protects the value stops freezing every other property on the
	// resource. Only this copy changes; DesiredState.Properties stays the
	// durable record of the stored hash.
	desiredForPlugin := data.resourceUpdate.DesiredState
	// Complete each co-owned collection to its intended post-write value
	// (declared plus never-owned live members), so DesiredProperties and
	// PatchDocument tell the plugin the same thing. Only this copy changes;
	// DesiredState.Properties stays the declared-only durable record the
	// write-echo recompute claims from.
	projectedProperties, err := patch.ProjectDesiredForWrite(
		desiredForPlugin.Properties,
		data.resourceUpdate.PriorState.Properties,
		data.resourceUpdate.PriorState.OwnedMembers,
		desiredForPlugin.Schema,
	)
	if err != nil {
		proc.Log().Error("failed to project co-owned desired properties for plugin: %v", err)
		data.resourceUpdate.FailureReason = updateRequestFailureReason(err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	desiredForPlugin.Properties = projectedProperties

	frozenProperties, err := FreezeUnrecoverableOpaqueValues(
		data.resourceUpdate.PriorState.Properties,
		desiredForPlugin.Properties,
		data.resourceUpdate.PriorState.Schema,
		desiredForPlugin.Schema,
		desiredForPlugin.Type,
	)
	if err != nil {
		proc.Log().Error("failed to prepare desired resource properties for plugin: %v", err)
		data.resourceUpdate.FailureReason = updateRequestFailureReason(err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	desiredForPlugin.Properties = frozenProperties

	// A generator binding the planner classified stable draws no value, so its
	// destination still holds the bare envelope here. Swap it for the same
	// present-but-unusable sentinel, so the guard that refuses to send a
	// reference in a secret's place stops blocking every other property on the
	// resource. Only this copy changes; DesiredState.Properties stays the
	// durable record of the binding.
	frozenProperties, err = FreezeStableGeneratorBindings(
		desiredForPlugin.Properties,
		data.resourceUpdate.ProvenanceRecords,
	)
	if err != nil {
		proc.Log().Error("failed to prepare desired resource properties for plugin: %v", err)
		data.resourceUpdate.FailureReason = updateRequestFailureReason(err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	desiredForPlugin.Properties = frozenProperties

	// Convert properties to plugin format (extracts $value from opaque structures).
	// DesiredState is the NEW value being written to the cloud as DesiredProperties,
	// so this stays guarded: a stored hash must never be sent to a plugin in place
	// of the live secret. SuppressUnchangedOpaqueValues plus fresh forma input keep
	// this plaintext-or-suppressed by the time we get here, and a stored hash that
	// survives is one the freeze above deliberately declined to rewrite.
	convertedResource, err := convertResourceForPlugin(desiredForPlugin)
	if err != nil {
		proc.Log().Error("failed to convert resource properties for plugin: %v", err)
		data.resourceUpdate.FailureReason = updateRequestFailureReason(err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	// PriorState becomes PriorProperties: prior/diff CONTEXT for the plugin, not a
	// value being written. Use the Read-safe (unguarded) conversion — the pre-update
	// synchronize() Read only merges in what the plugin's Read actually returns, so a
	// non-enriching secret (one the plugin's Read never returns) leaves PriorState's
	// stored $hashed marker untouched. The guarded converter would reject that hash
	// even though it is never written anywhere, permanently failing updates to any
	// other field on a resource with a non-enriching hashed secret.
	convertedExisting, err := convertResourceForPluginRead(data.resourceUpdate.PriorState)
	if err != nil {
		proc.Log().Error("failed to convert existing resource properties for plugin: %v", err)
		data.resourceUpdate.FailureReason = updateRequestFailureReason(err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	// PriorProperties is diff/context for the plugin, never a value
	// being written — the plugin has no legitimate use for the prior value of
	// an opaque field, hashed or not. convertResourceForPluginRead above
	// is deliberately unguarded (see its doc comment), so a non-enriching
	// secret's stored $hashed envelope survives conversion as a bare digest
	// with nothing left to mark it as hashed. Strip every opaque field here —
	// nested ones included — so no digest (or plaintext) for it ever reaches
	// the plugin via PriorProperties.
	priorProperties, opaqueDiagnostics, err := StripOpaqueFieldsForPriorProperties(
		convertedExisting.Properties,
		data.resourceUpdate.PriorState.Schema,
		data.resourceUpdate.DesiredState.Schema,
		data.resourceUpdate.DesiredState.Type,
	)
	if err != nil {
		proc.Log().Error("failed to strip opaque fields from prior properties: %v", err)
		data.resourceUpdate.FailureReason = updateRequestFailureReason(err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	for _, d := range opaqueDiagnostics {
		proc.Log().Warning("ambiguous opaque field hint redacting prior properties resourceLabel=%s resourceType=%s: %s",
			data.resourceUpdate.DesiredState.Label, data.resourceUpdate.DesiredState.Type, d.String())
	}

	updateOperation := plugin.UpdateResource{
		Namespace:         convertedResource.Namespace(),
		NativeID:          convertedResource.NativeID,
		ResourceType:      convertedResource.Type,
		Label:             convertedResource.Label,
		PriorProperties:   priorProperties,
		DesiredProperties: convertedResource.Properties,
		PatchDocument:     string(data.resourceUpdate.DesiredState.PatchDocument),
		TargetConfig:      data.resourceUpdate.ResourceTarget.Config,
	}

	// First we check if progress already was made on the update operation. This can happen for example if the node crashed while the
	// update operation was in progress. If so, we try to recover from the previous progress.
	if found, lastKnownProgress := data.resourceUpdate.FindProgress(resource.OperationUpdate); found {
		return recoverFromPreviousProgress(StateUpdating, data, lastKnownProgress, updateOperation, proc)
	}

	result, operatorRetryConfig, err := doPluginOperation(
		data.resourceUpdate.DesiredState.URI(),
		updateOperation,
		proc)
	if err != nil {
		proc.Log().Error("failed to start update operation: %v", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	data.operatorRetryConfig = operatorRetryConfig

	return handleProgressUpdate(gen.PID{}, state, data, *result, proc)
}

func recoverFromPreviousProgress(state gen.Atom, data ResourceUpdateData, lastKnownProgress *plugin.TrackedProgress, operation plugin.StatusCheck, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	data.stage = state
	// If the lastKnownProgress was finished, we can let the progress handler process the result and determine next steps.
	if lastKnownProgress.HasFinished() {
		return handleProgressUpdate(gen.PID{}, state, data, *lastKnownProgress, proc)
	}

	// Otherwise we retrieve the actual progress by spawning a plugin operator in the WaitingForResource state.
	// Pass the previous attempts count so the PluginOperator can continue from where it left off.
	actualProgress, operatorRetryConfig, err := resumeWaitingForResource(state, data, lastKnownProgress.ProgressResult, lastKnownProgress.Attempts, operation, proc)
	if err != nil {
		proc.Log().Error("failed to resume waiting for resource: %v", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}
	data.operatorRetryConfig = operatorRetryConfig

	// If the actual progress is finished, again we let the progress handler process the result and determine next steps.
	if actualProgress.HasFinished() {
		return handleProgressUpdate(gen.PID{}, state, data, *actualProgress, proc)
	}

	// If not, this means that the plugin operator is waiting for the operation to be completed, so we stay in the
	// deleting state and wait for the next progress update. We wait out the longest gap the operator's own retry
	// cadence can put between two progress reports before we give up on it.
	timeout := statemachine.StateTimeout{
		Duration: missingInActionTimeout(data.watchdogRetryConfig()),
		Message:  PluginOperatorMissingInAction{},
	}

	return state, data, []statemachine.Action{timeout}, nil
}

func resumeWaitingForResource(state gen.Atom, data ResourceUpdateData, progress resource.ProgressResult, previousAttempts int, operation plugin.StatusCheck, proc gen.Process) (*plugin.TrackedProgress, *pkgmodel.RetryConfig, error) {
	actualProgress, operatorRetryConfig, err := doPluginOperation(data.resourceUpdate.DesiredState.URI(), plugin.ResumeWaitingForResource{
		Namespace:         data.resourceUpdate.DesiredState.Namespace(),
		ResourceOperation: currentOperation(state),
		Request:           operation.StatusCheck(&progress),
		PreviousAttempts:  previousAttempts,
	}, proc)
	if err != nil {
		proc.Log().Error("failed to resume waiting for resource: %v", err)
		return nil, nil, fmt.Errorf("failed to resume waiting for resource: %w", err)
	}

	return actualProgress, operatorRetryConfig, nil
}

// handleProgressUpdate handles progress updates from th plugin operator. It persists any progress made, and moves the
// state machine to the next state when the plugin operation finished successfully. After the last plugin operation, or
// after the first error, it reports the final state to the stack updater and exits.
func handleProgressUpdate(from gen.PID, state gen.Atom, data ResourceUpdateData, message plugin.TrackedProgress, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	data.stage = state
	err := data.resourceUpdate.RecordProgress(&message)
	if err != nil {
		proc.Log().Error("failed to record progress for resource update: %v", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	// If the plugin operation has finished successfully, persist the resource
	// BEFORE recording the progress as Success. This prevents a crash window
	// where the command record shows Success but the resource was never written.
	if message.FinishedSuccessfully() {
		// Emit reachable target-health observation on every successful path,
		// including discovery filter matches and synchronizing rejects that
		// return early below without reaching the normal emission point.
		if obs, ok := targetHealthObservation(data.resourceUpdate.ResourceTarget.Label, &message, time.Now()); ok {
			if err := proc.Send(resourcePersisterProcess(proc), messages.UpdateTargetHealth{Observation: obs}); err != nil {
				proc.Log().Warning("failed to send UpdateTargetHealth for target %s: %v", obs.TargetLabel, err)
			}
		}

		if data.commandSource == FormaCommandSourceDiscovery {
			// Merge Properties and ReadOnlyProperties to get complete cloud state for filtering
			completeProperties, mergeErr := util.MergeJSON(
				data.resourceUpdate.DesiredState.Properties,
				data.resourceUpdate.DesiredState.ReadOnlyProperties,
			)
			if mergeErr != nil {
				proc.Log().Warning("failed to merge properties for filter, using Properties only: %v", mergeErr)
				completeProperties = data.resourceUpdate.DesiredState.Properties
			}

			// Check if resource should be filtered using MatchFilters (declarative, OR logic)
			shouldFilter := false
			for i := range data.resourceUpdate.MatchFilters {
				if ShouldFilterByMatchFilter(&data.resourceUpdate.MatchFilters[i], completeProperties) {
					shouldFilter = true
					break
				}
			}
			if shouldFilter {
				proc.Log().Debug("Skipping discovered resource resourceType=%s nativeID=%s",
					data.resourceUpdate.DesiredState.Type, data.resourceUpdate.DesiredState.NativeID)
				data.resourceUpdate.MarkAsSuccess()
				return StateFinishedSuccessfully, data, nil, nil
			}

			// Calculate and set the label for discovered (unmanaged) resources.
			data.resourceUpdate.DesiredState.Label = data.resourceLabeler.LabelForUnmanagedResource(
				data.resourceUpdate.DesiredState.NativeID,
				data.resourceUpdate.DesiredState.Type,
				data.resourceUpdate.DesiredState.Properties,
				data.labelConfig,
				data.labelTagKeys,
			)
		}

		operation := currentOperation(state)
		hash, err := proc.Call(resourcePersisterProcess(proc), PersistResourceUpdate{
			CommandID:         data.commandID,
			ResourceOperation: data.resourceUpdate.Operation,
			PluginOperation:   operation,
			ResourceUpdate:    *data.resourceUpdate,
		})

		if err != nil {
			proc.Log().Error("failed to persist resource update: %v", err)
			data.resourceUpdate.MarkAsFailed()
			return StateFinishedWithError, data, nil, nil
		}
		data.resourceUpdate.Version = hash.(string)

		// If we successfully persisted the read operation in the Synchronizing state, we should reject the resource update
		// and exit the state machine.
		if state == StateSynchronizing && data.resourceUpdate.Operation != OperationRead && operation == resource.OperationRead && hash != "" && !data.resourceUpdate.IsDelete() {
			proc.Log().Debug("Resource update rejected as a change to the resource was detected previousProperties=%s currentProperties=%s",
				pkgmodel.RedactOpaqueJSONForLog(data.resourceUpdate.PreviousProperties),
				pkgmodel.RedactOpaqueJSONForLog(data.resourceUpdate.DesiredState.Properties))
			data.resourceUpdate.Reject()

			return StateRejected, data, nil, nil
		}

		// Now that the resource is persisted, record the success progress in
		// the command record. This ordering ensures that if we crash between
		// the resource persist and this call, the resource exists and the
		// command re-run will handle it correctly (idempotent).
		proc.Log().Debug("ResourceUpdater: persisting success progress after resource persist state=%s resourceURI=%v",
			state, data.resourceUpdate.DesiredState.URI())
		_, err = proc.Call(
			formaCommandPersisterProcess(proc),
			messages.UpdateResourceProgress{
				CommandID:           data.commandID,
				ResourceURI:         data.resourceUpdate.DesiredState.URI(),
				Operation:           data.resourceUpdate.Operation,
				ResourceStartTs:     data.resourceUpdate.StartTs,
				ResourceModifiedTs:  data.resourceUpdate.ModifiedTs,
				ResourceState:       data.resourceUpdate.State,
				Progress:            message,
				ResolvedRootDigests: data.resourceUpdate.ResolvedRootDigests,
			},
		)
		if err != nil {
			proc.Log().Error("failed to send UpdateResourceProgress after resource persist: %v", err)
			// Resource is already persisted; don't fail the update for a
			// progress bookkeeping error — the onStateChange handler will
			// send MarkResourceUpdateAsComplete anyway.
		}

		return nextState(state, data, proc)
	}

	// For non-success progress (in-progress or failed), persist progress
	// to the command record immediately.
	proc.Log().Debug("ResourceUpdater: sending progress update to the forma command persister state=%s resourceURI=%v progress=%s",
		state, data.resourceUpdate.DesiredState.URI(), message.Operation)
	_, err = proc.Call(
		formaCommandPersisterProcess(proc),
		messages.UpdateResourceProgress{
			CommandID:           data.commandID,
			ResourceURI:         data.resourceUpdate.DesiredState.URI(),
			Operation:           data.resourceUpdate.Operation,
			ResourceStartTs:     data.resourceUpdate.StartTs,
			ResourceModifiedTs:  data.resourceUpdate.ModifiedTs,
			ResourceState:       data.resourceUpdate.State,
			Progress:            message,
			ResolvedRootDigests: data.resourceUpdate.ResolvedRootDigests,
		},
	)
	if err != nil {
		proc.Log().Error("failed to send UpdateResourceProgress message to forma command persister: %v", err)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	if message.Failed() {
		if obs, ok := targetHealthObservation(data.resourceUpdate.ResourceTarget.Label, &message, time.Now()); ok {
			if err := proc.Send(resourcePersisterProcess(proc), messages.UpdateTargetHealth{Observation: obs}); err != nil {
				proc.Log().Warning("failed to send UpdateTargetHealth for target %s: %v", obs.TargetLabel, err)
			}
		}
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, nil
	}

	// If the plugin operation is still in progress, we stay in the current state and wait for the next progress update. We set up
	// a state timeout in case the plugin operator crashes, sized to the longest gap its own retry cadence can put between two
	// progress reports.
	timeout := statemachine.StateTimeout{
		Duration: missingInActionTimeout(data.watchdogRetryConfig()),
		Message:  PluginOperatorMissingInAction{},
	}

	return state, data, []statemachine.Action{timeout}, nil
}

func nextState(state gen.Atom, data ResourceUpdateData, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	switch state {
	case StateInitializing:
		switch data.resourceUpdate.Operation {
		case OperationCreate:
			return resolve(StateResolving, data, proc)
		case OperationUpdate:
			// If the resource update has progress, we go to the updating state, otherwise we synchronize.
			if data.resourceUpdate.HasProgress() {
				return update(StateUpdating, data, proc)
			}
		case OperationDelete:
			// If the resource update has progress, we go to the deleting state, otherwise we synchronize.
			if data.resourceUpdate.HasProgress() {
				return delete(StateDeleting, data, proc)
			}
		default:
		}
		return synchronize(StateSynchronizing, data, proc)
	case StateSynchronizing:
		if data.resourceUpdate.IsSync() {
			return StateFinishedSuccessfully, data, nil, nil
		}
		if data.resourceUpdate.RequiresDelete() {
			return delete(StateDeleting, data, proc)
		}
		return resolve(StateResolving, data, proc)
	case StateDeleting:
		if !data.resourceUpdate.IsCreate() && !data.resourceUpdate.IsUpdate() {
			data.resourceUpdate.MarkAsSuccess()
			return StateFinishedSuccessfully, data, nil, nil
		}
		return resolve(StateResolving, data, proc)
	case StateResolving:
		if data.resourceUpdate.IsCreate() {
			return create(StateCreating, data, proc)
		}
		return update(StateUpdating, data, proc)
	case StateCreating:
		data.resourceUpdate.MarkAsSuccess()
		return StateFinishedSuccessfully, data, nil, nil
	case StateUpdating:
		data.resourceUpdate.MarkAsSuccess()
		return StateFinishedSuccessfully, data, nil, nil
	default:
		// We should never reach this point, so if we do we exit the state machine with an error.
		proc.Log().Error("ResourceUpdater reached an unexpected state state=%s commandID=%s ksuid=%s operation=%s", state, data.commandID, data.originalResourceKsuidURI.KSUID(), data.resourceUpdate.Operation)
		data.resourceUpdate.MarkAsFailed()
		return StateFinishedWithError, data, nil, gen.TerminateReasonPanic
	}
}

// doPluginOperation spawns a PluginOperator for the operation and runs it to its
// next progress report. Alongside the progress it returns the retry config the
// coordinator spawned that operator with, so the caller can size its watchdog
// from the cadence the operator actually polls on. That config is nil when the
// coordinator reported none.
func doPluginOperation(resourceURI pkgmodel.FormaeURI, operation plugin.PluginOperation, proc gen.Process) (*plugin.TrackedProgress, *pkgmodel.RetryConfig, error) {
	// Generate a random operationID based on UUID
	operationID := uuid.New().String()

	// Spawn a PluginOperator via PluginCoordinator
	proc.Log().Debug("Spawning plugin operator via PluginCoordinator resourceURI=%v operation=%s namespace=%s",
		resourceURI, string(operation.Operation()), operation.PluginNamespace())

	spawnResult, err := proc.Call(
		gen.ProcessID{Name: actornames.PluginCoordinator, Node: proc.Node().Name()},
		messages.SpawnPluginOperator{
			Namespace:   operation.PluginNamespace(),
			ResourceURI: string(resourceURI),
			Operation:   string(operation.Operation()),
			OperationID: operationID,
			RequestedBy: proc.PID(),
		})
	if err != nil {
		proc.Log().Error("failed to spawn plugin operator: %v", err)
		return nil, nil, fmt.Errorf("failed to spawn plugin operator: %w", err)
	}

	result, ok := spawnResult.(messages.SpawnPluginOperatorResult)
	if !ok {
		return nil, nil, fmt.Errorf("expected SpawnPluginOperatorResult, got %T", spawnResult)
	}
	if result.Error != "" {
		return nil, nil, fmt.Errorf("spawn plugin operator failed: %s", result.Error)
	}

	// Call the spawned PluginOperator with the operation
	proc.Log().Debug("Resource updater: calling plugin operator process resourceURI=%v operation=%s pid=%v",
		resourceURI, operation.Operation(), result.PID)

	response, err := proc.CallWithTimeout(result.PID, operation, PluginOperationCallTimeout)
	if err != nil {
		return nil, nil, err
	}

	progressResult, ok := response.(plugin.TrackedProgress)
	if !ok {
		return nil, nil, fmt.Errorf("expected TrackedProgress, got %T", response)
	}

	return &progressResult, result.RetryConfig, nil
}

func pluginOperationMissingInAction(from gen.PID, state gen.Atom, data ResourceUpdateData, message PluginOperatorMissingInAction, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	proc.Log().Error("Plugin operator is missing in action state=%s commandID=%s ksuid=%s operation=%s", state, data.commandID, data.originalResourceKsuidURI.KSUID(), data.resourceUpdate.Operation)
	data.resourceUpdate.MarkAsFailed()
	return StateFinishedWithError, data, nil, nil
}

func resolveTimedOut(from gen.PID, state gen.Atom, data ResourceUpdateData, message ResolveTimedOut, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	proc.Log().Error("Resolve cache is missing in action state=%s commandID=%s ksuid=%s operation=%s", state, data.commandID, data.originalResourceKsuidURI.KSUID(), data.resourceUpdate.Operation)
	data.resourceUpdate.MarkAsFailed()
	return StateFinishedWithError, data, nil, nil
}

func resourceFailedToResolve(from gen.PID, state gen.Atom, data ResourceUpdateData, message messages.FailedToResolveValue, proc gen.Process) (gen.Atom, ResourceUpdateData, []statemachine.Action, error) {
	proc.Log().Error("Failed to resolve resource property resourceUri=%v reason=%s", message.ResourceURI, message.Reason)
	// The resolve failure precedes any plugin operation, so no progress is
	// recorded. Carry the reason explicitly so it surfaces as the failed
	// resource update's ErrorMessage instead of an empty string.
	data.resourceUpdate.FailureReason = message.Reason
	data.resourceUpdate.MarkAsFailed()
	return StateFinishedWithError, data, nil, nil
}

// ShouldFilterByMatchFilter checks if a resource should be filtered using declarative MatchFilter.
// Returns true if all conditions match (AND logic), indicating the resource should be excluded.
func ShouldFilterByMatchFilter(filter *pkgmodel.MatchFilter, properties json.RawMessage) bool {
	if filter == nil {
		return false
	}

	// A filter naming no conditions excludes nothing. Reading it as a vacuous
	// AND would make it exclude everything it is scoped to, so the emptiest
	// filter anyone can write would be the most destructive one.
	if len(filter.Conditions) == 0 {
		return false
	}

	// All conditions must match (AND logic) to exclude
	for _, cond := range filter.Conditions {
		if !evaluateCondition(cond, properties) {
			return false
		}
	}

	return true // All conditions matched - exclude this resource
}

// evaluateCondition evaluates a single filter condition using JSONPath.
// PropertyPath is a JSONPath expression to query properties.
// PropertyValue: empty = existence check, non-empty = exact string match.
func evaluateCondition(cond pkgmodel.FilterCondition, properties json.RawMessage) bool {
	var data any
	if err := json.Unmarshal(properties, &data); err != nil {
		return false
	}

	path, err := jsonpathParser.Parse(cond.PropertyPath)
	if err != nil {
		// Invalid JSONPath expression - no match
		return false
	}

	nodes, ok := selectNodes(path, cond.PropertyPath, data)
	if !ok || len(nodes) == 0 {
		// No value found
		return false
	}

	// Empty PropertyValue = existence check (path returned something)
	if cond.PropertyValue == "" {
		return true
	}

	// Non-empty PropertyValue = exact string match against any result
	for _, node := range nodes {
		if matchValue(node, cond.PropertyValue) {
			return true
		}
	}
	return false
}

// selectNodes runs a parsed JSONPath against the document, reporting whether
// the evaluation completed.
//
// Parsing cleanly is not enough: a function extension applied to a member the
// document does not carry reaches the extension with nothing to read, and the
// evaluator dereferences it. This runs against every discovered resource, so
// letting that escape would take discovery down over one badly written filter
// expression. A failed evaluation is reported as a miss, the same answer an
// unparseable expression already gets. Failing towards "matches nothing" is the
// safe direction, because a match evicts the resource's inventory row; it is
// also why the failure is logged rather than swallowed, since a filter that
// quietly stopped working would leave substrate exposed with no signal.
func selectNodes(path *jsonpath.Path, expression string, data any) (nodes []any, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("Discovery filter expression could not be evaluated",
				"expression", expression,
				"panic", r,
			)
			nodes, ok = nil, false
		}
	}()

	return path.Select(data), true
}

// matchValue compares a JSONPath result against an expected string value.
// Handles various result types including arrays and nested structures.
func matchValue(val any, expected string) bool {
	switch v := val.(type) {
	case string:
		return v == expected
	case []any:
		// JSONPath filter expressions can return arrays
		for _, item := range v {
			if matchValue(item, expected) {
				return true
			}
		}
		return false
	case map[string]any:
		// Check if it's a tag-like structure with Value field
		if value, ok := v["Value"]; ok {
			return matchValue(value, expected)
		}
		return false
	default:
		// Convert other types to string for comparison
		return fmt.Sprintf("%v", v) == expected
	}
}

func currentOperation(state gen.Atom) resource.Operation {
	switch state {
	case StateSynchronizing:
		return resource.OperationRead
	case StateCreating:
		return resource.OperationCreate
	case StateUpdating:
		return resource.OperationUpdate
	case StateDeleting:
		return resource.OperationDelete
	default:
		panic(fmt.Sprintf("currentOperation: unknown state %s", state))
	}
}
