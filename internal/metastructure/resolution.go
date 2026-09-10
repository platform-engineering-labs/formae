// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/patch"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/drift"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func resolutionError(code, reason, id string) error {
	return apimodel.DriftResolutionError{Code: code, Reason: reason, ResourceID: id}
}
func resolutionHash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	// RawMessage values (properties/config) retain object key order. Decode
	// once so semantically identical JSON object ordering shares a review.
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	var canonical any
	if err := decoder.Decode(&canonical); err != nil {
		return "", err
	}
	b, err = json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func infrastructureOptions(options *config.FormaCommandConfig) config.FormaCommandConfig {
	o := *options
	o.Message = ""
	o.Simulate = false
	o.Resolution = nil
	return o
}

// Pin each displayed item to the latest physical observation. Historical
// modification operations cannot prove current cloud deletion.
func bindDriftObservation(ds datastore.Datastore, forma *pkgmodel.Forma, options *config.FormaCommandConfig, rejected apimodel.FormaReconcileRejectedError) (apimodel.FormaReconcileRejectedError, []pkgmodel.DriftObservation, error) {
	reader, ok := ds.(datastore.ResourceObservationReader)
	if !ok {
		return rejected, nil, fmt.Errorf("datastore lacks protected observations")
	}
	var observations []pkgmodel.DriftObservation
	baselines := map[string][]datastore.ResourceSnapshot{}
	for label, stack := range rejected.ModifiedStacks {
		baseline, err := ds.GetResourcesAtLastReconcile(label)
		if err != nil {
			return rejected, nil, err
		}
		baselines[label] = baseline
		current, err := ds.GetStackByLabel(label)
		if err != nil {
			return rejected, nil, err
		}
		if current == nil {
			return rejected, nil, datastore.ErrStaleAdmission
		}
		seen := map[string]bool{}
		var mods []apimodel.ResourceModification
		for _, mod := range stack.ModifiedResources {
			id := mod.ResourceID
			if id == "" {
				id, err = ds.GetKSUIDByTriplet(mod.Stack, mod.Label, mod.Type)
				if err != nil {
					return rejected, nil, err
				}
			}
			if id == "" {
				return rejected, nil, resolutionError("resolution-unavailable", "drift has no stable resource identity", id)
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			obs, err := reader.GetResourceObservation(id)
			if err != nil {
				return rejected, nil, err
			}
			if obs == nil || obs.Version == "" || obs.StackID != current.ID {
				return rejected, nil, resolutionError("resolution-unavailable", "observation has no matching stack incarnation", id)
			}
			kind := mod.Operation
			if obs.Operation == "delete" {
				if !obs.ConfirmedDeletion {
					return rejected, nil, resolutionError("resolution-unavailable", "missing resource is not a confirmed cloud deletion", id)
				}
				kind = "delete"
			} else if obs.Operation != "create" && obs.Operation != "update" {
				return rejected, nil, resolutionError("resolution-unavailable", "observation is not live or a confirmed deletion", id)
			} else if kind == "delete" {
				kind = "update"
			}
			var commandID string
			for _, b := range baseline {
				if b.KSUID == id {
					commandID = b.CommandID
					break
				}
			}
			if commandID == "" && kind != "delete" {
				kind = "create"
			}
			mod.ObservedCommandID = obs.CommandID
			// Origin is display metadata from the exact observed command. Old
			// observations may outlive that history; leave their origin absent.
			if obs.CommandID != "" {
				observedCommand, loadErr := ds.GetFormaCommandByCommandID(obs.CommandID)
				if loadErr == nil && observedCommand != nil {
					mod.ObservedCommand = observedCommand.Command
					mod.ObservedMode = observedCommand.Config.Mode
					mod.ObservedSource = string(observedCommand.Source)
				}
			}
			mod.ResourceID = id
			mod.ObservedVersion = obs.Version
			mod.StackID = current.ID
			mod.Operation = kind
			mods = append(mods, mod)
			observations = append(observations, pkgmodel.DriftObservation{ResourceID: id, StackID: current.ID, Stack: label, Type: mod.Type, Label: mod.Label, Kind: kind, ObservedVersion: obs.Version, ObservedCommandID: obs.CommandID, BaselineCommandID: commandID})
		}
		sort.Slice(mods, func(i, j int) bool { return mods[i].ResourceID < mods[j].ResourceID })
		stack.ModifiedResources = mods
		rejected.ModifiedStacks[label] = stack
		sort.Slice(baselines[label], func(i, j int) bool { return baselines[label][i].KSUID < baselines[label][j].KSUID })
	}
	sort.Slice(observations, func(i, j int) bool { return observations[i].ResourceID < observations[j].ResourceID })
	rejected.ObservationID, _ = resolutionHash(struct {
		Forma        *pkgmodel.Forma
		Options      config.FormaCommandConfig
		Observations []pkgmodel.DriftObservation
		Baselines    map[string][]datastore.ResourceSnapshot
	}{forma, infrastructureOptions(options), observations, baselines})
	return rejected, observations, nil
}

func (m *Metastructure) planResolution(ds datastore.Datastore, input *pkgmodel.Forma, options *config.FormaCommandConfig, clientID, subject, subjectName string) (*guardedApplyPlan, error) {
	controls := options.Resolution
	if options.Force || (options.Mode != "" && options.Mode != pkgmodel.FormaApplyModeReconcile) {
		return nil, resolutionError("invalid-resolution", "resolution requires soft reconcile", "")
	}
	if controls.ObservationID == "" {
		return nil, resolutionError("invalid-resolution", "ObservationID is required", "")
	}
	plain := ownPlanningValue(options)
	plain.Resolution = nil
	plain.Simulate = true
	_, err := m.planApplyFormaCore(ds, ownPlanningValue(input), plain, clientID, subject, subjectName, nil)
	var rejected apimodel.FormaReconcileRejectedError
	if !errors.As(err, &rejected) {
		if err != nil {
			return nil, err
		}
		return nil, resolutionError("stale-review", "the observed drift is no longer actionable", "")
	}
	if rejected.ObservationID != controls.ObservationID {
		return nil, resolutionError("stale-review", "observation or declaration changed; obtain a new drift review", "")
	}
	_, observations, err := bindDriftObservation(ds, ownPlanningValue(input), plain, rejected)
	if err != nil {
		return nil, err
	}
	choices := map[string]string{}
	known := map[string]bool{}
	for _, o := range observations {
		known[o.ResourceID] = true
	}
	for _, d := range controls.Decisions {
		if !known[d.ResourceID] || choices[d.ResourceID] != "" || (d.Action != "absorb" && d.Action != "revert") {
			return nil, resolutionError("invalid-decisions", "decisions must be unique known resource IDs with action absorb or revert", d.ResourceID)
		}
		choices[d.ResourceID] = d.Action
	}
	if len(choices) != len(known) {
		return nil, resolutionError("invalid-decisions", "every actionable resource requires a decision", "")
	}
	composed := ownPlanningValue(input)
	var deleted []resource_update.ResourceUpdate
	acceptedCandidates := map[string]pkgmodel.Resource{}
	for _, o := range observations {
		baseline, err := ds.GetResourcesAtLastReconcile(o.Stack)
		if err != nil {
			return nil, err
		}
		var prior *pkgmodel.Resource
		for _, b := range baseline {
			if b.KSUID == o.ResourceID {
				prior = ownPlanningValue(b.Declaration)
				break
			}
		}
		obs, err := ds.(datastore.ResourceObservationReader).GetResourceObservation(o.ResourceID)
		if err != nil {
			return nil, err
		}
		if obs == nil || obs.Version != o.ObservedVersion {
			return nil, datastore.ErrStaleAdmission
		}
		idx := -1
		for i, r := range composed.Resources {
			if r.Stack == o.Stack && r.Type == o.Type && (r.Label == o.Label || r.Alias == o.Label) {
				idx = i
				break
			}
		}
		var requested *pkgmodel.Resource
		if idx >= 0 {
			requested = &composed.Resources[idx]
		}
		action := choices[o.ResourceID]
		remove := (action == "absorb" && o.Kind == "delete") || (action == "revert" && o.Kind == "create")
		if remove {
			if requested != nil && prior != nil {
				equal, e := resolutionDeclarationEqual(*prior, *requested)
				if e != nil {
					return nil, e
				}
				if !equal {
					return nil, resolutionError("decision-edit-conflict", "resource edit conflicts with removing the resource", o.ResourceID)
				}
			} else if requested != nil && prior == nil && action == "revert" {
				return nil, resolutionError("decision-edit-conflict", "declaring this resource conflicts with reverting its creation", o.ResourceID)
			}
			if idx >= 0 {
				composed.Resources = append(composed.Resources[:idx], composed.Resources[idx+1:]...)
			}
			if action == "absorb" {
				if !obs.ConfirmedDeletion || prior == nil {
					return nil, resolutionError("resolution-unavailable", "deletion requires confirmed observation and previous desired declaration", o.ResourceID)
				}
				deleted = append(deleted, resource_update.ResourceUpdate{DesiredState: *prior, Operation: resource_update.OperationAcceptDelete, State: resource_update.ResourceUpdateStateSuccess, Version: o.ObservedVersion, StackLabel: o.Stack, Source: resource_update.FormaCommandSourceUser})
			}
			continue
		}
		var chosen pkgmodel.Resource
		if action == "revert" {
			if prior == nil {
				return nil, resolutionError("resolution-unavailable", "previous desired declaration is unavailable", o.ResourceID)
			}
			chosen = *ownPlanningValue(prior)
			witness, err := ds.GetPropertiesAtLastWrite(o.ResourceID)
			if err != nil {
				return nil, err
			}
			if witness != nil {
				chosen.Properties, err = patch.AssertWitnessedSuppressed(chosen.Properties, witness, chosen.Schema)
				if err != nil {
					return nil, err
				}
			}
		} else {
			if obs.Resource == nil {
				return nil, resolutionError("resolution-unavailable", "live observation is unavailable", o.ResourceID)
			}
			chosen = *ownPlanningValue(obs.Resource)
			if prior != nil {
				chosen = *ownPlanningValue(prior)
			}
			chosen.Properties, err = absorbDeclarationProperties(chosen, obs.Resource.Properties)
			if err != nil {
				return nil, resolutionError("resolution-input-required", err.Error(), o.ResourceID)
			}
		}
		if action == "absorb" {
			acceptedCandidates[o.ResourceID] = ownPlanningValue(chosen)
		}
		if requested == nil && prior != nil {
			return nil, resolutionError("decision-edit-conflict", "omitting the resource conflicts with preserving it", o.ResourceID)
		}
		if requested != nil && prior != nil {
			if action == "revert" {
				if err := rejectRevertEditConflict(ds, *prior, *requested, obs); err != nil {
					return nil, resolutionError("decision-edit-conflict", err.Error(), o.ResourceID)
				}
			}
			chosen, err = mergeResolutionDeclaration(*prior, *requested, chosen)
			if err != nil {
				return nil, resolutionError("decision-edit-conflict", err.Error(), o.ResourceID)
			}
		} else if requested != nil {
			equal, e := resolutionDeclarationEqual(chosen, *requested)
			if e != nil {
				return nil, e
			}
			if !equal {
				return nil, resolutionError("decision-edit-conflict", "declaration differs from absorbed creation", o.ResourceID)
			}
		}
		chosen.Ksuid = o.ResourceID
		chosen.Version = ""
		chosen.ReadOnlyProperties = nil
		chosen.PatchDocument = nil
		if idx >= 0 {
			composed.Resources[idx] = chosen
		} else {
			composed.Resources = append(composed.Resources, chosen)
		}
	}
	finalOptions := ownPlanningValue(options)
	finalOptions.Resolution = nil
	// Verify each absorbed declaration on its own before merging unrelated edits.
	// A preserved expression that still plans a provider write cannot represent
	// the accepted observation; silently reverting it would contradict absorb.
	checkForma := ownPlanningValue(composed)
	for i, r := range checkForma.Resources {
		if candidate, ok := acceptedCandidates[r.Ksuid]; ok {
			checkForma.Resources[i] = candidate
		}
	}
	checkPlan, err := m.planApplyFormaCore(ds, checkForma, finalOptions, clientID, subject, subjectName, choices)
	if err != nil {
		return nil, err
	}
	for _, u := range checkPlan.Command.ResourceUpdates {
		if _, ok := acceptedCandidates[u.DesiredState.Ksuid]; ok && !u.IsAcceptance() && !u.RecordOnly && !u.ConvergenceOnly() {
			return nil, resolutionError("resolution-input-required", "preserved declaration cannot represent this observation without a provider write; supply a valid declaration or revert", u.DesiredState.Ksuid)
		}
	}

	plan, err := m.planApplyFormaCore(ds, composed, finalOptions, clientID, subject, subjectName, choices)
	if err != nil {
		return nil, err
	}
	for _, deletion := range deleted {
		exists := false
		for _, u := range plan.Command.ResourceUpdates {
			if u.DesiredState.Ksuid == deletion.DesiredState.Ksuid {
				exists = true
			}
		}
		if !exists {
			plan.Command.ResourceUpdates = append(plan.Command.ResourceUpdates, deletion)
		}
	}
	// Ordinary contributions own the resulting intent when composition schedules
	// work. A metadata-only accepted creation also needs its first desired record.
	for _, o := range observations {
		if choices[o.ResourceID] != "absorb" || o.Kind == "delete" {
			continue
		}
		exists := false
		for _, u := range plan.Command.ResourceUpdates {
			if u.DesiredState.Ksuid == o.ResourceID {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		for _, r := range composed.Resources {
			if r.Ksuid == o.ResourceID {
				plan.Command.ResourceUpdates = append(plan.Command.ResourceUpdates, resource_update.ResourceUpdate{DesiredState: r, Operation: resource_update.OperationAccept, State: resource_update.ResourceUpdateStateSuccess, Version: o.ObservedVersion, StackLabel: o.Stack, Source: resource_update.FormaCommandSourceUser})
				break
			}
		}
	}
	decisions := append([]pkgmodel.DriftDecision(nil), controls.Decisions...)
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].ResourceID < decisions[j].ResourceID })
	plan.Command.Resolution = &pkgmodel.DriftReview{ObservationID: controls.ObservationID, Decisions: decisions, Observations: observations}
	plan.refreshResponse()
	return plan, nil
}

