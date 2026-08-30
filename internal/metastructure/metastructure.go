// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"ergo.services/application/observer"
	"ergo.services/ergo"
	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/registrar"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/auth"
	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/datastore/migration"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/discovery"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/patch"
	"github.com/platform-engineering-labs/formae/internal/metastructure/policy_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/querier"
	"github.com/platform-engineering-labs/formae/internal/metastructure/reaping"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_reaper"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/transformations"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

const (
	// actorCallTimeout is the maximum time we wait for the MetastructureBridge actor to respond
	actorCallTimeout = 30 * time.Second
)

type MetastructureAPI interface {
	ApplyForma(forma *pkgmodel.Forma, config *config.FormaCommandConfig, clientID string, subject string, subjectName string) (*apimodel.SubmitCommandResponse, error)
	DestroyForma(forma *pkgmodel.Forma, config *config.FormaCommandConfig, clientID string, subject string, subjectName string) (*apimodel.SubmitCommandResponse, error)
	DestroyByQuery(query string, config *config.FormaCommandConfig, clientID string, subject string, subjectName string) (*apimodel.SubmitCommandResponse, error)
	CancelCommand(commandID string, force bool, clientID string) (*changeset.CancelResponse, error)
	CancelCommandsByQuery(query string, force bool, caller querier.Caller) (*apimodel.CancelCommandResponse, error)
	ListFormaCommandStatus(query string, caller querier.Caller, n int, scope apimodel.CommandScope) (*apimodel.ListCommandStatusResponse, error)
	ExtractResources(query string) (*pkgmodel.Forma, error)
	ListResourceSummaries(query string) ([]pkgmodel.ResourceSummary, error)
	ExtractResourceByKsuid(ksuid string) (*pkgmodel.Resource, error)
	ExtractTargets(query string) ([]*pkgmodel.Target, error)
	ExtractStacks() ([]*pkgmodel.Stack, error)
	ExtractPolicies() ([]apimodel.PolicyInventoryItem, error)
	ForceSync() error
	ForceDiscovery() error
	ForceAutoReconcile(stackLabel string, subject string, subjectName string) (*apimodel.ForceReconcileResponse, error)
	ForceCheckTTL() (*apimodel.ForceCheckTTLResponse, error)
	ForceReap() error
	ListDrift(stack string) (*apimodel.ModifiedStack, error)
	Stats() (*apimodel.Stats, error)
	RegisteredPlugins() ([]messages.RegisteredPluginInfo, error)
}

type Metastructure struct {
	nodeName  string
	options   gen.NodeOptions
	Node      gen.Node
	Datastore datastore.Datastore
	Cfg       *pkgmodel.Config
	AgentID   string

	// TestResourcePlugin is a test-only field for injecting a resource plugin (e.g. FakeAWS)
	// directly into the actor system. Must be nil in production.
	TestResourcePlugin plugin.FullResourcePlugin

	// AuthPluginHandle is the pre-created handle for the auth plugin process.
	// Set by the agent before Start(). Passed to both the supervisor (which spawns
	// the process) and the API server (which uses it for request validation).
	AuthPluginHandle *auth.AuthPluginHandle

	// commandMu serializes Apply/Destroy/ForceAutoReconcile to prevent TOCTOU
	// races between the conflict check and command storage. This will be
	// removed once the Metastructure itself becomes an actor.
	commandMu sync.Mutex
}

func NewMetastructure(ctx context.Context, cfg *pkgmodel.Config, externalResourcePlugins []plugin.ResourcePluginInfo, oidcCredentialPlugins []plugin.OidcCredentialPluginInfo, agentID string) (*Metastructure, error) {
	datastoreType := cfg.Agent.Datastore.DatastoreType
	if datastoreType == "" {
		datastoreType = "sqlite"
	}

	ds, err := datastore.DefaultRegistry.Create(datastoreType, ctx, &cfg.Agent.Datastore, agentID)
	if err != nil {
		return nil, err
	}

	return NewMetastructureWithDataStoreAndContext(ctx, cfg, externalResourcePlugins, oidcCredentialPlugins, ds, agentID)
}

func NewMetastructureWithDataStoreAndContext(ctx context.Context, cfg *pkgmodel.Config, externalResourcePlugins []plugin.ResourcePluginInfo, oidcCredentialPlugins []plugin.OidcCredentialPluginInfo, datastore datastore.Datastore, agentID string) (*Metastructure, error) {
	metastructure := &Metastructure{}

	metastructure.Datastore = datastore
	metastructure.Cfg = cfg

	// Registers pkg/credential's types too, so the agent, every resource
	// plugin, and every oidc-credential broker agree on the wire format.
	err := plugin.RegisterSharedEDFTypes()
	if err != nil {
		return nil, err
	}

	metastructure.nodeName = fmt.Sprintf("%s@%s", cfg.Agent.Server.Nodename, cfg.Agent.Server.Hostname)
	apps := []gen.ApplicationBehavior{
		CreateApplication(),
	}

	if cfg.Agent.Server.ObserverPort != 0 {
		apps = append(apps, observer.CreateApp(observer.Options{
			Port: uint16(cfg.Agent.Server.ObserverPort),
		}))
	}

	metastructure.AgentID = agentID

	metastructure.options.Applications = apps

	metastructure.options.Env = map[gen.Env]any{
		gen.Env("ExternalResourcePlugins"):     externalResourcePlugins,
		gen.Env("OidcCredentialPlugins"):       oidcCredentialPlugins,
		gen.Env("OidcCredentialPluginConfigs"): cfg.Agent.OidcCredentialPlugins,
		gen.Env("Datastore"):                   metastructure.Datastore,
		gen.Env("Context"):                     ctx,
		gen.Env("disable_metrics"):             true,
		gen.Env("ServerConfig"):                cfg.Agent.Server,
		gen.Env("DatastoreConfig"):             cfg.Agent.Datastore,
		gen.Env("RetryConfig"):                 cfg.Agent.Retry,
		gen.Env("SynchronizationConfig"):       cfg.Agent.Synchronization,
		gen.Env("DiscoveryConfig"):             cfg.Agent.Discovery,
		gen.Env("LoggingConfig"):               cfg.Agent.Logging,
		gen.Env("OTelConfig"):                  cfg.Agent.OTel,
		gen.Env("StackExpirerConfig"):          cfg.Agent.StackExpirer,
		gen.Env("AgentID"):                     agentID,
		gen.Env("ResourcePluginConfigs"):       cfg.Agent.ResourcePlugins,
	}

	// Enable Ergo networking for distributed plugin architecture
	metastructure.options.Network.Mode = gen.NetworkModeEnabled

	// Disable environment sharing for RemoteSpawn because the agent's environment contains
	// non-serializable types (Datastore, Context). We inject the relevant (serializable)
	// parts of the environment during remote spawn in the PluginCoordinator actor.
	metastructure.options.Security.ExposeEnvRemoteSpawn = false

	//FIXME(discount-elf): enable real TLS if we want it
	//cert, _ := lib.GenerateSelfSignedCert("formae node")
	//metastructure.options.CertManager = gen.CreateCertManager(cert)

	// Use the secret from config which now defaults to a random value via PKL
	metastructure.options.Network.Cookie = cfg.Agent.Server.Secret

	// Configure Ergo listen address with custom port (enables parallel test execution)
	// Each agent gets its own registrar to avoid sharing the global one on port 4499.
	// When multiple agents share a registrar, the first agent to shut down kills the
	// registrar server, causing all other agents to lose their connection.
	if cfg.Agent.Server.ErgoPort != 0 {
		registrarPort := cfg.Agent.Server.RegistrarPort
		if registrarPort == 0 {
			registrarPort = 4499
		}
		// Set registrar at node level for both incoming registration and outgoing resolution
		metastructure.options.Network.Registrar = registrar.Create(registrar.Options{Port: uint16(registrarPort)})
		metastructure.options.Network.Acceptors = []gen.AcceptorOptions{
			{
				Host: cfg.Agent.Server.Hostname,
				Port: uint16(cfg.Agent.Server.ErgoPort),
			},
		}
	}

	metastructure.options.Log.DefaultLogger.Disable = true
	metastructure.options.Log.Level = gen.LogLevelDebug
	logger, err := logging.NewErgoLogger()
	if err != nil {
		slog.Error("Failed to create logger", "error", err)
		return nil, err
	}

	metastructure.options.Log.Loggers = append(metastructure.options.Log.Loggers, gen.Logger{Name: "ergo", Logger: logger})

	return metastructure, nil
}

func (m *Metastructure) Start() error {
	slog.Info("Starting actor node", "node", m.nodeName)

	// Test-only: inject test resource plugin into actor environment.
	// This must happen in Start() (not the constructor) because TestResourcePlugin
	// is set after construction but before Start() is called.
	if m.TestResourcePlugin != nil {
		m.options.Env[gen.Env("TestResourcePlugin")] = m.TestResourcePlugin
	}

	// Inject auth plugin handle for the supervisor to spawn the auth process.
	// Set after construction but before Start(), same pattern as TestResourcePlugin.
	if m.AuthPluginHandle != nil {
		m.options.Env[gen.Env("AuthPluginHandle")] = m.AuthPluginHandle
	}

	// One-time, idempotent sweep that hashes any plaintext opaque secrets left
	// behind by writes made before opaque-value hashing existed. It
	// runs against the datastore alone — no actors, no plugins — so it happens
	// here, before the node starts, rather than after: keying opacity on the
	// hard-coded known-opaque table (not a running plugin coordinator) means we
	// don't have to sequence it against actor/plugin startup. Safe on every
	// boot: a no-op once everything eligible is hashed, and it never touches
	// DesiredState of a command that isn't final yet, so it can't interfere
	// with ReRunIncompleteCommands below.
	if err := migration.BackfillHashedSecrets(m.Datastore); err != nil {
		slog.Error("Failed to backfill hashed secrets", "error", err)
		return err
	}

	// One-time, idempotent sweep that populates the refs column on pre-migration
	// resource rows so they are queryable by the indexed cascade lookup. Runs
	// against the datastore alone — no actors, no plugins — before the node
	// starts; a no-op on dialects without the refs column (sqlite, mssql).
	if err := migration.BackfillResourceRefs(m.Datastore); err != nil {
		slog.Error("Failed to backfill resource refs", "error", err)
		return err
	}

	node, err := ergo.StartNode(gen.Atom(m.nodeName), m.options)
	if err != nil {
		slog.Error("Failed to start node", "error", err)
		return err
	}
	m.Node = node

	return m.ReRunIncompleteCommands()
}

func (m *Metastructure) Stop(force bool) {
	slog.Info("Stopping node", "node", m.nodeName)
	m.Datastore.Close()
	if m.Node != nil {
		if force {
			m.Node.StopForce()
		} else {
			m.Node.Stop()
		}
	}

	slog.Info("Node stopped", "node", m.nodeName)
}

// callActor provides a synchronous call interface from the non-actor world (metastructure)
// to the actor world by using the MetastructureBridge actor.
func (m *Metastructure) callActor(targetPID gen.ProcessID, message any) (any, error) {
	successChan := make(chan any, 1)
	errorChan := make(chan error, 1)

	request := CallActorRequest{
		TargetPID:   targetPID,
		Message:     message,
		SuccessChan: successChan,
		ErrorChan:   errorChan,
	}

	err := m.Node.Send(
		gen.ProcessID{Name: actornames.MetastructureBridge, Node: m.Node.Name()},
		request,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send request to MetastructureBridge: %w", err)
	}

	// Wait for either success or error response
	select {
	case response := <-successChan:
		return response, nil
	case err := <-errorChan:
		return nil, err
	case <-time.After(actorCallTimeout):
		return nil, fmt.Errorf("timeout waiting for actor response")
	}
}

