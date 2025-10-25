// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"ergo.services/application/observer"
	"ergo.services/ergo"
	"ergo.services/ergo/gen"
	"ergo.services/ergo/lib"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/platform-engineering-labs/formae"
	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/discovery"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/querier"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
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
	ListFormaCommandStatus(query string, clientID string, n int) (*apimodel.ListCommandStatusResponse, error)
	ExtractResources(query string) (*pkgmodel.Forma, error)
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

	if cfg.Agent.Datastore.DatastoreType == pkgmodel.PostgresDatastore {
		ds, err = datastore.NewDatastorePostgres(ctx, &cfg.Agent.Datastore, agentID)
	} else {
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

	err := initDelegateMessageTypes()
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
		gen.Env("AgentID"):               agentID,
	}

	metastructure.options.Network.Mode = gen.NetworkModeDisabled

	//FIXME(discount-elf): enable real TLS if we want it
	//cert, _ := lib.GenerateSelfSignedCert("formae node")
	//metastructure.options.CertManager = gen.CreateCertManager(cert)

	if cfg.Agent.Server.Secret == "" {
		metastructure.options.Network.Cookie = lib.RandomString(16)
		slog.Warn("No secret provided, using random secret, nodes will not be able to communicate", "secret", metastructure.options.Network.Cookie)
	} else {
		metastructure.options.Network.Cookie = cfg.Agent.Server.Secret
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

	// Short-circuit if there are no resource updates
	if len(fa.ResourceUpdates) == 0 {
		return &apimodel.SubmitCommandResponse{
			CommandID:   fa.ID,
			Description: apimodel.Description(fa.Forma.Description),
			Simulation: apimodel.Simulation{
				ChangesRequired: false,
				Command:         apimodel.Command{},
			},
		}, nil
	}

	// Ensure we can successfully create a changeset from the resource updates
	cs, err := changeset.NewChangesetFromResourceUpdates(fa.ResourceUpdates, fa.ID, pkgmodel.CommandApply)
	if err != nil {
		return nil, err
	}

	if config.Mode == pkgmodel.FormaApplyModeReconcile && !config.Force {
		var modifiedStacks = make(map[string]apimodel.ModifiedStack)
		for _, stack := range fa.Forma.Stacks {
			modifications, loadErr := m.Datastore.GetResourceModificationsSinceLastReconcile(stack.Label)
			if loadErr != nil {
				slog.Error("Failed to load most recent non-reconcile forma commands by stack", "stack", stack.Label, "error", err)
				return nil, fmt.Errorf("failed to load most recent forma commands for stack %s: %w", stack.Label, err)
			}
			if len(modifications) > 0 {
				modifiedResources := make([]apimodel.ResourceModification, 0, len(modifications))
				for _, modification := range modifications {
					modifiedResources = append(modifiedResources, apimodel.ResourceModification(modification))
				}
				modifiedStacks[stack.Label] = apimodel.ModifiedStack{
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
			Description: apimodel.Description(fa.Forma.Description),
			Simulation: apimodel.Simulation{
				ChangesRequired: true,
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

	return &apimodel.SubmitCommandResponse{CommandID: fa.ID, Description: apimodel.Description(fa.Forma.Description)}, nil
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
			ResourceID:     ru.Resource.Ksuid,
			ResourceType:   ru.Resource.Type,
			ResourceLabel:  ru.Resource.Label,
			StackName:      ru.StackLabel,
			OldStackName:   ru.ExistingResource.Stack,
			Properties:     ru.Resource.Properties,
			OldProperties:  ru.PreviousProperties,
			PatchDocument:  ru.Resource.PatchDocument,
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

	return apiCommand
}

func (m *Metastructure) DestroyForma(forma *pkgmodel.Forma, config *config.FormaCommandConfig, clientID string) (*apimodel.SubmitCommandResponse, error) {
	fa, err := FormaCommandFromForma(forma, config, pkgmodel.CommandDestroy, m.Datastore, clientID, resource_update.FormaCommandSourceUser)
	if err != nil {
		slog.Error("Failed to create destroy from forma", "error", err)
		return nil, err
	}

	// Short-circuit if there are no resource updates
	if len(fa.ResourceUpdates) == 0 {
		return &apimodel.SubmitCommandResponse{
			CommandID:   fa.ID,
			Description: apimodel.Description(fa.Forma.Description),
			Simulation: apimodel.Simulation{
				ChangesRequired: false,
				Command:         apimodel.Command{},
			},
		}, nil
	}

	if config.Simulate {
		return &apimodel.SubmitCommandResponse{
			CommandID:   fa.ID,
			Description: apimodel.Description(fa.Forma.Description),
			Simulation: apimodel.Simulation{
				ChangesRequired: len(fa.ResourceUpdates) > 0,
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

	return &apimodel.SubmitCommandResponse{
		CommandID:   fa.ID,
		Description: apimodel.Description(fa.Forma.Description),
		Simulation: apimodel.Simulation{
			ChangesRequired: true,
			Command:         translateToAPICommand(fa),
		},
	}, nil
}

func (m *Metastructure) DestroyByQuery(query string, config *config.FormaCommandConfig, clientID string) (*apimodel.SubmitCommandResponse, error) {
	q := querier.NewBlugeQuerier(m.Datastore)
	resources, err := q.QueryResources(query)
	if err != nil {
		slog.Debug("Cannot get resources from query", "error", err)
		return nil, err
	}

	// Only managed resources can be destroyed
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
		// Reset state to not started
		for i := range fa.ResourceUpdates {
			fa.ResourceUpdates[i].State = resource_update.ResourceUpdateStateNotStarted
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
	for _, incompleteFormaCommand := range incompleteFormaCommands {
		if formaTouchesStacks(incompleteFormaCommand, command.Forma.Stacks) {
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

func formaTouchesStacks(forma *forma_command.FormaCommand, stacks []pkgmodel.Stack) bool {
	for _, stackTouchedByIncompleteCommand := range forma.Forma.Stacks {
		for _, stack := range stacks {
			if stackTouchedByIncompleteCommand.Label == stack.Label {
				return true
			}
		}
	}

	return false
}

func (m *Metastructure) checkIfPatchCanBeApplied(command *forma_command.FormaCommand) error {
	knownStacks, err := m.Datastore.LoadAllStacks()
	if err != nil {
		slog.Error("Failed to load all stacks", "error", err)
		return fmt.Errorf("failed to load all stacks: %w", err)
	}

	for _, stack := range command.Forma.Stacks {
		var knownStack *pkgmodel.Forma
		for _, s := range knownStacks {
			if stack.Label == s.SingleStackLabel() {
				knownStack = s
				break
			}
		}

		if knownStack == nil {
			return apimodel.FormaPatchRejectedError{}
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

	resourceUpdates, err := resource_update.GenerateResourceUpdates(forma, command, formaCommandConfig.Mode, source, existingTargets, ds, nil)
	if err != nil {
		if requiredFieldsErr, ok := err.(apimodel.RequiredFieldMissingOnCreateError); ok {
			return nil, requiredFieldsErr
		}
		if targetExistsErr, ok := err.(apimodel.TargetAlreadyExistsError); ok {
			return nil, targetExistsErr
		}
		return nil, fmt.Errorf("failed to generate resource updates: %w", err)
	}

	return forma_command.NewFormaCommand(
		forma,
		formaCommandConfig,
		command,
		resourceUpdates,
		clientID,
	), nil
}

func (m *Metastructure) Stats() (*apimodel.Stats, error) {
	stats, err := m.Datastore.Stats()
	if err != nil {
		return nil, fmt.Errorf("failed to get stats from datastore: %w", err)
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
				// Handle arrays like SubnetIds: ["formae://...", "formae://..."]
				value.ForEach(func(_, item gjson.Result) bool {
					if item.Type == gjson.String {
						if ksuid := pkgmodel.FormaeURI(item.String()).KSUID(); ksuid != "" {
							ksuidSet[ksuid] = struct{}{}
						}
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

func replaceKSUIDs(jsonStr string, ksuidToTriplet map[string]pkgmodel.TripletKey) string {
	result := gjson.Parse(jsonStr)
	modified := jsonStr

	result.ForEach(func(key, value gjson.Result) bool {
		switch value.Type {
		case gjson.String:
			formaeUri := pkgmodel.FormaeURI(value.String())
			if ksuid := formaeUri.KSUID(); ksuid != "" {
				if triplet, ok := ksuidToTriplet[ksuid]; ok {
					resolvableObj := map[string]any{
						"$res":      true,
						"$label":    triplet.Label,
						"$type":     triplet.Type,
						"$stack":    triplet.Stack,
						"$property": formaeUri.PropertyPath(),
						"$value":    value.Get("$value").String(),
					}
					modified, _ = sjson.Set(modified, key.String(), resolvableObj)
				}
			}
		case gjson.JSON:
			if value.IsArray() {
				value.ForEach(func(index, item gjson.Result) bool {
					if item.Type == gjson.String {
						formaeUri := pkgmodel.FormaeURI(item.String())
						if ksuid := formaeUri.KSUID(); ksuid != "" {
							if triplet, ok := ksuidToTriplet[ksuid]; ok {
								resolvableObj := map[string]any{
									"$res":      true,
									"$label":    triplet.Label,
									"$type":     triplet.Type,
									"$stack":    triplet.Stack,
									"$property": formaeUri.PropertyPath(),
									"$value":    value.Get("$value").String(),
								}
								path := key.String() + "." + index.String()
								modified, _ = sjson.Set(modified, path, resolvableObj)
							}
						}
					}
					return true
				})
			} else if value.IsObject() {
				// Check if this is a $ref object that needs to be converted
				if refValue := value.Get("$ref"); refValue.Exists() && refValue.Type == gjson.String {
					formaeUri := pkgmodel.FormaeURI(refValue.String())
					if ksuid := formaeUri.KSUID(); ksuid != "" {
						if triplet, ok := ksuidToTriplet[ksuid]; ok {
							resolvableObj := map[string]any{
								"$res":      true,
								"$label":    triplet.Label,
								"$type":     triplet.Type,
								"$stack":    triplet.Stack,
								"$property": formaeUri.PropertyPath(),
								"$value":    value.Get("$value").String(),
							}
							modified, _ = sjson.Set(modified, key.String(), resolvableObj)
						}
					}
				} else {
					// Handle nested objects recursively
					nestedReplaced := replaceKSUIDs(value.Raw, ksuidToTriplet)
					modified, _ = sjson.SetRaw(modified, key.String(), nestedReplaced)
				}
			}
		}
		return true
	})

	return modified
}
