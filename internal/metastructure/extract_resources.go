// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/querier"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

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
	stackLabels := make([]string, 0)
	uniqueStacks := make(map[string]struct{})

	for _, resource := range resources {
		if resource.Target != "" {
			if _, exists := uniqueTargets[resource.Target]; !exists {
				uniqueTargets[resource.Target] = struct{}{}
				targetNames = append(targetNames, resource.Target)
			}
		}
		if resource.Stack != "" {
			if _, exists := uniqueStacks[resource.Stack]; !exists {
				uniqueStacks[resource.Stack] = struct{}{}
				stackLabels = append(stackLabels, resource.Stack)
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

	if len(stackLabels) > 0 {
		forma.Stacks = make([]pkgmodel.Stack, 0, len(stackLabels))
		for _, label := range stackLabels {
			stack, err := m.Datastore.GetStackByLabel(label)
			if err != nil {
				slog.Error("Failed to load stack by label", "label", label, "error", err)
				continue
			}
			if stack != nil {
				forma.Stacks = append(forma.Stacks, *stack)
			} else {
				// Stack not found in datastore (e.g., $unmanaged) - create a synthetic entry
				forma.Stacks = append(forma.Stacks, pkgmodel.Stack{
					Label:       label,
					Description: "Resources imported with formae extract",
				})
			}
		}
	}

	// Collect referenced standalone policy labels from stacks
	uniquePolicyLabels := make(map[string]struct{})
	for _, stack := range forma.Stacks {
		for _, rawPolicy := range stack.Policies {
			if pkgmodel.IsPolicyReference(rawPolicy) {
				policyLabel, err := pkgmodel.ParsePolicyReference(rawPolicy)
				if err != nil {
					slog.Debug("Failed to parse policy reference", "error", err)
					continue
				}
				uniquePolicyLabels[policyLabel] = struct{}{}
			}
		}
	}

	// Load standalone policies and add to forma
	if len(uniquePolicyLabels) > 0 {
		forma.Policies = make([]json.RawMessage, 0, len(uniquePolicyLabels))
		for label := range uniquePolicyLabels {
			policy, err := m.Datastore.GetStandalonePolicy(label)
			if err != nil {
				slog.Error("Failed to load standalone policy", "label", label, "error", err)
				continue
			}
			if policy != nil {
				policyJSON, err := json.Marshal(policy)
				if err != nil {
					slog.Error("Failed to marshal standalone policy", "label", label, "error", err)
					continue
				}
				forma.Policies = append(forma.Policies, policyJSON)
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

	// Build a lookup of last reconcile times per stack
	reconcileInfos, err := m.Datastore.GetStacksWithAutoReconcilePolicy()
	lastReconcileByStack := make(map[string]time.Time)
	if err != nil {
		slog.Warn("Failed to get auto-reconcile info", "error", err)
	} else {
		for _, info := range reconcileInfos {
			lastReconcileByStack[info.StackLabel] = info.LastReconcileAt
		}
	}

	// Populate policies for each stack
	for _, stack := range stacks {
		policies, err := m.Datastore.GetPoliciesForStack(stack.ID)
		if err != nil {
			slog.Warn("Failed to get policies for stack", "stack", stack.Label, "error", err)
			continue
		}
		// Convert policies to json.RawMessage for the Stack.Policies field
		for _, policy := range policies {
			// Enrich auto-reconcile policies with last reconcile time
			if arPolicy, ok := policy.(*pkgmodel.AutoReconcilePolicy); ok {
				if lastRecon, found := lastReconcileByStack[stack.Label]; found {
					arPolicy.LastReconcileAt = lastRecon
				}
			}
			policyJSON, err := json.Marshal(policy)
			if err != nil {
				slog.Warn("Failed to marshal policy", "policy", policy.GetLabel(), "error", err)
				continue
			}
			stack.Policies = append(stack.Policies, json.RawMessage(policyJSON))
		}
	}

	slog.Debug("ExtractStacks returning", "count", len(stacks))
	return stacks, nil
}