func (m *Metastructure) ApplyForma(forma *pkgmodel.Forma, config *config.FormaCommandConfig, clientID string, subject string, subjectName string) (*apimodel.SubmitCommandResponse, error) {
	m.commandMu.Lock()
	defer m.commandMu.Unlock()

	// Check for conflicting commands BEFORE generating resource updates. This ordering is
	// critical: the resource queries in FormaCommandFromForma must run after any concurrent
	// commands have finished, otherwise we may see stale resource state (e.g. an empty stack)
	// while the conflict check passes (the command already completed). By checking conflicts
	// first, we guarantee that if no incomplete commands exist, all their resources are already
	// persisted and visible to subsequent queries.
	if !config.Simulate {
		if err := m.checkForConflictingCommands(stackLabelsFromForma(forma)); err != nil {
			return nil, err
		}

		// Reject an apply that touches a reaped target without re-declaring it.
		// A reaped target is a tombstone for a target that stayed unreachable past
		// its reap threshold; a resource-only or stale apply that references it must
		// not silently resurrect it. Re-declaring the target (it appears in the
		// forma's targets block) is the sanctioned recovery path: it produces a
		// target update that mints a fresh incarnation, so it is allowed through.
		if err := m.checkForReapedTargets(forma); err != nil {
			return nil, err
		}
	}

	// A forced reconcile asserts the write witness (the state formae's own
	// last write observed, which sync never refreshes) into the desired
	// state before planning, so witnessed out-of-band movement is reverted
	// like any overwritten drift. Assertion covers the forma's declared
	// resources; a resource updated indirectly (a cascade into an undeclared
	// stack) has no declaration to assert onto and keeps the pre-existing
	// absorb behavior under force.
	if config.Mode == pkgmodel.FormaApplyModeReconcile && config.Force {
		assertMods := make(map[string][]datastore.ResourceModification)
		assertWitnesses := make(map[string]json.RawMessage)
		for _, stackLabel := range stackLabelsFromForma(forma) {
			if err := m.loadModificationsAndWitnesses(stackLabel, assertMods, assertWitnesses); err != nil {
				return nil, err
			}
		}
		forma = assertWitnessesIntoForma(forma, assertMods, assertWitnesses)
	}

	fa, err := FormaCommandFromForma(forma, config, pkgmodel.CommandApply, m.Datastore, clientID, subject, subjectName, resource_update.FormaCommandSourceUser, m.Cfg.Agent.Synchronization.Interval)
	if err != nil {
		if requiredFieldsErr, ok := err.(apimodel.RequiredFieldMissingOnCreateError); ok {
			return nil, requiredFieldsErr
		}
		if targetExistsErr, ok := err.(apimodel.TargetAlreadyExistsError); ok {
			return nil, targetExistsErr
		}
		if nonPortableErr, ok := err.(apimodel.NonPortableResourcesError); ok {
			return nil, nonPortableErr
		}
		slog.Error("Failed to create apply from forma", "error", err)
		return nil, err
	}

	// Drift rejection runs before the no-changes return: a drift-only soft
	// reconcile must confront, not report "no changes". Out-of-band movement
	// on provider-default content formae's own write witnessed is drift like
	// any other; movement on content formae never wrote (late-populated
	// defaults, runtime registrations) stays the infrastructure's business
	// and never rejects. The snapshot is loaded AFTER planning so drift a
	// sync persists mid-submission is still confronted rather than silently
	// overwritten; a sync landing after this check keeps the pre-existing
	// race window.
	if config.Mode == pkgmodel.FormaApplyModeReconcile && !config.Force {
		modificationsByStack := make(map[string][]datastore.ResourceModification)
		witnessByKsuid := make(map[string]json.RawMessage)
		seenStacks := map[string]bool{}
		for _, stackLabel := range append(stackLabelsFromForma(forma), fa.GetStackLabels()...) {
			if seenStacks[stackLabel] {
				continue
			}
			seenStacks[stackLabel] = true
			if err := m.loadModificationsAndWitnesses(stackLabel, modificationsByStack, witnessByKsuid); err != nil {
				return nil, err
			}
		}
		var modifiedStacks = make(map[string]apimodel.ModifiedStack)
		for stackLabel, modifications := range modificationsByStack {
			unabsorbed := filterUnabsorbedModifications(modifications, forma, fa)
			unabsorbed = append(unabsorbed, witnessedMovedModifications(modifications, witnessByKsuid, forma, fa)...)
			if len(unabsorbed) > 0 {
				modifiedResources := make([]apimodel.ResourceModification, 0, len(unabsorbed))
				for _, modification := range unabsorbed {
					modifiedResources = append(modifiedResources, toAPIResourceModification(modification))
				}
				modifiedStacks[stackLabel] = apimodel.ModifiedStack{
					ModifiedResources: modifiedResources,
				}
			}
		}
		if len(modifiedStacks) > 0 {
			return nil, apimodel.FormaReconcileRejectedError{ModifiedStacks: modifiedStacks}
		}
	}

	if !fa.HasChanges() {
		return &apimodel.SubmitCommandResponse{
			CommandID:   fa.ID,
			Description: apimodel.Description(fa.Description),
			Simulation: apimodel.Simulation{
				ChangesRequired: false,
				Command:         apimodel.Command{},
			},
		}, nil
	}

	// Create changeset early to catch validation errors before simulate
	var cs changeset.Changeset
	if len(fa.ResourceUpdates) > 0 || len(fa.TargetUpdates) > 0 {
		synth, synthErr := target_update.SynthesizeResolveTargetUpdates(
			resource_update.ReferencedTargetLabels(fa.ResourceUpdates),
			resource_update.SourceTargetByKsuid(fa.ResourceUpdates),
			fa.TargetUpdates, m.Datastore)
		if synthErr != nil {
			return nil, synthErr
		}
		cs, err = changeset.NewChangeset(fa.ResourceUpdates, append(fa.TargetUpdates, synth...), fa.DrawGeneratorUpdates, fa.ID, fa.Command, config.Mode)
		if err != nil {
			return nil, err
		}
	}

	if config.Mode == pkgmodel.FormaApplyModePatch {
		err = m.checkIfPatchCanBeApplied(fa)
		if err != nil {
			return nil, err
		}
	}

	// Validate that no empty stacks are being created (applies to both modes)
	err = checkForEmptyStackCreation(fa)
	if err != nil {
		return nil, err
	}

	if config.Simulate {
		var warnings []string
		allByStack, loadErr := m.Datastore.LoadAllResourcesByStack()
		if loadErr != nil {
			slog.Warn("Failed to load resources for simulate warning", "error", loadErr)
		}
		for _, tu := range fa.TargetUpdates {
			if tu.Operation != target_update.TargetOperationReplace {
				continue
			}
			if allByStack == nil {
				continue
			}
			unmanagedOnTarget := 0
			if unmanagedResources, ok := allByStack[constants.UnmanagedStack]; ok {
				for _, r := range unmanagedResources {
					if r.Target == tu.Target.Label {
						unmanagedOnTarget++
					}
				}
			}
			if unmanagedOnTarget > 0 {
				warnings = append(warnings, fmt.Sprintf(
					"Target %q is being replaced. %d unmanaged resource(s) on this target will lose visibility and must be re-discovered.",
					tu.Target.Label, unmanagedOnTarget))
			}
		}

		return &apimodel.SubmitCommandResponse{
			CommandID:   fa.ID,
			Description: apimodel.Description(fa.Description),
			Simulation: apimodel.Simulation{
				ChangesRequired: fa.HasChanges(),
				Command:         translateToAPICommand(fa),
				Warnings:        warnings,
			},
		}, nil
	}

	m.Node.Log().Debug("Storing forma command commandID=%s", fa.ID)
	_, err = m.callActor(
		gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
		forma_persister.StoreNewFormaCommand{Command: *fa},
	)
	if err != nil {
		slog.Error("Failed to store forma command", "error", err)
		return nil, fmt.Errorf("failed to store forma command: %w", err)
	}

	if len(fa.StackUpdates) > 0 {
		_, err = m.callActor(
			gen.ProcessID{Name: actornames.ResourcePersister, Node: m.Node.Name()},
			stack_update.PersistStackUpdates{
				StackUpdates: fa.StackUpdates,
				CommandID:    fa.ID,
			},
		)
		if err != nil {
			slog.Error("Failed to persist stack updates", "error", err)
			return nil, fmt.Errorf("failed to persist stack updates: %w", err)
		}
		m.Node.Log().Debug("Successfully persisted stack updates count=%d", len(fa.StackUpdates))

		_, err = m.callActor(
			gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
			messages.UpdateStackStates{
				CommandID:    fa.ID,
				StackUpdates: fa.StackUpdates,
			},
		)
		if err != nil {
			slog.Error("Failed to update forma command with stack states", "error", err)
			return nil, fmt.Errorf("failed to update forma command with stack states: %w", err)
		}
	}

	if len(fa.PolicyUpdates) > 0 {
		// Build StackIDMap from persisted stack updates
		stackIDMap := make(map[string]string)
		for _, su := range fa.StackUpdates {
			if su.Stack.ID != "" {
				stackIDMap[su.Stack.Label] = su.Stack.ID
			}
		}

		// For inline policies whose stacks aren't in the map (existing stacks with no changes),
		// look up the stack ID from the database
		for _, pu := range fa.PolicyUpdates {
			if pu.StackLabel != "" {
				if _, ok := stackIDMap[pu.StackLabel]; !ok {
					stack, err := m.Datastore.GetStackByLabel(pu.StackLabel)
					if err != nil {
						return nil, fmt.Errorf("failed to look up stack %q for policy update: %w", pu.StackLabel, err)
					}
					if stack != nil {
						stackIDMap[pu.StackLabel] = stack.ID
					} else {
						// STOPGAP: The stack was deleted by a concurrent command between conflict check
						// and policy persist. This race is possible because stack/target/policy updates
						// are persisted outside the changeset execution DAG. The correct fix is to
						// incorporate these updates into the changeset so they are executed atomically
						// with resource updates. For now, fail the stored command to prevent it from
						// being orphaned in NotStarted state.
						slog.Warn("Stack deleted during apply setup, failing stored command",
							"commandID", fa.ID, "stackLabel", pu.StackLabel)
						refs := make([]forma_persister.ResourceUpdateRef, len(fa.ResourceUpdates))
						for i, ru := range fa.ResourceUpdates {
							refs[i] = forma_persister.ResourceUpdateRef{
								URI:       ru.DesiredState.URI(),
								Operation: ru.Operation,
							}
						}
						_, markErr := m.callActor(
							gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
							forma_persister.MarkResourcesAsFailed{
								CommandID:          fa.ID,
								Resources:          refs,
								ResourceModifiedTs: time.Now(),
							},
						)
						if markErr != nil {
							slog.Error("Failed to mark resources as failed after stack deletion",
								"commandID", fa.ID, "stackLabel", pu.StackLabel, "error", markErr)
						}
						return nil, apimodel.StackDeletedDuringApplyError{StackLabel: pu.StackLabel}
					}
				}
			}
		}

		_, err = m.callActor(
			gen.ProcessID{Name: actornames.ResourcePersister, Node: m.Node.Name()},
			policy_update.PersistPolicyUpdates{
				PolicyUpdates: fa.PolicyUpdates,
				CommandID:     fa.ID,
				StackIDMap:    stackIDMap,
			},
		)
		if err != nil {
			slog.Error("Failed to persist policy updates", "error", err)
			return nil, fmt.Errorf("failed to persist policy updates: %w", err)
		}
		m.Node.Log().Debug("Successfully persisted policy updates count=%d", len(fa.PolicyUpdates))

		_, err = m.callActor(
			gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
			messages.UpdatePolicyStates{
				CommandID:     fa.ID,
				PolicyUpdates: fa.PolicyUpdates,
			},
		)
		if err != nil {
			slog.Error("Failed to update forma command with policy states", "error", err)
			return nil, fmt.Errorf("failed to update forma command with policy states: %w", err)
		}
	}

	if len(fa.GeneratorUpdates) > 0 {
		// A generator has no standalone form, so every update is stack-scoped —
		// unlike the policy StackIDMap above there is no "empty = standalone"
		// case to skip. Build it the same way: prefer a stack this same command
		// just created or updated, else resolve the label from the datastore.
		stackIDMap := make(map[string]string)
		for _, su := range fa.StackUpdates {
			if su.Stack.ID != "" {
				stackIDMap[su.Stack.Label] = su.Stack.ID
			}
		}

		for _, gu := range fa.GeneratorUpdates {
			if _, ok := stackIDMap[gu.StackLabel]; ok {
				continue
			}
			stack, err := m.Datastore.GetStackByLabel(gu.StackLabel)
			if err != nil {
				return nil, fmt.Errorf("failed to look up stack %q for generator update: %w", gu.StackLabel, err)
			}
			if stack != nil {
				stackIDMap[gu.StackLabel] = stack.ID
				continue
			}
			// STOPGAP: the stack was deleted by a concurrent command between
			// conflict check and generator persist. See the identical race
			// noted on the policy StackIDMap above.
			slog.Warn("Stack deleted during apply setup, failing stored command",
				"commandID", fa.ID, "stackLabel", gu.StackLabel)
			refs := make([]forma_persister.ResourceUpdateRef, len(fa.ResourceUpdates))
			for i, ru := range fa.ResourceUpdates {
				refs[i] = forma_persister.ResourceUpdateRef{
					URI:       ru.DesiredState.URI(),
					Operation: ru.Operation,
				}
			}
			_, markErr := m.callActor(
				gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
				forma_persister.MarkResourcesAsFailed{
					CommandID:          fa.ID,
					Resources:          refs,
					ResourceModifiedTs: time.Now(),
				},
			)
			if markErr != nil {
				slog.Error("Failed to mark resources as failed after stack deletion",
					"commandID", fa.ID, "stackLabel", gu.StackLabel, "error", markErr)
			}
			return nil, apimodel.StackDeletedDuringApplyError{StackLabel: gu.StackLabel}
		}

		_, err = m.callActor(
			gen.ProcessID{Name: actornames.ResourcePersister, Node: m.Node.Name()},
			generator_update.PersistGeneratorUpdates{
				GeneratorUpdates: fa.GeneratorUpdates,
				CommandID:        fa.ID,
				StackIDMap:       stackIDMap,
			},
		)
		if err != nil {
			slog.Error("Failed to persist generator updates", "error", err)
			return nil, fmt.Errorf("failed to persist generator updates: %w", err)
		}
		m.Node.Log().Debug("Successfully persisted generator updates count=%d", len(fa.GeneratorUpdates))

		// Unlike PolicyUpdates and StackUpdates, GeneratorUpdates is not
		// round-tripped through the forma_commands table: that table's
		// resource/target/stack/policy update snapshots live in dedicated
		// columns (see StoreFormaCommand), and adding a generator_updates
		// column is command-status observability, not part of connecting
		// Forma.Generators to the datastore. The generator writes themselves
		// (CreateGenerator/UpdateGenerator/DeleteGenerator, just above) are
		// fully durable regardless.
	}

	if len(fa.ResourceUpdates) > 0 || len(fa.TargetUpdates) > 0 {
		m.Node.Log().Debug("Starting ChangesetExecutor of changeset from forma command commandID=%s", fa.ID)
		_, err = m.callActor(
			gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: m.Node.Name()},
			changeset.EnsureChangesetExecutor{CommandID: fa.ID},
		)
		if err != nil {
			slog.Error("Failed to ensure ChangesetExecutor for forma command", "command", fa.Command, "forma", fa, "error", err)
			return nil, fmt.Errorf("failed to ensure ChangesetExecutor: %w", err)
		}

		m.Node.Log().Debug("Sending Start message to ChangesetExecutor commandID=%s", fa.ID)
		err = m.Node.Send(
			gen.ProcessID{Name: actornames.ChangesetExecutor(fa.ID), Node: m.Node.Name()},
			changeset.Start{Changeset: cs},
		)
		if err != nil {
			slog.Error("Failed to start ChangesetExecutor for forma command", "command", fa.Command, "forma", fa, "error", err)
			return nil, fmt.Errorf("failed to start ChangesetExecutor: %w", err)
		}
	}

	return &apimodel.SubmitCommandResponse{
		CommandID:   fa.ID,
		Description: apimodel.Description(fa.Description),
		Simulation: apimodel.Simulation{
			ChangesRequired: fa.HasChanges(),
			Command:         translateToAPICommand(fa),
		},
	}, nil
}

