// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/policy_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type guardedApplyPlan struct {
	Command   *forma_command.FormaCommand
	Changeset changeset.Changeset
	Response  *apimodel.SubmitCommandResponse
	Guards    []datastore.RevisionGuard
}

// ownPlanningValue preserves internal model identity and exact numbers. JSON
// cloning would omit generator IDs and normalize schema hints during copying.
func ownPlanningValue[T any](in T) T { return clonePlanningValue(reflect.ValueOf(in)).Interface().(T) }
func clonePlanningValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(clonePlanningValue(v.Elem()))
		return out
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(clonePlanningValue(v.Elem()))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(clonePlanningValue(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		it := v.MapRange()
		for it.Next() {
			out.SetMapIndex(it.Key(), clonePlanningValue(it.Value()))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(clonePlanningValue(v.Index(i)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			if out.Field(i).CanSet() && v.Type().Field(i).IsExported() {
				out.Field(i).Set(clonePlanningValue(v.Field(i)))
			}
		}
		return out
	default:
		return v
	}
}

func (m *Metastructure) ApplyForma(forma *pkgmodel.Forma, options *config.FormaCommandConfig, clientID, subject, subjectName string) (*apimodel.SubmitCommandResponse, error) {
	// Existing clients have no final-preview token or durable caller retry key.
	key := util.NewID()
	if options != nil && options.Resolution != nil {
		r := options.Resolution
		if !options.Simulate {
			if r.ReviewID == "" {
				return nil, resolutionError("review-required", "a final ReviewID is required", "")
			}
			if r.IdempotencyKey == "" || len(r.IdempotencyKey) > 200 || strings.TrimSpace(r.IdempotencyKey) != r.IdempotencyKey || strings.ContainsRune(r.IdempotencyKey, 0) || !utf8.ValidString(r.IdempotencyKey) {
				return nil, resolutionError("invalid-resolution", "IdempotencyKey must be 1..200 UTF-8 bytes without surrounding whitespace or NUL", "")
			}
			key = r.IdempotencyKey
		}
	}
	return m.applyFormaWithKey(forma, options, clientID, subject, subjectName, key)
}

// applyFormaWithKey shares guarded admission between legacy applies and public
// reviewed resolutions. ApplyForma validates the public retry/review controls.
func (m *Metastructure) applyFormaWithKey(forma *pkgmodel.Forma, options *config.FormaCommandConfig, clientID, subject, subjectName, key string) (*apimodel.SubmitCommandResponse, error) {
	if forma == nil || options == nil {
		return nil, fmt.Errorf("forma and options are required")
	}
	original := ownPlanningValue(forma)
	original.Extraction = nil
	opts := ownPlanningValue(options)
	m.commandMu.Lock()
	defer m.commandMu.Unlock()
	// Scope is the authenticated stable subject; empty subject is explicitly the
	// datastore's unattributed namespace. Client-ID and display name confer no identity.
	principalBytes, _ := json.Marshal([]string{"formae-apply-v1", subject})
	principalSum := sha256.Sum256(principalBytes)
	principal := "subject:" + hex.EncodeToString(principalSum[:])
	identityOptions := ownPlanningValue(opts)
	if identityOptions.Resolution != nil {
		identityOptions.Resolution.IdempotencyKey = ""
		sort.Slice(identityOptions.Resolution.Decisions, func(i, j int) bool {
			return identityOptions.Resolution.Decisions[i].ResourceID < identityOptions.Resolution.Decisions[j].ResourceID
		})
	}
	digest, err := resolutionHash(struct {
		Forma   *pkgmodel.Forma
		Options *config.FormaCommandConfig
		Message string
	}{original, identityOptions, opts.Message})
	if err != nil {
		return nil, err
	}
	admitter, ok := m.Datastore.(datastore.CommandAdmitter)
	if !ok {
		return nil, fmt.Errorf("datastore lacks guarded admission")
	}
	if !opts.Simulate {
		prior, err := admitter.LookupCommandAdmission(principal, key)
		if err != nil {
			return nil, err
		}
		if prior != nil {
			if prior.RequestDigest != digest {
				return nil, datastore.ErrAdmissionConflict
			}
			return m.replayAdmittedApply(*prior)
		}
	}
	plan, err := m.prepareGuardedApply(original, opts, clientID, subject, subjectName)
	if err != nil {
		return nil, err
	}
	if opts.Simulate || !plan.Command.HasChanges() {
		return plan.Response, nil
	}
	// Receipt contains only durable response identity. Rendering the committed
	// command is separate and does not risk storing secrets in the receipt.
	receipt, err := json.Marshal(struct {
		CommandID string
	}{plan.Command.ID})
	if err != nil {
		return nil, err
	}
	admission := datastore.CommandAdmission{Guards: plan.Guards, PrincipalScope: principal, IdempotencyKey: key, RequestDigest: digest, Receipt: receipt}
	if plan.Command.HasExecutableChanges() {
		if len(m.pendingApplyDispatch) >= 4096 {
			return nil, fmt.Errorf("too many unresolved admitted dispatches; recover pending commands before retrying")
		}
		if m.pendingApplyDispatch == nil {
			m.pendingApplyDispatch = map[string]*changeset.Changeset{}
		}
		m.pendingApplyDispatch[plan.Command.ID] = &plan.Changeset
	}
	result, err := m.callActor(gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()}, forma_persister.StoreNewFormaCommand{Command: *plan.Command, Admission: &admission})
	if err != nil {
		if errors.Is(err, datastore.ErrStaleAdmission) || errors.Is(err, datastore.ErrAdmissionConflict) || errors.Is(err, datastore.ErrInvalidAdmission) {
			delete(m.pendingApplyDispatch, plan.Command.ID)
		}
		return nil, err
	}
	persisted, ok := result.(forma_persister.CommandPersistResult)
	if !ok || persisted.Admission == nil {
		return nil, fmt.Errorf("missing admission result")
	}
	if persisted.Admission.Replayed {
		delete(m.pendingApplyDispatch, plan.Command.ID)
		return m.replayAdmittedApply(persisted.Admission.StoredAdmission)
	}
	committed := persisted.Admission.Command
	if committed == nil {
		return nil, fmt.Errorf("missing committed command")
	}
	if err = m.dispatchAdmittedApply(committed, &plan.Changeset); err != nil {
		return nil, err
	}
	return responseForAdmittedApply(committed), nil
}

func (m *Metastructure) prepareGuardedApply(original *pkgmodel.Forma, options *config.FormaCommandConfig, clientID, subject, subjectName string) (*guardedApplyPlan, error) {
	original = ownPlanningValue(original)
	original.Extraction = nil // Read-side rendering context is never planning authority.
	options = ownPlanningValue(options)
	for i := range original.Targets {
		original.Targets[i].ExecutionIncarnation = ""
	}
	scope := newPlanningDatastore(m.Datastore, original)
	for attempt := 0; attempt < 16; attempt++ {
		var candidate *guardedApplyPlan
		guards, err := scope.certify(func() error {
			var err error
			candidate, err = m.planApplyForma(scope, ownPlanningValue(original), ownPlanningValue(options), clientID, subject, subjectName)
			if err != nil {
				return err
			}
			for i := range candidate.Command.ResourceUpdates {
				ru := &candidate.Command.ResourceUpdates[i]
				ru.ResourceTarget.ExecutionIncarnation = ""
				if ru.ResourceTarget.Health != nil {
					ru.ResourceTarget.ExecutionIncarnation = ru.ResourceTarget.Health.IncarnationID
				}
			}
			for _, label := range candidate.Command.GetStackLabels() {
				if _, err = scope.GetStackByLabel(label); err != nil {
					return err
				}
			}
			if !options.Simulate {
				if err = m.checkForConflictingCommands(candidate.Command.GetStackLabels()); err != nil {
					return err
				}
			}
			if err = pinPlannedPolicyIdentities(scope, candidate.Command); err != nil {
				return err
			}
			return pinPlannedGeneratorIdentities(scope, candidate.Command)
		})
		if errors.Is(err, errPlanningScopeExpanded) {
			continue
		}
		if err != nil {
			return nil, err
		}
		candidate.Guards = guards
		if err := bindFinalResolution(candidate, original, options); err != nil {
			return nil, err
		}
		return candidate, nil
	}
	return nil, fmt.Errorf("%w: planning scope did not stabilize after 16 complete attempts", datastore.ErrStaleAdmission)
}
func pinPlannedPolicyIdentities(scope *planningDatastore, command *forma_command.FormaCommand) error {
	reader, ok := scope.Datastore.(datastore.PolicyIdentityReader)
	if !ok {
		return fmt.Errorf("datastore lacks policy identity reader")
	}
	for i := range command.PolicyUpdates {
		update := &command.PolicyUpdates[i]
		if update.Operation == policy_update.PolicyOperationCreate || update.Operation == policy_update.PolicyOperationSkip {
			continue
		}
		label := update.PolicyRef
		stackID := ""
		if update.Policy != nil {
			label = update.Policy.GetLabel()
		}
		if update.StackLabel != "" && update.PolicyRef == "" {
			stack, err := scope.GetStackByLabel(update.StackLabel)
			if err != nil {
				return err
			}
			if stack != nil {
				stackID = stack.ID
			}
		}
		identity, err := reader.ReadPolicyIdentity(label, stackID)
		if err != nil {
			return err
		}
		if identity == nil {
			if update.Operation == policy_update.PolicyOperationAttach {
				continue
			}
			return fmt.Errorf("policy %q disappeared during planning", label)
		}
		update.PolicyID = identity.ID
		update.StackID = identity.StackID
		update.ExpectedVersion = identity.Version
	}
	return nil
}
func responseForAdmittedApply(command *forma_command.FormaCommand) *apimodel.SubmitCommandResponse {
	return &apimodel.SubmitCommandResponse{Review: ownPlanningValue(command.Resolution), CommandID: command.ID, Description: apimodel.Description(command.Description), Simulation: apimodel.Simulation{ChangesRequired: command.HasChanges(), Command: translateToAPICommand(command)}}
}
func (m *Metastructure) replayAdmittedApply(receipt datastore.StoredAdmission) (*apimodel.SubmitCommandResponse, error) {
	command, err := m.Datastore.GetFormaCommandByCommandID(receipt.CommandID)
	if err != nil {
		return nil, err
	}
	if command == nil {
		return nil, fmt.Errorf("admitted command %s is unavailable", receipt.CommandID)
	}
	if m.Node != nil && (len(command.StackUpdates) > 0 || len(command.PolicyUpdates) > 0) {
		if err = m.Node.Send(gen.ProcessID{Name: actornames.AutoReconciler, Node: m.Node.Name()}, messages.RefreshEffectivePolicies{}); err != nil {
			return nil, AdmittedCommandRecoveryError{command.ID, err}
		}
	}
	if err = m.dispatchAdmittedApply(command, nil); err != nil {
		return nil, err
	}
	return responseForAdmittedApply(command), nil
}

// AdmittedCommandRecoveryError carries the original durable identity even when
// execution ownership cannot safely be established in this live process.
type AdmittedCommandRecoveryError struct {
	CommandID string
	Cause     error
}

func (e AdmittedCommandRecoveryError) Error() string {
	return fmt.Sprintf("admitted command %s requires execution recovery: %v", e.CommandID, e.Cause)
}
func (e AdmittedCommandRecoveryError) Unwrap() error { return e.Cause }
func (m *Metastructure) dispatchAdmittedApply(command *forma_command.FormaCommand, planned *changeset.Changeset) error {
	if command.IsInFinalState() {
		delete(m.pendingApplyDispatch, command.ID)
		return nil
	}
	recoverErr := func(err error) error { return AdmittedCommandRecoveryError{command.ID, err} }
	if err := command.CheckSetupRecovery(); err != nil {
		return recoverErr(err)
	}
	destination := gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: m.Node.Name()}
	reply, err := m.callActor(destination, changeset.DispatchAdmittedChangeset{CommandID: command.ID})
	if err != nil {
		return recoverErr(err)
	}
	state, ok := reply.(changeset.AdmittedDispatchResult)
	if !ok {
		return recoverErr(fmt.Errorf("missing dispatch ownership result"))
	}
	if state.Owned {
		delete(m.pendingApplyDispatch, command.ID)
		return nil
	}
	if planned == nil {
		planned = m.pendingApplyDispatch[command.ID]
	}
	if planned == nil {
		planned, err = m.prepareIncompleteChangeset(command)
		if err != nil {
			return recoverErr(err)
		}
		if planned == nil {
			_, err = m.callActor(gen.ProcessID{Name: actornames.FormaCommandPersister, Node: m.Node.Name()}, forma_persister.FinalizeIncompleteCommand{CommandID: command.ID})
			if err != nil {
				return recoverErr(err)
			}
			return nil
		}
	}

	_, err = m.callActor(destination, changeset.DispatchAdmittedChangeset{CommandID: command.ID, Changeset: planned})
	if err != nil {
		return recoverErr(err)
	}
	delete(m.pendingApplyDispatch, command.ID)
	return nil
}

