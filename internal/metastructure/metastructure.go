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
	"time"

	"ergo.services/application/observer"
	"ergo.services/ergo"
	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/registrar"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/discovery"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/querier"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

const (
	// actorCallTimeout is the maximum time we wait for the MetastructureBridge actor to respond
	actorCallTimeout = 30 * time.Second
)

type MetastructureAPI interface {
	ApplyForma(forma *pkgmodel.Forma, config *config.FormaCommandConfig, clientID string) (*apimodel.SubmitCommandResponse, error)
	DestroyForma(forma *pkgmodel.Forma, config *config.FormaCommandConfig, clientID string) (*apimodel.SubmitCommandResponse, error)
	DestroyByQuery(query string, config *config.FormaCommandConfig, clientID string) (*apimodel.SubmitCommandResponse, error)
	CancelCommand(commandID string, clientID string) error
	CancelCommandsByQuery(query string, clientID string) (*apimodel.CancelCommandResponse, error)
	ListFormaCommandStatus(query string, clientID string, n int) (*apimodel.ListCommandStatusResponse, error)
	ExtractResources(query string) (*pkgmodel.Forma, error)
	ExtractTargets(query string) ([]*pkgmodel.Target, error)
	ExtractStacks() ([]*pkgmodel.Stack, error)
	ForceSync() error
	ForceDiscovery() error
	Stats() (*apimodel.Stats, error)
}

type Metastructure struct {
	nodeName      string
	options       gen.NodeOptions
	Node          gen.Node
	Datastore     datastore.Datastore
	PluginManager *plugin.Manager
	Cfg           *pkgmodel.Config
	AgentID       string
}

func NewMetastructure(ctx context.Context, cfg *pkgmodel.Config, pluginManager *plugin.Manager, agentID string) (*Metastructure, error) {
	var (
		ds  datastore.Datastore
		err error
	)

	switch cfg.Agent.Datastore.DatastoreType {
	case pkgmodel.PostgresDatastore:
		ds, err = datastore.NewDatastorePostgres(ctx, &cfg.Agent.Datastore, agentID)
	case pkgmodel.AuroraDataAPIDatastore:
		ds, err = datastore.NewDatastoreAuroraDataAPI(ctx, &cfg.Agent.Datastore, agentID)
	default:
		ds, err = datastore.NewDatastoreSQLite(ctx, &cfg.Agent.Datastore, agentID)
	}

	if err != nil {
		return nil, err
	}

	return NewMetastructureWithDataStoreAndContext(ctx, cfg, pluginManager, ds, agentID)
}