// loadModificationsAndWitnesses loads one stack's drift window into the
// submission snapshot, plus the write witness for each modified resource
// (GetPropertiesAtLastWrite: the state formae's own last write observed).
// Window load failures fail the submission; a witness fetch failure degrades
// to no witness for that resource, which classifies its movement as
// tolerated, the pre-existing behavior.
func (m *Metastructure) loadModificationsAndWitnesses(stackLabel string, modificationsByStack map[string][]datastore.ResourceModification, witnessByKsuid map[string]json.RawMessage) error {
	modifications, err := m.Datastore.GetResourceModificationsSinceLastReconcile(stackLabel)
	if err != nil {
		slog.Error("Failed to load modifications since last reconcile", "stack", stackLabel, "error", err)
		return fmt.Errorf("failed to load modifications for stack %s: %w", stackLabel, err)
	}
	if len(modifications) == 0 {
		return nil
	}
	modificationsByStack[stackLabel] = modifications
	for _, mod := range modifications {
		if mod.Operation != "update" || mod.Ksuid == "" {
			continue
		}
		if _, done := witnessByKsuid[mod.Ksuid]; done {
			continue
		}
		witness, werr := m.Datastore.GetPropertiesAtLastWrite(mod.Ksuid)
		if werr != nil {
			slog.Warn("Failed to load write witness for drift classification", "ksuid", mod.Ksuid, "error", werr)
			continue
		}
		witnessByKsuid[mod.Ksuid] = witness
	}
	return nil
}

func translateToAPICommand(fa *forma_command.FormaCommand) apimodel.Command {
	apiCommand := apimodel.Command{
		CommandID:   fa.ID,
		Command:     string(fa.Command),
		Mode:        string(fa.Config.Mode),
		Source:      string(fa.Source),
		Subject:     fa.Subject,
		SubjectName: fa.SubjectName,
		State:       string(fa.State),
		StartTs:     fa.StartTs,
		EndTs:       fa.ModifiedTs,
	}
	for _, ru := range fa.ResourceUpdates {
		var dur time.Duration = 0
		if !ru.StartTs.IsZero() {
			dur = ru.ModifiedTs.Sub(ru.StartTs)
		}

		var oldLabel string
		if ru.PriorState.Label != "" && ru.PriorState.Label != ru.DesiredState.Label {
			oldLabel = ru.PriorState.Label
		}

		// Property and patch documents are redacted at this single projection
		// point: everything downstream (simulate responses, command status,
		// conflict listings, the CLI, API consumers) is presentation data, and
		// neither opaque plaintext (pre-persist documents) nor at-rest digests
		// belong in it.
		opaque := transformations.OpaqueFields(ru.DesiredState.Schema, ru.DesiredState.Type)
		for f := range transformations.OpaqueFields(ru.PriorState.Schema, ru.PriorState.Type) {
			opaque[f] = true
		}

		apiCommand.ResourceUpdates = append(apiCommand.ResourceUpdates, apimodel.ResourceUpdate{
			ResourceID:      ru.DesiredState.Ksuid,
			ResourceType:    ru.DesiredState.Type,
			ResourceLabel:   ru.DesiredState.Label,
			OldLabel:        oldLabel,
			StackName:       ru.StackLabel,
			OldStackName:    ru.PriorState.Stack,
			Properties:      transformations.RedactPropertiesForDisplay(ru.DesiredState.Properties, opaque),
			OldProperties:   transformations.RedactPropertiesForDisplay(ru.PreviousProperties, opaque),
			PatchDocument:   transformations.RedactPatchDocumentForDisplay(ru.DesiredState.PatchDocument, opaque),
			CreateOnlyPatch: transformations.RedactPatchDocumentForDisplay(ru.CreateOnlyPatch, opaque),
			Operation:       string(ru.Operation),
			State:           string(ru.State),
			StartedAt:       ru.StartTs,
			Duration:        dur.Milliseconds(),
			CurrentAttempt:  ru.MostRecentProgressResult.Attempts,
			MaxAttempts:     ru.MostRecentProgressResult.MaxAttempts,
			ErrorMessage:    ru.MostRecentFailureMessage(),
			StatusMessage:   ru.MostRecentStatusMessage(),
			NativeID:        ru.DesiredState.NativeID,
			GroupID:         ru.GroupID,
			IsCascade:       ru.IsCascade,
			CascadeSource:   ru.CascadeSource,
		})
	}

	for _, tu := range fa.TargetUpdates {
		var dur time.Duration = 0
		if !tu.StartTs.IsZero() {
			dur = tu.ModifiedTs.Sub(tu.StartTs)
		}

		var existingConfig json.RawMessage
		if tu.ExistingTarget != nil {
			existingConfig = tu.ExistingTarget.Config
		}

		apiCommand.TargetUpdates = append(apiCommand.TargetUpdates, apimodel.TargetUpdate{
			TargetLabel:    tu.Target.Label,
			Operation:      string(tu.Operation),
			State:          string(tu.State),
			Duration:       dur.Milliseconds(),
			ErrorMessage:   tu.ErrorMessage,
			Discoverable:   tu.Target.Discoverable,
			ExistingConfig: existingConfig,
			DesiredConfig:  tu.Target.Config,
			StartTs:        tu.StartTs,
			ModifiedTs:     tu.ModifiedTs,
			IsCascade:      tu.IsCascade,
			CascadeSource:  tu.CascadeSource,
		})
	}

	for _, su := range fa.StackUpdates {
		var dur time.Duration = 0
		if !su.StartTs.IsZero() {
			dur = su.ModifiedTs.Sub(su.StartTs)
		}

		apiCommand.StackUpdates = append(apiCommand.StackUpdates, apimodel.StackUpdate{
			StackLabel:   su.Stack.Label,
			Operation:    string(su.Operation),
			State:        string(su.State),
			Duration:     dur.Milliseconds(),
			ErrorMessage: su.ErrorMessage,
			Description:  su.Stack.Description,
			StartTs:      su.StartTs,
			ModifiedTs:   su.ModifiedTs,
		})
	}

	for _, pu := range fa.PolicyUpdates {
		var dur time.Duration = 0
		if !pu.StartTs.IsZero() {
			dur = pu.ModifiedTs.Sub(pu.StartTs)
		}

		// For attach/detach operations, Policy may be nil - use PolicyRef for the label
		var policyLabel, policyType string
		if pu.Policy != nil {
			policyLabel = pu.Policy.GetLabel()
			policyType = pu.Policy.GetType()
		} else if pu.PolicyRef != "" {
			policyLabel = pu.PolicyRef
			policyType = "" // Type unknown until resolved
		}

		// Marshal policy configs for diff display
		var policyConfig, oldPolicyConfig json.RawMessage
		if pu.Policy != nil {
			policyConfig, _ = json.Marshal(pu.Policy)
		}
		if pu.ExistingPolicy != nil {
			oldPolicyConfig, _ = json.Marshal(pu.ExistingPolicy)
		}

		apiCommand.PolicyUpdates = append(apiCommand.PolicyUpdates, apimodel.PolicyUpdate{
			PolicyLabel:       policyLabel,
			PolicyType:        policyType,
			StackLabel:        pu.StackLabel,
			Operation:         string(pu.Operation),
			State:             string(pu.State),
			Duration:          dur.Milliseconds(),
			ErrorMessage:      pu.ErrorMessage,
			PolicyConfig:      policyConfig,
			OldPolicyConfig:   oldPolicyConfig,
			ReferencingStacks: pu.ReferencingStacks,
			StartTs:           pu.StartTs,
			ModifiedTs:        pu.ModifiedTs,
		})
	}

	for _, gu := range fa.GeneratorUpdates {
		var dur time.Duration = 0
		if !gu.StartTs.IsZero() {
			dur = gu.ModifiedTs.Sub(gu.StartTs)
		}

		// Generator may be nil defensively (mirroring the pu.Policy nil
		// check above); in practice the generator diff always sets it, on
		// every operation. On a Delete it is the existing (about-to-be-
		// removed) generator; on a Create/Update it is the desired one.
		var generatorLabel, generatorType string
		if gu.Generator != nil {
			generatorLabel = gu.Generator.GetLabel()
			generatorType = gu.Generator.GetType()
		}

		// Marshal generator configs for diff display. A concrete Generator's
		// own KSUID field is tagged json:"-", so this can never leak
		// generator identity, and nothing here ever touches
		// pkgmodel.GeneratorIdentity (the drawing spec) or a drawn value —
		// neither exists on Generator, and no generated value exists at
		// plan/simulate time to marshal in the first place.
		var generatorConfig, oldGeneratorConfig json.RawMessage
		if gu.Generator != nil {
			generatorConfig, _ = json.Marshal(gu.Generator)
		}
		if gu.ExistingGenerator != nil {
			oldGeneratorConfig, _ = json.Marshal(gu.ExistingGenerator)
		}

		apiCommand.GeneratorUpdates = append(apiCommand.GeneratorUpdates, apimodel.GeneratorUpdate{
			GeneratorLabel:     generatorLabel,
			GeneratorType:      generatorType,
			StackLabel:         gu.StackLabel,
			Operation:          string(gu.Operation),
			State:              string(gu.State),
			Duration:           dur.Milliseconds(),
			ErrorMessage:       gu.ErrorMessage,
			GeneratorConfig:    generatorConfig,
			OldGeneratorConfig: oldGeneratorConfig,
			StartTs:            gu.StartTs,
			ModifiedTs:         gu.ModifiedTs,
		})
	}

	return apiCommand
}