func decodeResolutionJSON(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	err := d.Decode(&v)
	return v, err
}
func resolutionJSONEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
func containsSymbolicNode(v any) bool {
	if symbolicNode(v) {
		return true
	}
	switch node := v.(type) {
	case map[string]any:
		for _, value := range node {
			if containsSymbolicNode(value) {
				return true
			}
		}
	case []any:
		for _, value := range node {
			if containsSymbolicNode(value) {
				return true
			}
		}
	}
	return false
}
func symbolicNode(v any) bool {
	o, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, ref := o["$ref"]
	return ref || o["$res"] == true || o["$gen"] == true || o["$opaque"] == true || o["$hashed"] != nil
}

// Absorption keeps symbolic declaration envelopes. A reference cannot be
// replaced by a provider echo or a hash. Normal planning verifies that the
// preserved expression is compatible with the chosen observation.
func absorbDeclarationProperties(declaration pkgmodel.Resource, observed json.RawMessage) (json.RawMessage, error) {
	filtered, err := filterCoOwnedProperties(observed, declaration.Schema, declaration.OwnedMembers)
	if err != nil {
		return nil, err
	}
	before, err := decodeResolutionJSON(declaration.Properties)
	if err != nil {
		return nil, err
	}
	live, err := decodeResolutionJSON(filtered)
	if err != nil {
		return nil, err
	}
	var adopt func(any, any) (any, error)
	adopt = func(prior, current any) (any, error) {
		if symbolicNode(prior) {
			return prior, nil
		}
		if symbolicNode(current) {
			return nil, fmt.Errorf("observed opaque value has no replayable declaration; supply a valid reference or value")
		}
		pm, pok := prior.(map[string]any)
		cm, cok := current.(map[string]any)
		if cok {
			out := map[string]any{}
			for k, v := range cm {
				var p any
				if pok {
					p = pm[k]
				}
				next, e := adopt(p, v)
				if e != nil {
					return nil, e
				}
				out[k] = next
			}
			return out, nil
		}
		pa, pok := prior.([]any)
		ca, cok := current.([]any)
		if cok {
			if pok && containsSymbolicNode(pa) {
				// An observed array may reorder or replace elements. Preserve the
				// entire symbolic declaration; normal planning verifies equivalence.
				return pa, nil
			}
			out := make([]any, len(ca))
			for i, v := range ca {
				var p any
				if pok && i < len(pa) {
					p = pa[i]
				}
				next, e := adopt(p, v)
				if e != nil {
					return nil, e
				}
				out[i] = next
			}
			return out, nil
		}
		return current, nil
	}
	result, err := adopt(before, live)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
func resolutionDeclarationEqual(a, b pkgmodel.Resource) (bool, error) {
	clean := func(r pkgmodel.Resource) pkgmodel.Resource {
		r.Ksuid = ""
		r.NativeID = ""
		r.Version = ""
		r.Managed = false
		r.OwnedMembers = nil
		r.ReadOnlyProperties = nil
		r.PatchDocument = nil
		return r
	}
	aa, err := json.Marshal(clean(a))
	if err != nil {
		return false, err
	}
	bb, err := json.Marshal(clean(b))
	if err != nil {
		return false, err
	}
	var av, bv any
	av, err = decodeResolutionJSON(aa)
	if err != nil {
		return false, err
	}
	bv, err = decodeResolutionJSON(bb)
	return resolutionJSONEqual(av, bv), err
}
func mergeResolutionDeclaration(prior, request, chosen pkgmodel.Resource) (pkgmodel.Resource, error) {
	// Metadata/schema edits are allowed only when unchanged by the disposition.
	pp, rp, cp := prior.Properties, request.Properties, chosen.Properties
	prior.Properties = nil
	request.Properties = nil
	chosen.Properties = nil
	equal, err := resolutionDeclarationEqual(prior, request)
	if err != nil {
		return chosen, err
	}
	if !equal {
		return chosen, fmt.Errorf("resource identity, schema or metadata edit overlaps this decision")
	}
	b, err := decodeResolutionJSON(pp)
	if err != nil {
		return chosen, err
	}
	r, err := decodeResolutionJSON(rp)
	if err != nil {
		return chosen, err
	}
	c, err := decodeResolutionJSON(cp)
	if err != nil {
		return chosen, err
	}
	var merge func(any, any, any, string) (any, error)
	merge = func(b, r, c any, path string) (any, error) {
		if resolutionJSONEqual(b, r) {
			return c, nil
		}
		if resolutionJSONEqual(b, c) || resolutionJSONEqual(r, c) {
			return r, nil
		}
		bm, bok := b.(map[string]any)
		rm, rok := r.(map[string]any)
		cm, cok := c.(map[string]any)
		if bok && rok && cok && !symbolicNode(b) && !symbolicNode(r) && !symbolicNode(c) {
			out := map[string]any{}
			keys := map[string]bool{}
			for k := range bm {
				keys[k] = true
			}
			for k := range rm {
				keys[k] = true
			}
			for k := range cm {
				keys[k] = true
			}
			for k := range keys {
				bv, be := bm[k]
				rv, re := rm[k]
				cv, ce := cm[k]
				if be != re || be != ce {
					if be == re && resolutionJSONEqual(bv, rv) {
						if ce {
							out[k] = cv
						}
						continue
					}
					if be == ce && resolutionJSONEqual(bv, cv) {
						if re {
							out[k] = rv
						}
						continue
					}
					if re == ce && resolutionJSONEqual(rv, cv) {
						if re {
							out[k] = rv
						}
						continue
					}
					return nil, fmt.Errorf("conflicting edit at %s/%s", path, k)
				}
				v, e := merge(bv, rv, cv, path+"/"+k)
				if e != nil {
					return nil, e
				}
				out[k] = v
			}
			return out, nil
		}
		return nil, fmt.Errorf("conflicting edit at %s", path)
	}
	result, err := merge(b, r, c, "")
	if err != nil {
		return chosen, err
	}
	chosen.Properties, err = json.Marshal(result)
	return chosen, err
}

// Final review includes every certified revision and the actual composed command
// (schemas, references, ownership and provenance included). Only generated IDs
// and execution clocks are normalized; authored property values remain exact.
func bindFinalResolution(plan *guardedApplyPlan, input *pkgmodel.Forma, options *config.FormaCommandConfig) error {
	if plan.Command.Resolution == nil {
		return nil
	}
	command := ownPlanningValue(plan.Command)
	replacements := map[string]string{}
	for _, u := range command.ResourceUpdates {
		if u.Operation == resource_update.OperationCreate && u.PriorState.Ksuid == "" {
			replacements[u.DesiredState.Ksuid] = "new-resource:" + u.DesiredState.Stack + ":" + u.DesiredState.Type + ":" + u.DesiredState.Label
		}
	}
	for _, u := range command.StackUpdates {
		if u.Operation == types.OperationCreate && u.Stack.ID != "" {
			replacements[u.Stack.ID] = "new-stack:" + u.Stack.Label
		}
	}
	for _, u := range command.GeneratorUpdates {
		if u.Operation == types.OperationCreate && u.Generator != nil {
			replacements[u.Generator.GetID()] = "new-generator:" + u.StackLabel + ":" + u.Generator.GetLabel()
		}
	}
	for _, u := range command.PolicyUpdates {
		if string(u.Operation) == string(types.OperationCreate) && u.PolicyID != "" {
			replacements[u.PolicyID] = "new-policy:" + u.StackLabel + ":" + u.Policy.GetLabel()
		}
	}
	delete(replacements, "")
	command.ID = ""
	command.StartTs = time.Time{}
	command.ModifiedTs = time.Time{}
	command.ClientID = ""
	command.Subject = ""
	command.SubjectName = ""
	command.Message = ""
	command.Config = infrastructureOptions(options)
	command.Resolution.ReviewID = ""
	for i := range command.ResourceUpdates {
		u := &command.ResourceUpdates[i]
		u.StartTs = time.Time{}
		u.ModifiedTs = time.Time{}
		u.GroupID = ""
		if !u.IsAcceptance() {
			u.Version = ""
		}
	}
	for i := range command.TargetUpdates {
		u := &command.TargetUpdates[i]
		u.StartTs = time.Time{}
		u.ModifiedTs = time.Time{}
		u.Version = ""
	}
	for i := range command.StackUpdates {
		u := &command.StackUpdates[i]
		u.StartTs = time.Time{}
		u.ModifiedTs = time.Time{}
		u.Version = ""
	}
	for i := range command.PolicyUpdates {
		u := &command.PolicyUpdates[i]
		u.StartTs = time.Time{}
		u.ModifiedTs = time.Time{}
		u.Version = ""
	}
	for i := range command.GeneratorUpdates {
		u := &command.GeneratorUpdates[i]
		u.StartTs = time.Time{}
		u.ModifiedTs = time.Time{}
		u.Version = ""
	}
	draws := ownPlanningValue(command.DrawGeneratorUpdates)
	for i := range draws {
		draws[i].StartTs = time.Time{}
		draws[i].ModifiedTs = time.Time{}
		draws[i].Version = ""
	}
	raw, err := json.Marshal(struct {
		Command any
		Draws   any
	}{command, draws})
	if err != nil {
		return err
	}
	value, err := decodeResolutionJSON(raw)
	if err != nil {
		return err
	}
	var normalize func(any) any
	normalize = func(v any) any {
		switch x := v.(type) {
		case string:
			for id, name := range replacements {
				x = strings.ReplaceAll(x, id, name)
			}
			return x
		case map[string]any:
			for k, e := range x {
				x[k] = normalize(e)
			}
			return x
		case []any:
			for i, e := range x {
				x[i] = normalize(e)
			}
			return x
		}
		return v
	}
	value = normalize(value)
	// Operation order derives from maps in several existing planners. It is not
	// execution order (the changeset DAG computes that from the bound references).
	root := value.(map[string]any)
	sortJSONList := func(v any) {
		if a, ok := v.([]any); ok {
			sort.Slice(a, func(i, j int) bool {
				x, _ := json.Marshal(a[i])
				y, _ := json.Marshal(a[j])
				return bytes.Compare(x, y) < 0
			})
		}
	}
	for _, key := range []string{"ResourceUpdates", "TargetUpdates", "StackUpdates", "PolicyUpdates", "GeneratorUpdates", "Stacks"} {
		sortJSONList(root["Command"].(map[string]any)[key])
	}
	sortJSONList(root["Draws"])
	reviewID, err := resolutionHash(struct {
		Version int
		Forma   *pkgmodel.Forma
		Options config.FormaCommandConfig
		Plan    any
		Guards  []datastore.RevisionGuard
	}{1, input, infrastructureOptions(options), value, plan.Guards})
	if err != nil {
		return err
	}
	if options.Resolution.ReviewID != "" && options.Resolution.ReviewID != reviewID {
		return resolutionError("stale-review", "final plan or relevant state changed; simulate the complete resolution again", "")
	}
	if !options.Simulate && options.Resolution.ReviewID == "" {
		return resolutionError("review-required", "simulate the complete decisions and submit its ReviewID", "")
	}
	plan.Command.Resolution.ReviewID = reviewID
	plan.refreshResponse()
	return nil
}

// A full reconcile explicitly omitting desired intent records its withdrawal.
// Confirmed observed deletion remains a distinct acceptance; withdrawal changes
// only intent and preserves the original operation outcome and cloud uncertainty.
func addOmittedDesiredAcceptances(ds datastore.Datastore, forma *pkgmodel.Forma, command *forma_command.FormaCommand) error {
	reader, ok := ds.(datastore.ResourceObservationReader)
	if !ok {
		return nil
	}
	for _, label := range drift.StackLabelsFromForma(forma) {
		baseline, err := ds.GetResourcesAtLastReconcile(label)
		if err != nil {
			return err
		}
		for _, prior := range baseline {
			declared := false
			for _, r := range forma.Resources {
				if r.Stack == label && r.Type == prior.Type && (r.Label == prior.Label || r.Alias == prior.Label) {
					declared = true
					break
				}
			}
			if declared {
				continue
			}
			obs, err := reader.GetResourceObservation(prior.KSUID)
			if err != nil {
				return err
			}
			if obs != nil && (obs.Operation == "create" || obs.Operation == "update") {
				continue
			}
			operation := resource_update.OperationWithdraw
			version := ""
			if obs != nil && obs.ConfirmedDeletion && obs.StackID == prior.StackID {
				operation = resource_update.OperationAcceptDelete
				version = obs.Version
			}
			if prior.Declaration == nil {
				return resolutionError("desired-intent-unavailable", "previous desired declaration is unavailable", prior.KSUID)
			}
			exists := false
			for _, u := range command.ResourceUpdates {
				if u.DesiredState.Ksuid == prior.KSUID {
					exists = true
				}
			}
			if !exists {
				command.ResourceUpdates = append(command.ResourceUpdates, resource_update.ResourceUpdate{DesiredState: *ownPlanningValue(prior.Declaration), Operation: operation, State: resource_update.ResourceUpdateStateSuccess, Version: version, StackLabel: label, Source: resource_update.FormaCommandSourceUser})
			}
		}
	}
	return nil
}