func pinPlannedGeneratorIdentities(scope *planningDatastore, command *forma_command.FormaCommand) error {
	for i := range command.GeneratorUpdates {
		update := &command.GeneratorUpdates[i]
		g := update.Generator
		if g == nil {
			return fmt.Errorf("generator required")
		}
		stack, err := scope.GetStackByLabel(update.StackLabel)
		if err != nil {
			return err
		}
		if stack != nil {
			g.SetStackID(stack.ID)
		}
		if update.Operation == generator_update.GeneratorOperationCreate {
			continue
		}
		if stack == nil {
			return fmt.Errorf("generator stack disappeared during planning")
		}
		label := g.GetLabel()
		if update.ExistingGenerator != nil {
			label = update.ExistingGenerator.GetLabel()
		}
		identity, err := scope.GetGeneratorIdentity(label, update.StackLabel)
		if err != nil {
			return err
		}
		if identity.ID == "" || (g.GetID() != "" && g.GetID() != identity.ID) {
			return fmt.Errorf("%w: generator identity changed during planning", datastore.ErrStaleAdmission)
		}
		g.SetID(identity.ID)
		if update.ExistingGenerator != nil {
			update.ExistingGenerator.SetID(identity.ID)
			update.ExistingGenerator.SetStackID(stack.ID)
		}
	}
	return nil
}

// refreshResponse retains planner diagnostics while updating composed command data.
func (p *guardedApplyPlan) refreshResponse() {
	var warnings []string
	if p.Response != nil {
		warnings = p.Response.Simulation.Warnings
	}
	p.Response = responseForAdmittedApply(p.Command)
	p.Response.Simulation.Warnings = warnings
}