func (m *Metastructure) DestroyForma(forma *pkgmodel.Forma, config *config.FormaCommandConfig, clientID string, subject string, subjectName string) (*apimodel.SubmitCommandResponse, error) {
	m.commandMu.Lock()
	defer m.commandMu.Unlock()

	// Check for conflicting commands before generating resource updates (same reasoning as ApplyForma).
	if !config.Simulate {
		stackLabels := stackLabelsFromForma(forma)

		// For destroy commands, also check stacks affected by cascade deletes
		cascadeStacks, err := m.findCascadeStackLabels(forma)
		if err != nil {
			return nil, fmt.Errorf("failed to compute cascade stacks: %w", err)
		}
		stackLabels = append(stackLabels, cascadeStacks...)

		if err := m.checkForConflictingCommands(stackLabels); err != nil {
			return nil, err
		}
	}

	fa, err := FormaCommandFromForma(forma, config, pkgmodel.CommandDestroy, m.Datastore, clientID, subject, subjectName, resource_update.FormaCommandSourceUser, m.Cfg.Agent.Synchronization.Interval)
	if err != nil {
		slog.Error("Failed to create destroy from forma", "error", err)
		return nil, err
	}

	// Short-circuit if there are no resource updates
	if !fa.HasChanges() {
		return &apimodel.SubmitCommandResponse{
			CommandID:   fa.ID,
			Description: apimodel.Description(fa.Description),
			Simulation: apimodel.Simulation{
				ChangesRequired: false,
				Command:         apimodel.Command{},
			},
		}, nil
	}

	if config.Simulate {
		return &apimodel.SubmitCommandResponse{
			CommandID:   fa.ID,
			Description: apimodel.Description(fa.Description),
			Simulation: apimodel.Simulation{
				ChangesRequired: fa.HasChanges(),
				Command:         translateToAPICommand(fa),
			},
		}, nil
	}

	m.Node.Log().Debug("Storing forma command commandID=%s", fa.ID)
	_, err = m.callActor(
		gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
		forma_persister.StoreNewFormaCommand{Command: *fa},
	)
	if err != nil {
		slog.Error("Failed to store forma command", "error", err)
		return nil, fmt.Errorf("failed to store forma command: %w", err)
	}

	if len(fa.PolicyUpdates) > 0 {
		_, err = m.callActor(
			gen.ProcessID{Name: actornames.ResourcePersister, Node: m.Node.Name()},
			policy_update.PersistPolicyUpdates{
				PolicyUpdates: fa.PolicyUpdates,
				CommandID:     fa.ID,
				StackIDMap:    nil, // For destroy, policies are being deleted, no stack mapping needed
			},
		)
		if err != nil {
			slog.Error("Failed to persist policy updates", "error", err)
			return nil, fmt.Errorf("failed to persist policy updates: %w", err)
		}
		m.Node.Log().Debug("Successfully persisted policy updates count=%d", len(fa.PolicyUpdates))

		_, err = m.callActor(
			gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
			messages.UpdatePolicyStates{
				CommandID:     fa.ID,
				PolicyUpdates: fa.PolicyUpdates,
			},
		)
		if err != nil {
			slog.Error("Failed to update forma command with policy states", "error", err)
			return nil, fmt.Errorf("failed to update forma command with policy states: %w", err)
		}
	}

	if len(fa.ResourceUpdates) > 0 || len(fa.TargetUpdates) > 0 {
		synth, synthErr := target_update.SynthesizeResolveTargetUpdates(
			resource_update.ReferencedTargetLabels(fa.ResourceUpdates),
			resource_update.SourceTargetByKsuid(fa.ResourceUpdates),
			fa.TargetUpdates, m.Datastore)
		if synthErr != nil {
			return nil, synthErr
		}
		// No generator draws: a destroy writes no property.
		cs, err := changeset.NewChangeset(fa.ResourceUpdates, append(fa.TargetUpdates, synth...), nil, fa.ID, pkgmodel.CommandDestroy, config.Mode)
		if err != nil {
			return nil, err
		}

		m.Node.Log().Debug("Starting ChangesetExecutor of changeset from forma command commandID=%s", fa.ID)
		_, err = m.callActor(
			gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: m.Node.Name()},
			changeset.EnsureChangesetExecutor{CommandID: fa.ID},
		)
		if err != nil {
			slog.Error("Failed to ensure ChangesetExecutor for forma command", "command", fa.Command, "forma", fa, "error", err)
			return nil, fmt.Errorf("failed to ensure ChangesetExecutor: %w", err)
		}

		m.Node.Log().Debug("Sending Start message to ChangesetExecutor commandID=%s", fa.ID)
		err = m.Node.Send(
			gen.ProcessID{Name: actornames.ChangesetExecutor(fa.ID), Node: m.Node.Name()},
			changeset.Start{Changeset: cs},
		)
		if err != nil {
			slog.Error("Failed to start ChangesetExecutor for forma command", "command", fa.Command, "forma", fa, "error", err)
			return nil, fmt.Errorf("failed to start ChangesetExecutor: %w", err)
		}
	}

	return &apimodel.SubmitCommandResponse{
		CommandID:   fa.ID,
		Description: apimodel.Description(fa.Description),
		Simulation: apimodel.Simulation{
			ChangesRequired: fa.HasChanges(),
			Command:         translateToAPICommand(fa),
		},
	}, nil
}

func (m *Metastructure) DestroyByQuery(query string, config *config.FormaCommandConfig, clientID string, subject string, subjectName string) (*apimodel.SubmitCommandResponse, error) {
	q := querier.NewBlugeQuerier(m.Datastore)
	resources, err := q.QueryResourcesForDestroy(query)
	if err != nil {
		slog.Debug("Cannot get resources from query", "error", err)
		return nil, err
	}

	var managedResources []*pkgmodel.Resource
	for _, r := range resources {
		if r.Managed {
			managedResources = append(managedResources, r)
		}
	}

	forma := pkgmodel.FormaFromResources(managedResources)

	return m.DestroyForma(forma, config, clientID, subject, subjectName)
}

func (m *Metastructure) CancelCommand(commandID string, force bool, clientID string) (*changeset.CancelResponse, error) {
	slog.Info("Canceling command", "commandID", commandID, "force", force, "clientID", clientID)

	changesetExecutorPID := gen.ProcessID{
		Name: actornames.ChangesetExecutor(commandID),
		Node: m.Node.Name(),
	}

	result, err := m.callActor(changesetExecutorPID, changeset.Cancel{
		CommandID: commandID,
		Force:     force,
	})
	if err != nil {
		slog.Error("Failed to cancel command", "commandID", commandID, "error", err)
		return nil, fmt.Errorf("failed to cancel command: %w", err)
	}

	cancelResp, ok := result.(changeset.CancelResponse)
	if !ok {
		return nil, fmt.Errorf("unexpected response type from changeset executor: %T", result)
	}

	// A --force cancel that failed to persist carries its error in-band (the executor
	// stayed alive and terminated no actors). Surface it as a returned error.
	if cancelResp.ErrorMessage != "" {
		slog.Error("Force-cancel failed to persist", "commandID", commandID, "error", cancelResp.ErrorMessage)
		return nil, fmt.Errorf("failed to force-cancel command: %s", cancelResp.ErrorMessage)
	}

	return &cancelResp, nil
}

// commandsForCancelQuery resolves the candidate FormaCommands for a
// cancel-by-query request.
//
// Unlike ListFormaCommandStatus (a user-facing surface that hard-restricts
// to Source=user), this deliberately does NOT restrict by Source: an
// operator draining scheduler bookkeeping (sync, discovery) ahead of an
// agent restart must still be able to target those commands by an explicit
// query, e.g. `command:sync`.
//
// The one exclusion preserved here is the one QueryFormaCommands itself used
// to apply implicitly, before Source-based filtering replaced it for the
// status-listing path: an *unfiltered* query (no explicit `command:` term)
// must not surface sync/discovery bookkeeping by accident, since both use
// the "sync" command type. A caller who explicitly asks for `command:sync`
// still reaches them — only the implicit, no-command-filter case is
// protected.
func (m *Metastructure) commandsForCancelQuery(query string, caller querier.Caller) ([]*forma_command.FormaCommand, error) {
	if query == "" {
		command, err := m.Datastore.GetMostRecentFormaCommandByClientID(caller.ClientID)
		if err != nil {
			return nil, err
		}
		if command == nil {
			return nil, nil
		}
		return []*forma_command.FormaCommand{command}, nil
	}

	q := querier.NewBlugeQuerier(m.Datastore)
	statusQuery, err := q.BuildStatusQuery(query, caller, 100) // limit to 100 commands
	if err != nil {
		return nil, err
	}
	if statusQuery.Command == nil {
		statusQuery.Command = &datastore.QueryItem[string]{
			Item:       string(pkgmodel.CommandSync),
			Constraint: datastore.Excluded,
		}
	}

	return m.Datastore.QueryFormaCommands(statusQuery)
}

func (m *Metastructure) CancelCommandsByQuery(query string, force bool, caller querier.Caller) (*apimodel.CancelCommandResponse, error) {
	commandsToCancel, err := m.commandsForCancelQuery(query, caller)
	if err != nil {
		slog.Debug("Cannot get forma commands from query", "error", err)
		return nil, err
	}

	// Filter to only InProgress commands
	var canceledCommandIDs []string
	var forceCancelFailures []string
	allResourceStates := make(map[string]apimodel.CancelResourceState)
	for _, cmd := range commandsToCancel {
		if cmd.State == forma_command.CommandStateInProgress {
			cancelResp, err := m.CancelCommand(cmd.ID, force, caller.ClientID)
			if err != nil {
				slog.Warn("Failed to cancel command", "commandID", cmd.ID, "error", err)
				// A --force cancel that fails left the command running: the executor
				// terminated no actors and the command is still non-terminal. Record it
				// so the caller is told to retry rather than seeing a success with the
				// command silently dropped. A graceful (non-force) cancel of a command
				// that has since vanished is benign, so keep skipping those.
				if force {
					forceCancelFailures = append(forceCancelFailures, fmt.Sprintf("%s: %v", cmd.ID, err))
				}
				continue
			}
			canceledCommandIDs = append(canceledCommandIDs, cmd.ID)
			if cancelResp != nil {
				forceCanceled := make(map[string]bool, len(cancelResp.ForceCanceledInProgress))
				for _, uri := range cancelResp.ForceCanceledInProgress {
					forceCanceled[uri] = true
				}
				for uri, state := range cancelResp.ResourceStates {
					allResourceStates[uri] = apimodel.CancelResourceState{
						State:         state,
						ForceCanceled: forceCanceled[uri],
						CommandID:     cmd.ID,
					}
				}
			}
		}
	}

	if len(forceCancelFailures) > 0 {
		return nil, fmt.Errorf("force-cancel failed for %d command(s): %s",
			len(forceCancelFailures), strings.Join(forceCancelFailures, "; "))
	}

	return &apimodel.CancelCommandResponse{
		CommandIDs:           canceledCommandIDs,
		ResourceUpdateStates: allResourceStates,
		Forced:               force,
	}, nil
}

// ListFormaCommandStatus answers a command-status listing.
//
// An empty query is answered according to scope:
//   - CommandScopeClient (the default) — the calling client's single most
//     recent command, which is what a bare `formae command status` asks for.
//   - CommandScopeAgent — every client's commands, newest first, bounded by
//     n. This is what a bare `formae command list` asks for; it runs through
//     the querier's unconstrained query rather than the client-scoped route.
//
// A non-empty query ignores scope: the query itself expresses the narrowing
// (`client:me` for the caller's own commands).
//
// Every path is restricted to user-initiated commands. Source is applied
// here, not parsed from the query grammar, so a caller cannot ask for
// scheduler bookkeeping (sync, discovery, auto-reconcile, stack expiry) even
// when it shares a command type with user work.
func (m *Metastructure) ListFormaCommandStatus(query string, caller querier.Caller, n int, scope apimodel.CommandScope) (*apimodel.ListCommandStatusResponse, error) {
	if query == "" && scope != apimodel.CommandScopeAgent {
		fa, err := m.Datastore.GetMostRecentFormaCommandByClientID(caller.ClientID)
		if err != nil {
			slog.Debug("Cannot get forma command from client ID", "error", err)
			return nil, err
		}
		if fa == nil {
			return &apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{}}, nil
		}

		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{translateToAPICommand(fa)},
		}, nil
	}

	q := querier.NewBlugeQuerier(m.Datastore)
	statusQuery, err := q.BuildStatusQuery(query, caller, n)
	if err != nil {
		slog.Debug("Cannot get forma commands from query", "error", err)
		return nil, err
	}

	statusQuery.Source = &datastore.QueryItem[string]{
		Item:       string(forma_command.SourceUser),
		Constraint: datastore.Required,
	}

	formaCommands, err := m.Datastore.QueryFormaCommands(statusQuery)
	if err != nil {
		slog.Debug("Cannot get forma commands from query", "error", err)
		return nil, err
	}

	res := &apimodel.ListCommandStatusResponse{}
	for _, fa := range formaCommands {
		res.Commands = append(res.Commands, translateToAPICommand(fa))
	}

	return res, nil
}