func NewMetastructureWithDataStoreAndContext(ctx context.Context, cfg *pkgmodel.Config, pluginManager *plugin.Manager, datastore datastore.Datastore, agentID string) (*Metastructure, error) {
	metastructure := &Metastructure{}

	metastructure.Datastore = datastore
	metastructure.Cfg = cfg
	metastructure.PluginManager = pluginManager

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
		gen.Env("PluginManager"):         metastructure.PluginManager,
		gen.Env("Datastore"):             metastructure.Datastore,
		gen.Env("Context"):               ctx,
		gen.Env("disable_metrics"):       true,
		gen.Env("ServerConfig"):          cfg.Agent.Server,
		gen.Env("DatastoreConfig"):       cfg.Agent.Datastore,
		gen.Env("RetryConfig"):           cfg.Agent.Retry,
		gen.Env("PluginConfig"):          cfg.Plugins,
		gen.Env("SynchronizationConfig"): cfg.Agent.Synchronization,
		gen.Env("DiscoveryConfig"):       cfg.Agent.Discovery,
		gen.Env("LoggingConfig"):         cfg.Agent.Logging,
		gen.Env("OTelConfig"):            cfg.Agent.OTel,
		gen.Env("AgentID"):               agentID,
	}

	// Enable Ergo networking for distributed plugin architecture
	metastructure.options.Network.Mode = gen.NetworkModeEnabled

	// Disable environment sharing for RemoteSpawn because the agent's environment contains
	// non-serializable types (Datastore, PluginManager, Context). We inject the relevant
	// (serializable) parts of the environment during remote spawn in the PluginCoordinator actor.
	metastructure.options.Security.ExposeEnvRemoteSpawn = false

	//FIXME(discount-elf): enable real TLS if we want it
	//cert, _ := lib.GenerateSelfSignedCert("formae node")
	//metastructure.options.CertManager = gen.CreateCertManager(cert)

	// Use the secret from config which now defaults to a random value via PKL
	metastructure.options.Network.Cookie = cfg.Agent.Server.Secret

	// Configure Ergo listen address with custom port (enables parallel test execution)
	if cfg.Agent.Server.ErgoPort != 0 {
		metastructure.options.Network.Acceptors = []gen.AcceptorOptions{
			{
				Host:      cfg.Agent.Server.Hostname,
				Port:      uint16(cfg.Agent.Server.ErgoPort),
				Registrar: registrar.Create(registrar.Options{Port: uint16(cfg.Agent.Server.ErgoPort)}),
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

func (m *Metastructure) ApplyForma(forma *pkgmodel.Forma, config *config.FormaCommandConfig, clientID string) (*apimodel.SubmitCommandResponse, error) {
	fa, err := FormaCommandFromForma(forma, config, pkgmodel.CommandApply, m.Datastore, clientID, resource_update.FormaCommandSourceUser)
	if err != nil {
		if requiredFieldsErr, ok := err.(apimodel.RequiredFieldMissingOnCreateError); ok {
			return nil, requiredFieldsErr
		}
		if targetExistsErr, ok := err.(apimodel.TargetAlreadyExistsError); ok {
			return nil, targetExistsErr
		}
		slog.Error("Failed to create apply from forma", "error", err)
		return nil, err
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
	if len(fa.ResourceUpdates) > 0 {
		cs, err = changeset.NewChangesetFromResourceUpdates(fa.ResourceUpdates, fa.ID, fa.Command)
		if err != nil {
			return nil, err
		}
	}

	if config.Mode == pkgmodel.FormaApplyModeReconcile && !config.Force {
		var modifiedStacks = make(map[string]apimodel.ModifiedStack)
		for _, stackLabel := range fa.GetStackLabels() {
			modifications, loadErr := m.Datastore.GetResourceModificationsSinceLastReconcile(stackLabel)
			if loadErr != nil {
				slog.Error("Failed to load most recent non-reconcile forma commands by stack", "stack", stackLabel, "error", loadErr)
				return nil, fmt.Errorf("failed to load most recent forma commands for stack %s: %w", stackLabel, loadErr)
			}
			if len(modifications) > 0 {
				modifiedResources := make([]apimodel.ResourceModification, 0, len(modifications))
				for _, modification := range modifications {
					modifiedResources = append(modifiedResources, apimodel.ResourceModification(modification))
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

	if !config.Simulate {
		err = m.checkForConflictingCommands(fa)
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

	m.Node.Log().Debug("Storing forma command", "commandID", fa.ID)
	_, err = m.callActor(
		gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
		forma_persister.StoreNewFormaCommand{Command: *fa},
	)
	if err != nil {
		slog.Error("Failed to store forma command", "error", err)
		return nil, fmt.Errorf("failed to store forma command: %w", err)
	}

	if len(fa.TargetUpdates) > 0 {
		_, err = m.callActor(
			gen.ProcessID{Name: actornames.ResourcePersister, Node: m.Node.Name()},
			target_update.PersistTargetUpdates{
				TargetUpdates: fa.TargetUpdates,
				CommandID:     fa.ID,
			},
		)
		if err != nil {
			slog.Error("Failed to persist target updates", "error", err)
			return nil, fmt.Errorf("failed to persist target updates: %w", err)
		}
		m.Node.Log().Debug("Successfully persisted target updates", "count", len(fa.TargetUpdates))

		_, err = m.callActor(
			gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
			messages.UpdateTargetStates{
				CommandID:     fa.ID,
				TargetUpdates: fa.TargetUpdates,
			},
		)
		if err != nil {
			slog.Error("Failed to update forma command with target states", "error", err)
			return nil, fmt.Errorf("failed to update forma command with target states: %w", err)
		}
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
		m.Node.Log().Debug("Successfully persisted stack updates", "count", len(fa.StackUpdates))
	}
	if len(fa.ResourceUpdates) > 0 {
		m.Node.Log().Debug("Starting ChangesetExecutor of changeset from forma command", "commandID", fa.ID)
		_, err = m.callActor(
			gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: m.Node.Name()},
			changeset.EnsureChangesetExecutor{CommandID: fa.ID},
		)
		if err != nil {
			slog.Error("Failed to ensure ChangesetExecutor for forma command", "command", fa.Command, "forma", fa, "error", err)
			return nil, fmt.Errorf("failed to ensure ChangesetExecutor: %w", err)
		}

		m.Node.Log().Debug("Sending Start message to ChangesetExecutor", "commandID", fa.ID)
		err = m.Node.Send(
			gen.ProcessID{Name: actornames.ChangesetExecutor(fa.ID), Node: m.Node.Name()},
			changeset.Start{Changeset: cs},
		)
		if err != nil {
			slog.Error("Failed to start ChangesetExecutor for forma command", "command", fa.Command, "forma", fa, "error", err)
			return nil, fmt.Errorf("failed to start ChangesetExecutor: %w", err)
		}
	} else {
		m.Node.Log().Debug("No resource updates, skipping ChangesetExecutor (target-only forma)", "commandID", fa.ID)
	}

	return &apimodel.SubmitCommandResponse{CommandID: fa.ID, Description: apimodel.Description(fa.Description)}, nil
}

func translateToAPICommand(fa *forma_command.FormaCommand) apimodel.Command {
	apiCommand := apimodel.Command{
		CommandID: fa.ID,
		Command:   string(fa.Command),
		State:     string(fa.State),
		StartTs:   fa.StartTs,
		EndTs:     fa.ModifiedTs,
	}
	for _, ru := range fa.ResourceUpdates {
		var dur time.Duration = 0
		if !ru.StartTs.IsZero() {
			dur = ru.ModifiedTs.Sub(ru.StartTs)
		}

		apiCommand.ResourceUpdates = append(apiCommand.ResourceUpdates, apimodel.ResourceUpdate{
			ResourceID:     ru.DesiredState.Ksuid,
			ResourceType:   ru.DesiredState.Type,
			ResourceLabel:  ru.DesiredState.Label,
			StackName:      ru.StackLabel,
			OldStackName:   ru.PriorState.Stack,
			Properties:     ru.DesiredState.Properties,
			OldProperties:  ru.PreviousProperties,
			PatchDocument:  ru.DesiredState.PatchDocument,
			Operation:      string(ru.Operation),
			State:          string(ru.State),
			Duration:       dur.Milliseconds(),
			CurrentAttempt: ru.MostRecentProgressResult.Attempts,
			MaxAttempts:    ru.MostRecentProgressResult.MaxAttempts,
			ErrorMessage:   ru.MostRecentFailureMessage(),
			StatusMessage:  ru.MostRecentStatusMessage(),
			GroupID:        ru.GroupID,
		})
	}

	for _, tu := range fa.TargetUpdates {
		var dur time.Duration = 0
		if !tu.StartTs.IsZero() {
			dur = tu.ModifiedTs.Sub(tu.StartTs)
		}

		apiCommand.TargetUpdates = append(apiCommand.TargetUpdates, apimodel.TargetUpdate{
			TargetLabel:  tu.Target.Label,
			Operation:    string(tu.Operation),
			State:        string(tu.State),
			Duration:     dur.Milliseconds(),
			ErrorMessage: tu.ErrorMessage,
			Discoverable: tu.Target.Discoverable,
			StartTs:      tu.StartTs,
			ModifiedTs:   tu.ModifiedTs,
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

	return apiCommand
}

func (m *Metastructure) DestroyForma(forma *pkgmodel.Forma, config *config.FormaCommandConfig, clientID string) (*apimodel.SubmitCommandResponse, error) {
	fa, err := FormaCommandFromForma(forma, config, pkgmodel.CommandDestroy, m.Datastore, clientID, resource_update.FormaCommandSourceUser)
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

	if !config.Simulate {
		err = m.checkForConflictingCommands(fa)
		if err != nil {
			return nil, err
		}
	}

	m.Node.Log().Debug("Storing forma command", "commandID", fa.ID)
	_, err = m.callActor(
		gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
		forma_persister.StoreNewFormaCommand{Command: *fa},
	)
	if err != nil {
		slog.Error("Failed to store forma command", "error", err)
		return nil, fmt.Errorf("failed to store forma command: %w", err)
	}

	if len(fa.TargetUpdates) > 0 {
		_, err = m.callActor(
			gen.ProcessID{Name: actornames.ResourcePersister, Node: m.Node.Name()},
			target_update.PersistTargetUpdates{
				TargetUpdates: fa.TargetUpdates,
				CommandID:     fa.ID,
			},
		)
		if err != nil {
			slog.Error("Failed to persist target updates", "error", err)
			return nil, fmt.Errorf("failed to persist target updates: %w", err)
		}
		m.Node.Log().Debug("Successfully persisted target updates", "count", len(fa.TargetUpdates))

		_, err = m.callActor(
			gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
			messages.UpdateTargetStates{
				CommandID:     fa.ID,
				TargetUpdates: fa.TargetUpdates,
			},
		)
		if err != nil {
			slog.Error("Failed to update forma command with target states", "error", err)
			return nil, fmt.Errorf("failed to update forma command with target states: %w", err)
		}
	}

	if len(fa.ResourceUpdates) > 0 {
		cs, err := changeset.NewChangesetFromResourceUpdates(fa.ResourceUpdates, fa.ID, pkgmodel.CommandDestroy)
		if err != nil {
			return nil, err
		}

		m.Node.Log().Debug("Starting ChangesetExecutor of changeset from forma command", "commandID", fa.ID)
		_, err = m.callActor(
			gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: m.Node.Name()},
			changeset.EnsureChangesetExecutor{CommandID: fa.ID},
		)
		if err != nil {
			slog.Error("Failed to ensure ChangesetExecutor for forma command", "command", fa.Command, "forma", fa, "error", err)
			return nil, fmt.Errorf("failed to ensure ChangesetExecutor: %w", err)
		}

		m.Node.Log().Debug("Sending Start message to ChangesetExecutor", "commandID", fa.ID)
		err = m.Node.Send(
			gen.ProcessID{Name: actornames.ChangesetExecutor(fa.ID), Node: m.Node.Name()},
			changeset.Start{Changeset: cs},
		)
		if err != nil {
			slog.Error("Failed to start ChangesetExecutor for forma command", "command", fa.Command, "forma", fa, "error", err)
			return nil, fmt.Errorf("failed to start ChangesetExecutor: %w", err)
		}
	} else {
		m.Node.Log().Debug("No resource updates, skipping ChangesetExecutor (target-only destroy)", "commandID", fa.ID)
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

func (m *Metastructure) DestroyByQuery(query string, config *config.FormaCommandConfig, clientID string) (*apimodel.SubmitCommandResponse, error) {
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

	return m.DestroyForma(forma, config, clientID)
}

func (m *Metastructure) CancelCommand(commandID string, clientID string) error {
	slog.Info("Canceling command", "commandID", commandID, "clientID", clientID)

	// Send Cancel message to the ChangesetExecutor for this command
	changesetExecutorPID := gen.ProcessID{
		Name: actornames.ChangesetExecutor(commandID),
		Node: m.Node.Name(),
	}

	err := m.Node.Send(changesetExecutorPID, changeset.Cancel{
		CommandID: commandID,
	})
	if err != nil {
		slog.Error("Failed to send cancel message to changeset executor", "commandID", commandID, "error", err)
		return fmt.Errorf("failed to send cancel message: %w", err)
	}

	return nil
}

func (m *Metastructure) CancelCommandsByQuery(query string, clientID string) (*apimodel.CancelCommandResponse, error) {
	var commandsToCancel []*forma_command.FormaCommand
	var err error

	if query != "" {
		// Cancel by query
		q := querier.NewBlugeQuerier(m.Datastore)
		commandsToCancel, err = q.QueryStatus(query, clientID, 100) // limit to 100 commands
		if err != nil {
			slog.Debug("Cannot get forma commands from query", "error", err)
			return nil, err
		}
	} else {
		// Cancel most recent command
		command, err := m.Datastore.GetMostRecentFormaCommandByClientID(clientID)
		if err != nil {
			slog.Debug("Cannot get most recent forma command", "error", err)
			return nil, err
		}
		commandsToCancel = []*forma_command.FormaCommand{command}
	}

	// Filter to only InProgress commands
	var canceledCommandIDs []string
	for _, cmd := range commandsToCancel {
		if cmd.State == forma_command.CommandStateInProgress {
			err := m.CancelCommand(cmd.ID, clientID)
			if err != nil {
				slog.Warn("Failed to cancel command", "commandID", cmd.ID, "error", err)
				// Continue with other commands even if one fails
				continue
			}
			canceledCommandIDs = append(canceledCommandIDs, cmd.ID)
		}
	}

	return &apimodel.CancelCommandResponse{
		CommandIDs: canceledCommandIDs,
	}, nil
}

func (m *Metastructure) ListFormaCommandStatus(query string, clientID string, n int) (*apimodel.ListCommandStatusResponse, error) {
	if query != "" {
		q := querier.NewBlugeQuerier(m.Datastore)
		formaCommands, err := q.QueryStatus(query, clientID, n)
		if err != nil {
			slog.Debug("Cannot get forma commands from query", "error", err)
			return nil, err
		}

		res := &apimodel.ListCommandStatusResponse{}
		for _, fa := range formaCommands {
			res.Commands = append(res.Commands, translateToAPICommand(fa))
		}

		return res, nil
	} else {
		fa, err := m.Datastore.GetMostRecentFormaCommandByClientID(clientID)
		if err != nil {
			slog.Debug("Cannot get forma command from client ID", "error", err)
			return nil, err
		}

		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{translateToAPICommand(fa)},
		}, nil
	}
}

func (m *Metastructure) ExtractResources(query string) (*pkgmodel.Forma, error) {
	q := querier.NewBlugeQuerier(m.Datastore)
	resources, err := q.QueryResources(query)
	if err != nil {
		slog.Debug("Cannot get resources from query", "error", err)
		return nil, err
	}

	if err := m.reverseTranslateKSUIDsToTriplets(resources); err != nil {
		slog.Error("Failed to reverse translate KSUIDs to triplets", "error", err)
		return nil, err
	}

	targetNames := make([]string, 0)
	uniqueTargets := make(map[string]struct{})

	for _, resource := range resources {
		if resource.Target != "" {
			if _, exists := uniqueTargets[resource.Target]; !exists {
				uniqueTargets[resource.Target] = struct{}{}
				targetNames = append(targetNames, resource.Target)
			}
		}
	}

	forma := pkgmodel.FormaFromResources(resources)

	if len(targetNames) > 0 {
		targets, err := m.Datastore.LoadTargetsByLabels(targetNames)
		if err != nil {
			slog.Error("Failed to load targets by names", "error", err)
			return nil, err
		}

		forma.Targets = make([]pkgmodel.Target, 0, len(targets))
		for _, t := range targets {
			if t != nil {
				forma.Targets = append(forma.Targets, *t)
			}
		}
	}

	return forma, nil
}

func (m *Metastructure) ExtractTargets(queryStr string) ([]*pkgmodel.Target, error) {
	slog.Debug("ExtractTargets called", "queryStr", queryStr)
	query := &datastore.TargetQuery{}

	if queryStr != "" {
		parts := strings.Fields(queryStr)
		for _, part := range parts {
			if strings.Contains(part, ":") {
				kv := strings.SplitN(part, ":", 2)
				key := strings.TrimSpace(kv[0])
				value := strings.TrimSpace(kv[1])

				switch key {
				case "label":
					query.Label = &datastore.QueryItem[string]{
						Item:       value,
						Constraint: datastore.Required,
					}
				case "namespace":
					query.Namespace = &datastore.QueryItem[string]{
						Item:       value,
						Constraint: datastore.Required,
					}
				case "discoverable":
					boolVal := value == "true"
					query.Discoverable = &datastore.QueryItem[bool]{
						Item:       boolVal,
						Constraint: datastore.Required,
					}
				}
			}
		}
	}

	slog.Debug("Calling QueryTargets", "query", query)
	targets, err := m.Datastore.QueryTargets(query)
	if err != nil {
		slog.Debug("Cannot get targets from query", "error", err)
		return nil, err
	}

	slog.Debug("ExtractTargets returning", "count", len(targets))
	return targets, nil
}

func (m *Metastructure) ExtractStacks() ([]*pkgmodel.Stack, error) {
	slog.Debug("ExtractStacks called")
	stacks, err := m.Datastore.ListAllStacks()
	if err != nil {
		slog.Debug("Cannot get stacks from datastore", "error", err)
		return nil, err
	}

	slog.Debug("ExtractStacks returning", "count", len(stacks))
	return stacks, nil
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

func (m *Metastructure) ForceSync() error {
	if err := m.Node.Send(gen.Atom("Synchronizer"), Synchronize{Once: true}); err != nil {
		slog.Error(fmt.Sprintf("Failed to send message to Synchronizer: %v", err))
		return err
	}

	return nil
}

func (m *Metastructure) ForceDiscovery() error {
	if err := m.Node.Send(gen.Atom("Discovery"), discovery.Discover{Once: true}); err != nil {
		slog.Error(fmt.Sprintf("Failed to send message to Discovery: %v", err))
		return err
	}

	return nil
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
		// - Success: Keep as Success (completed, don't re-execute)
		// - Failed: Keep as Failed (completed with error, don't re-execute)
		// - InProgress: Reset to NotStarted (was interrupted, needs retry)
		// - NotStarted: Keep as NotStarted (never started, needs execution)
		for i := range fa.ResourceUpdates {
			fa.ResourceUpdates[i].UpdateState()
			// InProgress means the operation was in progress when the agent crashed/restarted.
			// We need to reset it to NotStarted so the changeset executor will re-queue it.
			if fa.ResourceUpdates[i].State == resource_update.ResourceUpdateStateInProgress {
				fa.ResourceUpdates[i].State = resource_update.ResourceUpdateStateNotStarted
			}
		}

		var pendingTargetUpdates []target_update.TargetUpdate
		for _, tu := range fa.TargetUpdates {
			if tu.State == target_update.TargetUpdateStateNotStarted {
				pendingTargetUpdates = append(pendingTargetUpdates, tu)
			}
		}

		if len(pendingTargetUpdates) > 0 {
			_, err = m.callActor(
				gen.ProcessID{Name: actornames.ResourcePersister, Node: m.Node.Name()},
				target_update.PersistTargetUpdates{
					TargetUpdates: pendingTargetUpdates,
					CommandID:     fa.ID,
				},
			)
			if err != nil {
				slog.Error("Failed to recover target updates for incomplete forma command", "commandID", fa.ID, "error", err)
				// Continue with other commands even if this one fails
				continue
			}
			m.Node.Log().Debug("Successfully recovered target updates", "commandID", fa.ID, "count", len(pendingTargetUpdates))

			// Update the forma command with the recovered target states
			_, err = m.callActor(
				gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()},
				messages.UpdateTargetStates{
					CommandID:     fa.ID,
					TargetUpdates: pendingTargetUpdates,
				},
			)
			if err != nil {
				slog.Error("Failed to update forma command with recovered target states", "commandID", fa.ID, "error", err)
				// Continue with other commands even if this one fails
				continue
			}
		}

		// This changeset was validated upon the initial apply so we can safely ignore the error.
		cs, _ := changeset.NewChangesetFromResourceUpdates(fa.ResourceUpdates, fa.ID, pkgmodel.CommandApply)

		m.Node.Log().Debug("Starting ChangesetExecutor of changeset from incomplete forma command", "commandID", fa.ID)
		_, err = m.callActor(
			gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: m.Node.Name()},
			changeset.EnsureChangesetExecutor{CommandID: fa.ID},
		)
		if err != nil {
			slog.Error("Failed to ensure ChangesetExecutor for incomplete forma command", "command", fa.Command, "forma", fa, "error", err)
			return err
		}

		m.Node.Log().Debug("Sending Start message to ChangesetExecutor", "commandID", fa.ID)
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

func (m *Metastructure) checkForConflictingCommands(command *forma_command.FormaCommand) error {
	incompleteFormaCommands, err := m.Datastore.LoadIncompleteFormaCommands()
	if err != nil {
		slog.Error("Failed to load incomplete forma commands", "error", err)
		return fmt.Errorf("failed to load incomplete forma commands: %w", err)
	}

	// Group incomplete resource commands by forma command ID
	incompleteResourceUpdates := make(map[string][]resource_update.ResourceUpdate)
	commandStackLabels := command.GetStackLabels()
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

func FormaCommandFromForma(forma *pkgmodel.Forma,
	formaCommandConfig *config.FormaCommandConfig,
	command pkgmodel.Command,
	ds datastore.Datastore,
	clientID string,
	source resource_update.FormaCommandSource) (*forma_command.FormaCommand, error) {

	if formaCommandConfig.Mode == "" {
		formaCommandConfig.Mode = pkgmodel.FormaApplyModePatch
	}

	existingTargets, err := ds.LoadAllTargets()
	if err != nil {
		return nil, fmt.Errorf("failed to load targets: %w", err)
	}

	resourceUpdates, err := resource_update.GenerateResourceUpdates(forma, command, formaCommandConfig.Mode, source, existingTargets, ds)
	if err != nil {
		if requiredFieldsErr, ok := err.(apimodel.RequiredFieldMissingOnCreateError); ok {
			return nil, requiredFieldsErr
		}
		if targetExistsErr, ok := err.(apimodel.TargetAlreadyExistsError); ok {
			return nil, targetExistsErr
		}
		return nil, fmt.Errorf("failed to generate resource updates: %w", err)
	}

	targetUpdates, err := target_update.NewTargetUpdateGenerator(ds).GenerateTargetUpdates(forma.Targets, command, len(forma.Resources) > 0)
	if err != nil {
		return nil, err
	}

	stackUpdates, err := stack_update.NewStackUpdateGenerator(ds).GenerateStackUpdates(forma.Stacks, command)
	if err != nil {
		return nil, err
	}

	return forma_command.NewFormaCommand(
		forma,
		formaCommandConfig,
		command,
		resourceUpdates,
		targetUpdates,
		stackUpdates,
		clientID,
	), nil
}

func (m *Metastructure) Stats() (*apimodel.Stats, error) {
	stats, err := m.Datastore.Stats()
	if err != nil {
		return nil, fmt.Errorf("failed to get stats from datastore: %w", err)
	}

	// Get registered plugins from PluginCoordinator
	var plugins []apimodel.PluginInfo
	result, err := m.callActor(gen.ProcessID{Name: actornames.PluginCoordinator, Node: m.Node.Name()}, messages.GetRegisteredPlugins{})
	if err == nil {
		if pluginsResult, ok := result.(messages.GetRegisteredPluginsResult); ok {
			for _, p := range pluginsResult.Plugins {
				plugins = append(plugins, apimodel.PluginInfo{
					Namespace:            p.Namespace,
					Version:              p.Version,
					NodeName:             p.NodeName,
					MaxRequestsPerSecond: p.MaxRequestsPerSecond,
					ResourceCount:        p.ResourceCount,
				})
			}
		}
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
		ResourceErrors:     stats.ResourceErrors,
		Plugins:            plugins,
	}, nil
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
						return map[string]any{
							"$res":      true,
							"$label":    triplet.Label,
							"$type":     triplet.Type,
							"$stack":    triplet.Stack,
							"$property": formaeUri.PropertyPath(),
							"$value":    dollarValue,
						}
					}
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