func (m *Metastructure) ExtractPolicies() ([]apimodel.PolicyInventoryItem, error) {
	policies, err := m.Datastore.ListAllStandalonePolicies()
	if err != nil {
		return nil, err
	}

	var items []apimodel.PolicyInventoryItem
	for _, policy := range policies {
		configJSON, err := json.Marshal(policy)
		if err != nil {
			slog.Warn("Failed to marshal policy config", "label", policy.GetLabel(), "error", err)
			continue
		}

		stacks, err := m.Datastore.GetStacksReferencingPolicy(policy.GetLabel())
		if err != nil {
			slog.Warn("Failed to get stacks for policy", "label", policy.GetLabel(), "error", err)
		}

		items = append(items, apimodel.PolicyInventoryItem{
			Label:          policy.GetLabel(),
			Type:           policy.GetType(),
			Config:         configJSON,
			AttachedStacks: stacks,
		})
	}

	return items, nil
}

func (m *Metastructure) reverseTranslateKSUIDsToTriplets(resources []*pkgmodel.Resource) error {
	ksuidSet := make(map[string]struct{})
	for _, resource := range resources {
		if resource.Properties != nil {
			extractKSUIDs(string(resource.Properties), ksuidSet)
		}
		if resource.ReadOnlyProperties != nil {
			extractKSUIDs(string(resource.ReadOnlyProperties), ksuidSet)
		}
	}

	if len(ksuidSet) == 0 {
		return nil
	}

	ksuids := make([]string, 0, len(ksuidSet))
	for ksuid := range ksuidSet {
		ksuids = append(ksuids, ksuid)
	}

	ksuidToTriplet, err := m.Datastore.BatchGetTripletsByKSUIDs(ksuids)
	if err != nil {
		return fmt.Errorf("failed to batch lookup triplets: %w", err)
	}

	for i, resource := range resources {
		if resource.Properties != nil {
			translated := replaceKSUIDs(string(resource.Properties), ksuidToTriplet)
			resources[i].Properties = json.RawMessage(translated)
		}
		if resource.ReadOnlyProperties != nil {
			translated := replaceKSUIDs(string(resource.ReadOnlyProperties), ksuidToTriplet)
			resources[i].ReadOnlyProperties = json.RawMessage(translated)
		}
	}

	return nil
}

func (m *Metastructure) ListDrift(stack string) (*apimodel.ModifiedStack, error) {
	modifications, err := m.Datastore.GetResourceModificationsSinceLastReconcile(stack)
	if err != nil {
		slog.Error("Failed to get drift for stack", "stack", stack, "error", err)
		return nil, fmt.Errorf("failed to get drift for stack %s: %w", stack, err)
	}

	modifiedResources := make([]apimodel.ResourceModification, 0, len(modifications))
	for _, modification := range modifications {
		modifiedResources = append(modifiedResources, toAPIResourceModification(modification))
	}

	return &apimodel.ModifiedStack{ModifiedResources: modifiedResources}, nil
}

func (m *Metastructure) ForceSync() error {
	if err := m.Node.Send(gen.Atom("Synchronizer"), Synchronize{}); err != nil {
		slog.Error(fmt.Sprintf("Failed to send message to Synchronizer: %v", err))
		return err
	}

	return nil
}

func (m *Metastructure) ForceDiscovery() error {
	if err := m.Node.Send(gen.Atom("Discovery"), discovery.Discover{}); err != nil {
		slog.Error(fmt.Sprintf("Failed to send message to Discovery: %v", err))
		return err
	}

	return nil
}

// ForceReap triggers a single, immediate TargetReaper tick: it advances the
// unreachability-accrual clock for every currently-unreachable target,
// detects reap candidates, and (subject to the per-tick rate cap — see
// TargetReaper) actually reaps them. Exists so workflow tests can drive a
// deterministic tick without waiting for the reaper's interval to elapse.
func (m *Metastructure) ForceReap() error {
	if err := m.Node.Send(gen.Atom(actornames.TargetReaper), target_reaper.CheckUnreachableTargets{}); err != nil {
		slog.Error(fmt.Sprintf("Failed to send message to TargetReaper: %v", err))
		return err
	}

	return nil
}

func (m *Metastructure) ForceAutoReconcile(stackLabel string, subject string, subjectName string) (*apimodel.ForceReconcileResponse, error) {
	m.commandMu.Lock()
	defer m.commandMu.Unlock()

	// Check if stack has active commands
	hasActive, err := m.Datastore.StackHasActiveCommands(stackLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to check active commands: %w", err)
	}
	if hasActive {
		// Build a conflicting commands error so mapError maps it to 409
		incompleteCommands, listErr := m.Datastore.LoadIncompleteFormaCommands()
		if listErr != nil {
			return nil, fmt.Errorf("failed to load incomplete commands: %w", listErr)
		}
		conflicting := make([]apimodel.Command, 0)
		targetStacks := []string{stackLabel}
		for _, cmd := range incompleteCommands {
			if formaTouchesStacks(cmd, targetStacks) {
				conflicting = append(conflicting, translateToAPICommand(cmd))
			}
		}
		return nil, apimodel.FormaConflictingCommandsError{
			ConflictingCommands: conflicting,
		}
	}

	// Verify the stack has an auto-reconcile policy attached.
	// Force-reconcile is a destructive operation that reverts all resources to
	// their last-known desired state. Require explicit opt-in via policy.
	reconcileInfos, err := m.Datastore.GetStacksWithAutoReconcilePolicy()
	if err != nil {
		return nil, fmt.Errorf("failed to check auto-reconcile policies: %w", err)
	}
	hasPolicy := false
	for _, info := range reconcileInfos {
		if info.StackLabel == stackLabel {
			hasPolicy = true
			break
		}
	}
	if !hasPolicy {
		return nil, apimodel.ReconcilePolicyRequiredError{StackLabel: stackLabel}
	}

	// Prepare the reconcile command and changeset
	// A force-reconcile is user-initiated: stamping it SourceUser is what
	// keeps the CommandID returned below resolvable through the ordinary
	// `command status` / get-command-status path, which only shows
	// user-initiated commands.
	result, err := prepareReconcile(m.Datastore, stackLabel, "force-reconcile", subject, subjectName, forma_command.SourceUser)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return &apimodel.ForceReconcileResponse{Message: "no drift detected"}, nil
	}

	// Store the forma command via the FormaCommandPersister actor
	_, err = m.callActor(
		gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
		forma_persister.StoreNewFormaCommand{Command: *result.command},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to store reconcile command: %w", err)
	}

	// Ensure ChangesetExecutor exists
	_, err = m.callActor(
		gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: m.Node.Name()},
		changeset.EnsureChangesetExecutor{CommandID: result.command.ID},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure changeset executor: %w", err)
	}

	// Start the changeset execution (no notification needed for force reconcile)
	err = m.Node.Send(
		gen.ProcessID{Name: actornames.ChangesetExecutor(result.command.ID), Node: m.Node.Name()},
		changeset.Start{Changeset: result.changeset},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start changeset executor: %w", err)
	}

	return &apimodel.ForceReconcileResponse{CommandID: result.command.ID}, nil
}

func (m *Metastructure) ForceCheckTTL() (*apimodel.ForceCheckTTLResponse, error) {
	m.commandMu.Lock()
	defer m.commandMu.Unlock()

	expiredStacks, err := m.Datastore.GetExpiredStacks()
	if err != nil {
		return nil, fmt.Errorf("failed to query expired stacks: %w", err)
	}

	expiredLabels := make([]string, 0)
	commandIDs := make([]string, 0)

	for _, stackInfo := range expiredStacks {
		slog.Info("Force TTL check: expiring stack", "stack", stackInfo.StackLabel, "onDependents", stackInfo.OnDependents)

		result, err := prepareDestroyExpiredStack(m.Datastore, stackInfo, "force-check-ttl", "force-check-ttl-cleanup")
		if err != nil {
			slog.Error("Force TTL check: failed to prepare destroy for expired stack", "stack", stackInfo.StackLabel, "error", err)
			continue
		}
		if result == nil {
			// Stack was empty (and cleaned up), aborted due to dependents, or no updates needed
			continue
		}

		// Store the forma command via the FormaCommandPersister actor
		_, err = m.callActor(
			gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
			forma_persister.StoreNewFormaCommand{Command: *result.command},
		)
		if err != nil {
			slog.Error("Force TTL check: failed to store destroy command", "stack", stackInfo.StackLabel, "error", err)
			continue
		}

		// Ensure ChangesetExecutor exists
		_, err = m.callActor(
			gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: m.Node.Name()},
			changeset.EnsureChangesetExecutor{CommandID: result.command.ID},
		)
		if err != nil {
			slog.Error("Force TTL check: failed to ensure changeset executor", "stack", stackInfo.StackLabel, "error", err)
			continue
		}

		// Start the changeset execution
		err = m.Node.Send(
			gen.ProcessID{Name: actornames.ChangesetExecutor(result.command.ID), Node: m.Node.Name()},
			changeset.Start{Changeset: result.changeset},
		)
		if err != nil {
			slog.Error("Force TTL check: failed to start changeset executor", "stack", stackInfo.StackLabel, "error", err)
			continue
		}

		expiredLabels = append(expiredLabels, stackInfo.StackLabel)
		commandIDs = append(commandIDs, result.command.ID)
	}

	return &apimodel.ForceCheckTTLResponse{ExpiredStacks: expiredLabels, CommandIDs: commandIDs}, nil
}

func (m *Metastructure) ReRunIncompleteCommands() error {
	commands, err := m.Datastore.LoadIncompleteFormaCommands()
	if err != nil {
		slog.Error("Failed to read incomplete forma commands", "error", err)
		return err
	}
	if len(commands) > 0 {
		slog.Debug("Retrying %d incomplete forma commands", "count", len(commands))
	}

	for _, fa := range commands {
		// Derive state from progress and prepare for re-execution.
		// - InProgress: Reset to NotStarted (was interrupted, needs retry)
		// - NotStarted: Keep as NotStarted (never started, needs execution)
		// - Terminal states (Success, Failed, etc.): Exclude from the new
		//   changeset entirely. Including them would re-create dependency
		//   links that can never be resolved (the changeset executor only
		//   picks up NotStarted resources, so a Success parent would block
		//   its children forever).
		var pendingUpdates []resource_update.ResourceUpdate
		for i := range fa.ResourceUpdates {
			ru := &fa.ResourceUpdates[i]
			// Only re-derive state for non-terminal resources.
			// Terminal resources (e.g. cascaded failures) have authoritative DB state
			// but may have empty ProgressResult, which UpdateState() would
			// incorrectly interpret as NotStarted.
			switch ru.State {
			case resource_update.ResourceUpdateStateSuccess,
				resource_update.ResourceUpdateStateFailed,
				resource_update.ResourceUpdateStateRejected,
				resource_update.ResourceUpdateStateCanceled:
				continue
			}
			ru.UpdateState()
			if ru.State == resource_update.ResourceUpdateStateInProgress {
				ru.State = resource_update.ResourceUpdateStateNotStarted
			}
			if ru.State == resource_update.ResourceUpdateStateNotStarted {
				pendingUpdates = append(pendingUpdates, *ru)
			}
		}

		// If all resource updates already reached a terminal state, the command
		// just needs its own state updated — no changeset execution needed.
		// This happens when the agent crashed after all CRUD ops completed but
		// before the command transitioned to a final state.
		if len(pendingUpdates) == 0 {
			_, err := m.callActor(
				gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
				forma_persister.FinalizeIncompleteCommand{CommandID: fa.ID},
			)
			if err != nil {
				slog.Error("Failed to finalize incomplete command", "commandID", fa.ID, "error", err)
			}
			continue
		}

		// If all resource updates already reached a terminal state, the command
		// just needs its own state updated — no changeset execution needed.
		// This happens when the agent crashed after all CRUD ops completed but
		// before the command transitioned to a final state.
		if len(pendingUpdates) == 0 {
			_, err := m.callActor(
				gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
				forma_persister.FinalizeIncompleteCommand{CommandID: fa.ID},
			)
			if err != nil {
				slog.Error("Failed to finalize incomplete command", "commandID", fa.ID, "error", err)
			}
			continue
		}

		var pendingTargetUpdates []target_update.TargetUpdate
		for _, tu := range fa.TargetUpdates {
			if tu.State == target_update.TargetUpdateStateNotStarted {
				pendingTargetUpdates = append(pendingTargetUpdates, tu)
			}
		}

		// Build the changeset from only the pending (non-terminal) resource
		// updates. Terminal resources are excluded so they don't create
		// phantom dependency links in the new changeset's pipeline.
		synth, synthErr := target_update.SynthesizeResolveTargetUpdates(
			resource_update.ReferencedTargetLabels(pendingUpdates),
			resource_update.SourceTargetByKsuid(pendingUpdates),
			pendingTargetUpdates, m.Datastore)
		if synthErr != nil {
			slog.Error("Failed to build changeset for incomplete forma command, skipping", "commandID", fa.ID, "error", synthErr)
			continue
		}
		// No generator draws yet. A draw is meaningless outside the changeset
		// it produced a value for, so it is not stored with the command and
		// cannot be replayed; the surviving destinations have to be re-read to
		// derive it, which this recovery path does not do.
		cs, err := changeset.NewChangeset(pendingUpdates, append(pendingTargetUpdates, synth...), nil, fa.ID, pkgmodel.CommandApply, fa.Config.Mode)
		if err != nil {
			slog.Error("Failed to build changeset for incomplete forma command, skipping", "commandID", fa.ID, "error", err)
			continue
		}

		m.Node.Log().Debug("Starting ChangesetExecutor of changeset from incomplete forma command commandID=%s", fa.ID)
		_, err = m.callActor(
			gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: m.Node.Name()},
			changeset.EnsureChangesetExecutor{CommandID: fa.ID},
		)
		if err != nil {
			slog.Error("Failed to ensure ChangesetExecutor for incomplete forma command", "command", fa.Command, "forma", fa, "error", err)
			return err
		}

		m.Node.Log().Debug("Sending Start message to ChangesetExecutor commandID=%s", fa.ID)
		err = m.Node.Send(
			gen.ProcessID{Name: actornames.ChangesetExecutor(fa.ID), Node: m.Node.Name()},
			changeset.Start{Changeset: cs},
		)
		if err != nil {
			slog.Error("Failed to start ChangesetExecutor for incomplete forma command", "command", fa.Command, "forma", fa, "error", err)
			return err
		}
	}

	return nil
}

func (m *Metastructure) checkForConflictingCommands(commandStackLabels []string) error {
	incompleteFormaCommands, err := m.Datastore.LoadIncompleteFormaCommands()
	if err != nil {
		slog.Error("Failed to load incomplete forma commands", "error", err)
		return fmt.Errorf("failed to load incomplete forma commands: %w", err)
	}

	// Group incomplete resource commands by forma command ID
	incompleteResourceUpdates := make(map[string][]resource_update.ResourceUpdate)
	for _, incompleteFormaCommand := range incompleteFormaCommands {
		if formaTouchesStacks(incompleteFormaCommand, commandStackLabels) {
			for _, incompleteResourceUpdate := range incompleteFormaCommand.ResourceUpdates {
				if incompleteResourceUpdate.Operation != resource_update.OperationRead && incompleteResourceUpdate.State != resource_update.ResourceUpdateStateSuccess && incompleteResourceUpdate.State != resource_update.ResourceUpdateStateFailed && incompleteResourceUpdate.State != resource_update.ResourceUpdateStateRejected {
					incompleteResourceUpdates[incompleteFormaCommand.ID] = append(incompleteResourceUpdates[incompleteFormaCommand.ID], incompleteResourceUpdate)
				}
			}
		}
	}

	// If there are conflicting resource commands, create a copy of the forma command with the conflicting resource commands
	if len(incompleteResourceUpdates) > 0 {
		err := apimodel.FormaConflictingCommandsError{}
		for _, incompleteFormaCommand := range incompleteFormaCommands {
			if resources, ok := incompleteResourceUpdates[incompleteFormaCommand.ID]; ok {
				copy := incompleteFormaCommand
				copy.ResourceUpdates = resources
				err.ConflictingCommands = append(err.ConflictingCommands, translateToAPICommand(copy))
			}
		}

		return err
	}

	return nil
}

// checkForReapedTargets rejects an apply that references a reaped target it does
// not re-declare. It collects every target label the forma touches (via a
// resource's Target or an explicit target declaration), asks the datastore which
// of those are currently reaped, and rejects with a TargetReapedError for any
// reaped target that the forma does not re-declare. Re-declared targets are the
// recovery path and pass through untouched.
func (m *Metastructure) checkForReapedTargets(forma *pkgmodel.Forma) error {
	redeclared := make(map[string]bool)
	for _, t := range forma.Targets {
		redeclared[t.Label] = true
	}

	touched := make(map[string]bool)
	var touchedLabels []string
	addTouched := func(label string) {
		if label == "" || touched[label] {
			return
		}
		touched[label] = true
		touchedLabels = append(touchedLabels, label)
	}
	for _, r := range forma.Resources {
		addTouched(r.Target)
	}
	for _, t := range forma.Targets {
		addTouched(t.Label)
	}

	if len(touchedLabels) == 0 {
		return nil
	}

	reaped, err := m.Datastore.CheckTargetsReaped(touchedLabels)
	if err != nil {
		return fmt.Errorf("failed to check reaped targets: %w", err)
	}

	var unsafe []string
	for _, label := range reaped {
		if !redeclared[label] {
			unsafe = append(unsafe, label)
		}
	}
	if len(unsafe) > 0 {
		return apimodel.TargetReapedError{TargetLabels: unsafe}
	}

	return nil
}

// stackLabelsFromForma extracts unique stack labels from a forma's resources.
func stackLabelsFromForma(forma *pkgmodel.Forma) []string {
	seen := make(map[string]bool)
	var labels []string
	for _, r := range forma.Resources {
		if !seen[r.Stack] {
			seen[r.Stack] = true
			labels = append(labels, r.Stack)
		}
	}
	return labels
}

// findCascadeStackLabels returns stack labels that would be affected by cascade
// deletes for the given forma's resources. It queries the datastore for
// cross-stack dependents of resources being destroyed.
func (m *Metastructure) findCascadeStackLabels(forma *pkgmodel.Forma) ([]string, error) {
	// Client-submitted formas carry no ksuids, only (stack, label, type)
	// triplets — resolve those against the datastore so the dependents walk
	// actually has roots. Without this the walk is empty for every destroy
	// that arrives over the API, and the admission conflict check never sees
	// the stacks a cascade delete will touch.
	currentLevel := make([]string, 0)
	var unresolved []pkgmodel.TripletKey
	for _, r := range forma.Resources {
		if r.Ksuid != "" {
			currentLevel = append(currentLevel, r.Ksuid)
			continue
		}
		unresolved = append(unresolved, pkgmodel.TripletKey{Stack: r.Stack, Label: r.Label, Type: r.Type})
	}
	if len(unresolved) > 0 {
		resolved, err := m.Datastore.BatchGetKSUIDsByTriplets(unresolved)
		if err != nil {
			return nil, err
		}
		for _, ksuid := range resolved {
			currentLevel = append(currentLevel, ksuid)
		}
	}
	if len(currentLevel) == 0 {
		return nil, nil
	}

	seenStacks := make(map[string]bool)
	for _, r := range forma.Resources {
		seenStacks[r.Stack] = true
	}

	processed := make(map[string]bool)
	var cascadeStacks []string

	// BFS: traverse dependents level by level (mirrors findCascadeDeletes)
	for len(currentLevel) > 0 {
		dependentsMap, err := m.Datastore.FindResourcesDependingOnMany(currentLevel)
		if err != nil {
			return nil, err
		}

		var nextLevel []string
		for _, dependents := range dependentsMap {
			for _, dep := range dependents {
				if processed[dep.Ksuid] {
					continue
				}
				processed[dep.Ksuid] = true

				if !seenStacks[dep.Stack] && dep.Stack != constants.UnmanagedStack {
					seenStacks[dep.Stack] = true
					cascadeStacks = append(cascadeStacks, dep.Stack)
				}
				nextLevel = append(nextLevel, dep.Ksuid)
			}
		}
		currentLevel = nextLevel
	}

	return cascadeStacks, nil
}

// findCascadeTargetDeletes discovers targets whose config contains $ref references to
// resources being deleted. It returns cascade TargetUpdates and cascade ResourceUpdates
// for all managed resources in those targets.
//
// The function uses BFS to handle transitive cascades: if deleting resource A causes
// target T to be cascade-deleted, and T has resources that are referenced by target U,
// then U is also cascade-deleted.
func findCascadeTargetDeletes(
	resourceUpdates []resource_update.ResourceUpdate,
	existingTargetUpdates []target_update.TargetUpdate,
	existingTargets []*pkgmodel.Target,
	source resource_update.FormaCommandSource,
	ds datastore.Datastore,
) ([]target_update.TargetUpdate, []resource_update.ResourceUpdate, error) {

	// Collect KSUIDs of all resources being deleted and build a KSUID→label
	// lookup so cascade sources can be displayed as human-readable labels.
	deletingKSUIDs := make([]string, 0)
	deletingSet := make(map[string]bool)
	ksuidToLabel := make(map[string]string)
	for _, ru := range resourceUpdates {
		if ru.Operation == resource_update.OperationDelete && ru.DesiredState.Ksuid != "" {
			if !deletingSet[ru.DesiredState.Ksuid] {
				deletingSet[ru.DesiredState.Ksuid] = true
				deletingKSUIDs = append(deletingKSUIDs, ru.DesiredState.Ksuid)
				ksuidToLabel[ru.DesiredState.Ksuid] = ru.DesiredState.Label
			}
		}
	}

	if len(deletingKSUIDs) == 0 {
		return nil, nil, nil
	}

	// Build set of targets already being deleted (from explicit target updates)
	alreadyDeleting := make(map[string]bool)
	for _, tu := range existingTargetUpdates {
		if tu.Operation == target_update.TargetOperationDelete {
			alreadyDeleting[tu.Target.Label] = true
		}
	}

	// Build target map for resource update factory
	existingTargetMap := make(map[string]*pkgmodel.Target)
	for _, t := range existingTargets {
		existingTargetMap[t.Label] = t
	}

	var cascadeTargetUpdates []target_update.TargetUpdate
	var cascadeResourceUpdates []resource_update.ResourceUpdate

	// Load all resources once before the BFS loop — the result doesn't
	// change between iterations.
	allResourcesByStack, err := ds.LoadAllResourcesByStack()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load resources for cascade target delete: %w", err)
	}

	// BFS: find targets depending on deleted resources, then find resources
	// in those targets (which may trigger further target cascades)
	currentLevel := deletingKSUIDs
	for len(currentLevel) > 0 {
		dependentTargetsMap, err := ds.FindTargetsDependingOnMany(currentLevel)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to find targets depending on resources: %w", err)
		}

		var newDeletedTargetLabels []string
		for sourceKSUID, targets := range dependentTargetsMap {
			for _, target := range targets {
				if alreadyDeleting[target.Label] {
					continue
				}
				alreadyDeleting[target.Label] = true

				cascadeSourceLabel := ksuidToLabel[sourceKSUID]
				if cascadeSourceLabel == "" {
					cascadeSourceLabel = sourceKSUID
				}
				slog.Debug("Cascade target delete detected",
					"target", target.Label,
					"cascadeSource", cascadeSourceLabel)

				cascadeTargetUpdates = append(cascadeTargetUpdates,
					target_update.NewTargetUpdateForCascadeDelete(target, cascadeSourceLabel))
				newDeletedTargetLabels = append(newDeletedTargetLabels, target.Label)
			}
		}

		// Generate resource deletes for managed resources in cascade-deleted targets
		// and collect their KSUIDs for the next BFS level
		var nextLevel []string
		if len(newDeletedTargetLabels) > 0 {
			newDeletedSet := make(map[string]bool, len(newDeletedTargetLabels))
			for _, label := range newDeletedTargetLabels {
				newDeletedSet[label] = true
			}

			for _, resources := range allResourcesByStack {
				for _, res := range resources {
					if !res.Managed || !newDeletedSet[res.Target] {
						continue
					}
					if deletingSet[res.Ksuid] {
						continue // Already being deleted
					}

					target, ok := existingTargetMap[res.Target]
					if !ok {
						continue
					}

					resourceDestroy, err := resource_update.NewResourceUpdateForDestroy(*res, *target, source)
					if err != nil {
						return nil, nil, fmt.Errorf("failed to create cascade resource destroy for %s: %w", res.Label, err)
					}
					resourceDestroy.IsCascade = true
					resourceDestroy.CascadeSource = res.Target // cascade source is the target being deleted

					cascadeResourceUpdates = append(cascadeResourceUpdates, resourceDestroy)
					deletingSet[res.Ksuid] = true
					nextLevel = append(nextLevel, res.Ksuid)
				}
			}
		}

		currentLevel = nextLevel
	}

	return cascadeTargetUpdates, cascadeResourceUpdates, nil
}

// toAPIResourceModification converts a datastore ResourceModification into its
// API-model counterpart. For update operations it also computes the JSON-patch
// document describing the drift between the properties at the last reconcile
// and the current (cloud) properties. A failed patch computation degrades to
// label-only display — it never fails the caller.
func toAPIResourceModification(modification datastore.ResourceModification) apimodel.ResourceModification {
	patchDoc := json.RawMessage(nil)
	if modification.Operation == "update" && len(modification.OldProperties) > 0 && len(modification.Properties) > 0 {
		if p, perr := patch.DriftPatch(modification.OldProperties, modification.Properties); perr == nil {
			patchDoc = p
		} else {
			slog.Warn("failed to compute drift patch", "stack", modification.Stack, "label", modification.Label, "error", perr)
		}
	}
	return apimodel.ResourceModification{
		Stack:         modification.Stack,
		Type:          modification.Type,
		Label:         modification.Label,
		Operation:     modification.Operation,
		PatchDocument: patchDoc,
		Properties:    modification.Properties,
		OldProperties: modification.OldProperties,
	}
}

// filterUnabsorbedModifications returns only those modifications that have NOT been
// absorbed into the provided forma. A modification is considered absorbed when:
//   - The forma contains a resource with matching stack, type, and label
//   - No resource update was generated for that resource (i.e. its properties already
//     match the current state in the datastore)
//
// This prevents false drift rejection when the user has already incorporated
// out-of-band changes into their forma (e.g. via extract) before applying.
func filterUnabsorbedModifications(
	modifications []datastore.ResourceModification,
	forma *pkgmodel.Forma,
	fa *forma_command.FormaCommand,
) []datastore.ResourceModification {
	// Build a set of resources that have pending updates in the FormaCommand
	type resourceKey struct {
		stack    string
		typeName string
		label    string
	}
	resourcesWithUpdates := make(map[resourceKey]struct{})
	for _, ru := range fa.ResourceUpdates {
		// A convergence-only update propagates a source movement the stored
		// state has already absorbed (e.g. a synced secret rotation reaching
		// its consumer). It asserts no state differing from the current one,
		// so it must not block absorbing the modification it follows from.
		if ru.ConvergenceOnly() {
			continue
		}
		resourcesWithUpdates[resourceKey{
			stack:    ru.StackLabel,
			typeName: ru.DesiredState.Type,
			label:    ru.DesiredState.Label,
		}] = struct{}{}
	}

	// Build a set of resources present in the forma
	formaResources := make(map[resourceKey]struct{})
	// A forma resource that declares an `alias` covers its previous
	// label too. Index aliases by (stack, type, alias) so a drift recorded
	// under the old label is absorbed when the forma renames the resource.
	formaAliases := make(map[resourceKey]struct{})
	for _, r := range forma.Resources {
		formaResources[resourceKey{
			stack:    r.Stack,
			typeName: r.Type,
			label:    r.Label,
		}] = struct{}{}
		if r.Alias != "" {
			formaAliases[resourceKey{
				stack:    r.Stack,
				typeName: r.Type,
				label:    r.Alias,
			}] = struct{}{}
		}
	}

	var unabsorbed []datastore.ResourceModification
	for _, mod := range modifications {
		key := resourceKey{
			stack:    mod.Stack,
			typeName: mod.Type,
			label:    mod.Label,
		}
		// A modification is absorbed if:
		// 1. The resource is present in the forma, AND
		// 2. No resource update was generated for it (properties match current state)
		_, inForma := formaResources[key]
		_, hasUpdate := resourcesWithUpdates[key]
		if inForma && !hasUpdate {
			continue // absorbed
		}
		// Alias-aware absorption. A modification keyed by the OLD
		// label is absorbed by a forma resource declaring `alias = <old>`.
		// The rename update (if any) takes the modification with it.
		if _, isAlias := formaAliases[key]; isAlias {
			continue
		}
		unabsorbed = append(unabsorbed, mod)
	}
	return unabsorbed
}

func formaTouchesStacks(forma *forma_command.FormaCommand, stackLabels []string) bool {
	formaStackLabels := forma.GetStackLabels()
	for _, formaStackLabel := range formaStackLabels {
		for _, stackLabel := range stackLabels {
			if formaStackLabel == stackLabel {
				return true
			}
		}
	}

	return false
}

func (m *Metastructure) checkIfPatchCanBeApplied(command *forma_command.FormaCommand) error {
	resourcesByStack, err := m.Datastore.LoadAllResourcesByStack()
	if err != nil {
		slog.Error("Failed to load all stacks", "error", err)
		return fmt.Errorf("failed to load all stacks: %w", err)
	}

	for _, stackLabel := range command.GetStackLabels() {
		if _, exists := resourcesByStack[stackLabel]; !exists {
			return apimodel.FormaPatchRejectedError{
				UnknownStacks: []*pkgmodel.Stack{{Label: stackLabel}},
			}
		}
	}

	return nil
}

// checkForEmptyStackCreation validates that no new stacks are being created without resources
// or generators. Empty stacks are automatically cleaned up when the last resource is removed,
// so creating them manually is not allowed — but a generator is content too: a stack whose
// only declared member is a generator is exactly the case the generator lifecycle is meant to
// support (see the GeneratorOnlyStackKeepsExistingResources regression test), so it must not
// be rejected as empty.
func checkForEmptyStackCreation(command *forma_command.FormaCommand) error {
	// Build a set of stacks that have resources or generators in this command
	stacksWithResources := make(map[string]bool)
	for _, ru := range command.ResourceUpdates {
		stacksWithResources[ru.StackLabel] = true
	}
	for _, gu := range command.GeneratorUpdates {
		stacksWithResources[gu.StackLabel] = true
	}

	// Check if any stack update is creating a new stack without resources
	var emptyStacks []string
	for _, su := range command.StackUpdates {
		if su.Operation == stack_update.StackOperationCreate {
			if !stacksWithResources[su.Stack.Label] {
				emptyStacks = append(emptyStacks, su.Stack.Label)
			}
		}
	}

	if len(emptyStacks) > 0 {
		return apimodel.FormaEmptyStackRejectedError{EmptyStacks: emptyStacks}
	}

	return nil
}

func FormaCommandFromForma(forma *pkgmodel.Forma,
	formaCommandConfig *config.FormaCommandConfig,
	command pkgmodel.Command,
	ds datastore.Datastore,
	clientID string,
	subject string,
	subjectName string,
	source resource_update.FormaCommandSource,
	syncInterval time.Duration) (*forma_command.FormaCommand, error) {

	if formaCommandConfig.Mode == "" {
		formaCommandConfig.Mode = pkgmodel.FormaApplyModePatch
	}

	existingTargets, err := ds.LoadAllTargets()
	if err != nil {
		return nil, fmt.Errorf("failed to load targets: %w", err)
	}

	// Reject opaque resolvables embedded in string fields before translation.
	// Must run pre-translation because translation drops $visibility from $res
	// envelopes, making a post-translation check unable to see the opaque flag.
	if err := validateNoOpaqueEmbed(forma); err != nil {
		return nil, err
	}

	// Translate $res triplet references to $ref KSUID URIs in both resource
	// properties and target configs. Must happen before GenerateTargetUpdates
	// so that target config resolvables can be extracted.
	doTranslate := source != resource_update.FormaCommandSourceSynchronize &&
		source != resource_update.FormaCommandSourceDiscovery &&
		command != pkgmodel.CommandDestroy
	// genKeyToKsuid carries the KSUIDs translation resolved for this
	// command's own declared generators through to GenerateGeneratorUpdates
	// below, so a generator created by this same command gets the exact
	// KSUID any $gen reference to it was translated to, instead of
	// CreateGenerator minting an independent one. Left nil on a path that
	// skips translation (Sync/Discovery/Destroy never declare generators).
	var genKeyToKsuid map[pkgmodel.GeneratorKey]string
	if doTranslate {
		var err error
		if _, genKeyToKsuid, err = resource_update.TranslateFormaeReferencesToKsuid(forma, ds); err != nil {
			return nil, fmt.Errorf("failed to translate references to KSUID: %w", err)
		}
	}

	minReapDuration := reaping.DeriveMinReapDuration(reaping.DeriveMaxBeatGap(syncInterval))
	targetUpdates, err := target_update.NewTargetUpdateGenerator(ds).
		WithMinReapDuration(minReapDuration).
		GenerateTargetUpdates(forma.Targets, command, len(forma.Resources) > 0)
	if err != nil {
		return nil, err
	}

	// Build affected targets sets for resource generation
	var replacedTargets map[string]bool
	var deletedTargets map[string]bool
	for _, tu := range targetUpdates {
		switch tu.Operation {
		case target_update.TargetOperationReplace:
			if replacedTargets == nil {
				replacedTargets = make(map[string]bool)
			}
			replacedTargets[tu.Target.Label] = true
		case target_update.TargetOperationDelete:
			if deletedTargets == nil {
				deletedTargets = make(map[string]bool)
			}
			deletedTargets[tu.Target.Label] = true
		}
	}

	resourceUpdates, err := resource_update.GenerateResourceUpdates(forma, command, formaCommandConfig.Mode, source, existingTargets, ds, replacedTargets, deletedTargets, formaCommandConfig.Force)
	if err != nil {
		if requiredFieldsErr, ok := err.(apimodel.RequiredFieldMissingOnCreateError); ok {
			return nil, requiredFieldsErr
		}
		if targetExistsErr, ok := err.(apimodel.TargetAlreadyExistsError); ok {
			return nil, targetExistsErr
		}
		return nil, fmt.Errorf("failed to generate resource updates: %w", err)
	}

	// For destroy commands, find cascade target deletes: targets whose config
	// references resources being deleted. Also generate resource deletes for
	// all managed resources in those cascade-deleted targets.
	if command == pkgmodel.CommandDestroy {
		cascadeTargetUpdates, cascadeResourceUpdates, err := findCascadeTargetDeletes(
			resourceUpdates, targetUpdates, existingTargets, source, ds,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to find cascade target deletes: %w", err)
		}

		// Default to abort: deleting a resource that a target's config references
		// (e.g. a secret) would cascade-delete that target and its resources. Unless
		// the command carries on-dependents=cascade, reject it and name the
		// dependents — mirroring the resource/stack cascade-abort default. Simulation
		// still surfaces the cascades so the client can show them and prompt the user.
		if len(cascadeTargetUpdates) > 0 &&
			!formaCommandConfig.Simulate &&
			formaCommandConfig.OnDependents != "cascade" {
			dependents := make([]apimodel.TargetDependent, 0, len(cascadeTargetUpdates))
			for _, tu := range cascadeTargetUpdates {
				dependents = append(dependents, apimodel.TargetDependent{
					TargetLabel:   tu.Target.Label,
					CascadeSource: tu.CascadeSource,
				})
			}
			return nil, apimodel.FormaTargetHasDependentsError{Dependents: dependents}
		}

		// Same default-abort for resource-to-resource cascades: deleting a resource
		// whose CreateOnly field another resource references cascade-deletes that
		// dependent (possibly in another stack — findCascadeDeletes matches by ref
		// URI across all managed stacks). These IsCascade deletes are already folded
		// into resourceUpdates by the generator. Gate them server-side too, so a
		// non-CLI caller cannot tear down dependents without on-dependents=cascade;
		// the CLI still surfaces them via simulation and elevates on confirmation.
		//
		// Exclude the resource's own target being torn down: a resource deleted
		// because its target is destroyed in this same command (the generator also
		// marks that IsCascade, with CascadeSource = the target) is the expected
		// consequence of an explicit target destroy, not a surprising dependency
		// cascade, so it must not require opt-in.
		if !formaCommandConfig.Simulate && formaCommandConfig.OnDependents != "cascade" {
			targetsBeingDeleted := make(map[string]bool)
			for i := range targetUpdates {
				if targetUpdates[i].Operation == target_update.TargetOperationDelete {
					targetsBeingDeleted[targetUpdates[i].Target.Label] = true
				}
			}
			for i := range cascadeTargetUpdates {
				targetsBeingDeleted[cascadeTargetUpdates[i].Target.Label] = true
			}

			var resourceDependents []apimodel.ResourceDependent
			for i := range resourceUpdates {
				ru := &resourceUpdates[i]
				if ru.IsCascade && ru.Operation == resource_update.OperationDelete &&
					!targetsBeingDeleted[ru.DesiredState.Target] {
					resourceDependents = append(resourceDependents, apimodel.ResourceDependent{
						ResourceLabel: ru.DesiredState.Label,
						ResourceType:  ru.DesiredState.Type,
						Stack:         ru.DesiredState.Stack,
						CascadeSource: ru.CascadeSource,
					})
				}
			}
			if len(resourceDependents) > 0 {
				return nil, apimodel.FormaResourceHasDependentsError{Dependents: resourceDependents}
			}
		}

		targetUpdates = append(targetUpdates, cascadeTargetUpdates...)
		resourceUpdates = append(resourceUpdates, cascadeResourceUpdates...)
	}

	stackUpdates, err := stack_update.NewStackUpdateGenerator(ds).GenerateStackUpdates(forma.Stacks, command)
	if err != nil {
		return nil, err
	}

	policyUpdates, err := policy_update.NewPolicyUpdateGenerator(ds).GeneratePolicyUpdates(forma, command, formaCommandConfig.Mode)
	if err != nil {
		return nil, err
	}

	generatorUpdates, err := generator_update.NewGeneratorUpdateGenerator(ds).GenerateGeneratorUpdates(forma, command, formaCommandConfig.Mode, genKeyToKsuid)
	if err != nil {
		return nil, err
	}

	// The draws are derived from the DESTINATIONS that still need a value,
	// not from the generator diff above: a generator whose spec is unchanged
	// produces no GeneratorUpdate, yet a resource newly bound to it still
	// needs a value drawn. genKeyToKsuid is what maps a translated $gen
	// envelope's KSUID back to the generator it names.
	drawGeneratorUpdates, err := generator_update.SynthesizeDrawGeneratorUpdates(
		resourceUpdates, generatorUpdates, genKeyToKsuid, ds)
	if err != nil {
		return nil, err
	}

	fc := forma_command.NewFormaCommand(
		forma,
		formaCommandConfig,
		command,
		resourceUpdates,
		targetUpdates,
		stackUpdates,
		policyUpdates,
		generatorUpdates,
		clientID,
		subject,
		subjectName,
		forma_command.SourceUser,
	)
	fc.DrawGeneratorUpdates = drawGeneratorUpdates

	return fc, nil
}

// RegisteredPlugins returns plugins currently registered with the
// PluginCoordinator. Used by Stats() and by the plugins API handler to
// surface plugins the agent has loaded but orbital has no record of
// (the `make install` from a plugin repo case).
func (m *Metastructure) RegisteredPlugins() ([]messages.RegisteredPluginInfo, error) {
	result, err := m.callActor(gen.ProcessID{Name: actornames.PluginCoordinator, Node: m.Node.Name()}, messages.GetRegisteredPlugins{})
	if err != nil {
		return nil, err
	}
	r, ok := result.(messages.GetRegisteredPluginsResult)
	if !ok {
		return nil, fmt.Errorf("unexpected response type %T from PluginCoordinator", result)
	}
	return r.Plugins, nil
}

func (m *Metastructure) Stats() (*apimodel.Stats, error) {
	stats, err := m.Datastore.Stats()
	if err != nil {
		return nil, fmt.Errorf("failed to get stats from datastore: %w", err)
	}

	registered, regErr := m.RegisteredPlugins()
	if regErr != nil {
		// A registry hiccup shouldn't take down /stats; the rest of the
		// payload is still useful. Log and continue with no plugins.
		slog.Warn("plugin registry lookup failed; stats response will omit plugins", "error", regErr)
	}
	plugins := make([]apimodel.PluginInfo, 0, len(registered))
	for _, p := range registered {
		plugins = append(plugins, apimodel.PluginInfo{
			Namespace:               p.Namespace,
			Version:                 p.Version,
			NodeName:                p.NodeName,
			MaxRequestsPerSecond:    p.MaxRequestsPerSecond,
			ResourceCount:           p.ResourceCount,
			ResourceTypesToDiscover: p.ResourceTypesToDiscover,
			RetryConfig:             p.RetryConfig,
			LabelConfig:             &p.LabelConfig,
			DiscoveryFilters:        p.DiscoveryFilters,
		})
	}

	reapPending, reaped, reapErr := m.reapTargetCounts()
	if reapErr != nil {
		// Same posture as the plugin registry lookup above: don't fail the
		// whole /stats response over this, just omit the counts.
		slog.Warn("failed to compute reap-pending/reaped target counts; stats response will report zero", "error", reapErr)
	}

	return &apimodel.Stats{
		Version:            formae.Version,
		AgentID:            m.AgentID,
		Clients:            stats.Clients,
		Commands:           stats.Commands,
		States:             stats.States,
		Stacks:             stats.Stacks,
		ManagedResources:   stats.ManagedResources,
		UnmanagedResources: stats.UnmanagedResources,
		Targets:            stats.Targets,
		ResourceTypes:      stats.ResourceTypes,
		Plugins:            plugins,
		ReapPendingTargets: reapPending,
		ReapedTargets:      reaped,
	}, nil
}

// reapTargetCounts derives the reap-pending and reaped target counts for the
// stats surface directly from LoadAllTargets (implemented identically across
// every datastore backend), so no per-backend Stats() query is needed. See
// TargetReapStatus for what "reap-pending" means.
func (m *Metastructure) reapTargetCounts() (reapPending, reaped int, err error) {
	targets, err := m.Datastore.LoadAllTargets()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to load targets: %w", err)
	}

	for _, target := range targets {
		status, statusErr := target_reaper.TargetReapStatus(target)
		if statusErr != nil {
			slog.Warn("failed to resolve reap status for target; skipping from stats", "target", target.Label, "error", statusErr)
			continue
		}
		switch status {
		case pkgmodel.TargetHealthStateReapPending:
			reapPending++
		case pkgmodel.TargetHealthStateReaped:
			reaped++
		}
	}

	return reapPending, reaped, nil
}

func extractKSUIDs(jsonStr string, ksuidSet map[string]struct{}) {
	result := gjson.Parse(jsonStr)

	result.ForEach(func(key, value gjson.Result) bool {
		switch value.Type {
		case gjson.String:
			if ksuid := pkgmodel.FormaeURI(value.String()).KSUID(); ksuid != "" {
				ksuidSet[ksuid] = struct{}{}
			}
		case gjson.JSON:
			if value.IsArray() {
				// Handle arrays - may contain strings or objects with nested $ref
				value.ForEach(func(_, item gjson.Result) bool {
					if item.Type == gjson.String {
						if ksuid := pkgmodel.FormaeURI(item.String()).KSUID(); ksuid != "" {
							ksuidSet[ksuid] = struct{}{}
						}
					} else if item.IsObject() {
						// Recursively extract from objects inside arrays
						extractKSUIDs(item.Raw, ksuidSet)
					}
					return true
				})
			} else if value.IsObject() {
				// Handle nested objects
				extractKSUIDs(value.Raw, ksuidSet)
			}
		}
		return true
	})
}

// replaceKSUIDs recursively walks the JSON structure and replaces all $ref objects
// (containing formae URIs) with $res objects (containing resolved resource metadata).
//
// This handles $ref only. A $gen envelope's bare $generator KSUID is not
// reverse-translated here, and extractKSUIDs above does not even collect it
// (it only looks at strings that parse as formae:// URIs): no resource row
// can persist a $gen today, so there is nothing for this function to see.
// The reverse arm for $gen is owed by whichever slice lands value drawing.
func replaceKSUIDs(jsonStr string, ksuidToTriplet map[string]pkgmodel.TripletKey) string {
	var replace func(value any) any
	replace = func(value any) any {
		switch v := value.(type) {
		case map[string]any:
			// Check if this is a $ref object that needs conversion
			if ref, ok := v["$ref"].(string); ok {
				formaeUri := pkgmodel.FormaeURI(ref)
				if ksuid := formaeUri.KSUID(); ksuid != "" {
					if triplet, ok := ksuidToTriplet[ksuid]; ok {
						dollarValue, _ := v["$value"].(string)
						rewritten := map[string]any{
							"$res":      true,
							"$label":    triplet.Label,
							"$type":     triplet.Type,
							"$stack":    triplet.Stack,
							"$property": formaeUri.PropertyPath(),
							"$value":    dollarValue,
						}
						// The selector is part of what the reference means: it
						// names the sub-key of the referenced property. Dropping
						// it would extract a reference to the whole document, so
						// re-applying the extracted forma would write that
						// document where one of its members belongs. Provenance
						// keys are deliberately not carried over: they record
						// formae's own writes and are stripped from any incoming
						// forma.
						if selector, ok := v["$json"].(string); ok && selector != "" {
							rewritten["$json"] = selector
						}
						return rewritten
					}
				}
			}
			// Rewrite framed envelopes inside $embed.$template spans
			if isEmbed, _ := v["$embed"].(bool); isEmbed {
				if tmpl, ok := v["$template"].(string); ok {
					result := make(map[string]any, len(v))
					for key, val := range v {
						result[key] = replace(val)
					}
					result["$template"] = rewriteEmbedSpans(tmpl, func(env map[string]any) map[string]any {
						rewritten, ok := replace(env).(map[string]any)
						if !ok {
							// defensive: replace returned a non-map; leave the span unchanged
							return env
						}
						return rewritten
					})
					return result
				}
			}
			// Recursively process all values in the map
			result := make(map[string]any, len(v))
			for key, val := range v {
				result[key] = replace(val)
			}
			return result
		case []any:
			// Recursively process all items in the array
			result := make([]any, len(v))
			for i, item := range v {
				result[i] = replace(item)
			}
			return result
		default:
			return value
		}
	}

	var data any
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return jsonStr
	}

	replaced := replace(data)
	result, err := json.Marshal(replaced)
	if err != nil {
		return jsonStr
	}
	return string(result)
}

// rewriteEmbedSpans scans a $embed.$template string for framed RS<base64>US spans,
// applies fn to each decoded envelope (as a map), re-encodes, and splices back.
// Spans are replaced in reverse offset order so earlier offsets remain valid.
// On scan error the original template is returned unchanged.
func rewriteEmbedSpans(tmpl string, fn func(map[string]any) map[string]any) string {
	spans, err := pkgmodel.ScanEmbedSpans(tmpl)
	if err != nil || len(spans) == 0 {
		return tmpl
	}

	// Work backwards so byte offsets of earlier spans stay valid.
	for i := len(spans) - 1; i >= 0; i-- {
		span := spans[i]
		var env map[string]any
		if jsonErr := json.Unmarshal([]byte(span.EnvelopeJSON), &env); jsonErr != nil {
			continue
		}
		rewritten := fn(env)
		rewrittenJSON, marshalErr := json.Marshal(rewritten)
		if marshalErr != nil {
			continue
		}
		framed := pkgmodel.FrameEnvelope(string(rewrittenJSON))
		tmpl = tmpl[:span.Start] + framed + tmpl[span.End:]
	}
	return tmpl
}

// validateNoOpaqueEmbed rejects any forma whose resource properties or target
// configs contain a $embed field whose $template carries a framed span for a
// $res envelope with $visibility == "Opaque".
//
// v1 limitation: once an opaque resolvable is assembled into a string the
// structured span needed for redaction is lost, so we hard-reject it at plan
// time rather than silently leaking secrets.
//
// This MUST run before translation (doTranslate) because translation replaces
// $res envelopes with {"$ref":…} and drops $visibility.
func validateNoOpaqueEmbed(forma *pkgmodel.Forma) error {
	for i := range forma.Resources {
		r := &forma.Resources[i]
		if err := validateNoOpaqueEmbedInJSON(r.Properties, r.Label); err != nil {
			return err
		}
	}
	for i := range forma.Targets {
		t := &forma.Targets[i]
		if err := validateNoOpaqueEmbedInJSON(t.Config, t.Label); err != nil {
			return err
		}
	}
	return nil
}

// validateNoOpaqueEmbedInJSON walks all JSON objects in raw looking for
// {"$embed":true, "$template":"…"} nodes, scans each template for framed
// spans, and returns an error if any span envelope carries "$visibility":"Opaque".
func validateNoOpaqueEmbedInJSON(raw json.RawMessage, label string) error {
	if len(raw) == 0 {
		return nil
	}
	result := gjson.ParseBytes(raw)
	return walkForOpaqueEmbed(result, label, "")
}

func walkForOpaqueEmbed(val gjson.Result, label, path string) error {
	if val.IsArray() {
		// Recurse into each array element; mirror how the resolver's extractFromJson
		// handles IsArray() so that embedded opaques nested in arrays are caught.
		var childErr error
		val.ForEach(func(key, child gjson.Result) bool {
			childPath := key.String()
			if path != "" {
				childPath = path + "." + childPath
			}
			if err := walkForOpaqueEmbed(child, label, childPath); err != nil {
				childErr = err
				return false
			}
			return true
		})
		return childErr
	}
	if !val.IsObject() {
		return nil
	}
	// Check if this object is an embed node.
	if val.Get("$embed").Bool() {
		tmpl := val.Get("$template")
		if tmpl.Type == gjson.String {
			spans, err := pkgmodel.ScanEmbedSpans(tmpl.String())
			if err != nil {
				return fmt.Errorf("corrupt embed template in field %q on %q: %w", path, label, err)
			}
			for _, span := range spans {
				visibility := gjson.Get(span.EnvelopeJSON, "$visibility")
				if visibility.String() == pkgmodel.VisibilityOpaque {
					return fmt.Errorf("opaque resolvables cannot be embedded in string fields (field %q on %q); v1 limitation", path, label)
				}
			}
		}
	}
	// Recurse into all child values.
	var childErr error
	val.ForEach(func(key, child gjson.Result) bool {
		childPath := key.String()
		if path != "" {
			childPath = path + "." + childPath
		}
		if err := walkForOpaqueEmbed(child, label, childPath); err != nil {
			childErr = err
			return false
		}
		return true
	})
	return childErr
}
