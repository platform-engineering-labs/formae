// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package policy_update

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// PolicyDatastore defines the datastore operations needed for policy updates
type PolicyDatastore interface {
	// GetStackByLabel retrieves a stack by its label
	GetStackByLabel(label string) (*pkgmodel.Stack, error)
	// GetPoliciesForStack returns all non-deleted policies for a given stack ID
	GetPoliciesForStack(stackID string) ([]pkgmodel.Policy, error)
	// GetStandalonePolicy retrieves a standalone policy by label (stack_id IS NULL)
	// Returns nil, nil if no policy is found
	GetStandalonePolicy(label string) (pkgmodel.Policy, error)
	// IsPolicyAttachedToStack checks if a standalone policy is attached to a stack
	IsPolicyAttachedToStack(stackLabel, policyLabel string) (bool, error)
}

// PolicyUpdateGenerator generates policy updates by comparing desired state with existing state
type PolicyUpdateGenerator struct {
	datastore PolicyDatastore
}

// NewPolicyUpdateGenerator creates a new policy update generator
func NewPolicyUpdateGenerator(ds PolicyDatastore) *PolicyUpdateGenerator {
	return &PolicyUpdateGenerator{datastore: ds}
}

// GeneratePolicyUpdates determines what policy changes are needed
func (pg *PolicyUpdateGenerator) GeneratePolicyUpdates(forma *pkgmodel.Forma, command pkgmodel.Command) ([]PolicyUpdate, error) {
	// For destroy commands, we don't generate policy updates here.
	// Inline policy deletion happens implicitly when their stack is deleted
	// (via cascade delete in DeleteStack).
	if command == pkgmodel.CommandDestroy {
		return nil, nil
	}

	var updates []PolicyUpdate

	// Process inline policies from stacks
	for _, stack := range forma.Stacks {
		stackUpdates, err := pg.generateInlinePolicyUpdates(stack)
		if err != nil {
			return nil, fmt.Errorf("failed to generate policy updates for stack %s: %w", stack.Label, err)
		}
		updates = append(updates, stackUpdates...)
	}

	// Process standalone policies from forma
	standaloneUpdates, err := pg.generateStandalonePolicyUpdates(forma.Policies)
	if err != nil {
		return nil, fmt.Errorf("failed to generate standalone policy updates: %w", err)
	}
	updates = append(updates, standaloneUpdates...)

	// Process policy attachments (stack -> standalone policy references)
	attachments, err := pg.generatePolicyAttachments(forma)
	if err != nil {
		return nil, fmt.Errorf("failed to generate policy attachments: %w", err)
	}
	updates = append(updates, attachments...)

	return updates, nil
}

func (pg *PolicyUpdateGenerator) generateInlinePolicyUpdates(stack pkgmodel.Stack) ([]PolicyUpdate, error) {
	if len(stack.Policies) == 0 {
		return nil, nil
	}

	// Filter out policy references - they are handled as standalone policies
	var inlinePolicies []json.RawMessage
	for _, raw := range stack.Policies {
		if pkgmodel.IsPolicyReference(raw) {
			slog.Debug("Skipping policy reference in stack (handled as standalone)",
				"stack", stack.Label)
			continue
		}
		inlinePolicies = append(inlinePolicies, raw)
	}

	if len(inlinePolicies) == 0 {
		return nil, nil
	}

	policies, err := pkgmodel.ParsePolicies(inlinePolicies)
	if err != nil {
		return nil, fmt.Errorf("failed to parse policies: %w", err)
	}

	// Look up existing policies for this stack
	existingPoliciesByType := make(map[string]pkgmodel.Policy)
	if pg.datastore != nil {
		existingStack, err := pg.datastore.GetStackByLabel(stack.Label)
		if err == nil && existingStack != nil {
			existingPolicies, err := pg.datastore.GetPoliciesForStack(existingStack.ID)
			if err == nil {
				for _, p := range existingPolicies {
					existingPoliciesByType[p.GetType()] = p
				}
			}
		}
	}

	now := util.TimeNow()
	var updates []PolicyUpdate

	for _, policy := range policies {
		var operation PolicyOperation
		label := policy.GetLabel()

		// Check if a policy of this type already exists for this stack
		if existing, found := existingPoliciesByType[policy.GetType()]; found {
			// Reuse the existing label for inline policies
			if label == "" {
				label = existing.GetLabel()
				// Set the label on the policy
				if ttl, ok := policy.(*pkgmodel.TTLPolicy); ok {
					ttl.Label = label
				}
			}
			operation = PolicyOperationUpdate
		} else {
			// New policy - generate label if not provided
			if label == "" {
				label = fmt.Sprintf("%s-%s-%s", stack.Label, policy.GetType(), util.NewID()[:8])
				// Set the label on the policy
				if ttl, ok := policy.(*pkgmodel.TTLPolicy); ok {
					ttl.Label = label
				}
			}
			operation = PolicyOperationCreate
		}

		update := PolicyUpdate{
			Policy:     policy,
			Operation:  operation,
			State:      PolicyUpdateStateNotStarted,
			StackLabel: stack.Label, // Mark as inline
			StartTs:    now,
			ModifiedTs: now,
		}

		updates = append(updates, update)
		slog.Debug("Generated inline policy update",
			"label", label,
			"type", policy.GetType(),
			"stack", stack.Label,
			"operation", update.Operation)
	}

	return updates, nil
}

func (pg *PolicyUpdateGenerator) generateStandalonePolicyUpdates(rawPolicies []json.RawMessage) ([]PolicyUpdate, error) {
	if len(rawPolicies) == 0 {
		return nil, nil
	}

	policies, err := pkgmodel.ParsePolicies(rawPolicies)
	if err != nil {
		return nil, fmt.Errorf("failed to parse policies: %w", err)
	}

	now := util.TimeNow()
	var updates []PolicyUpdate

	for _, policy := range policies {
		// Standalone policies must have a label
		if policy.GetLabel() == "" {
			return nil, fmt.Errorf("standalone policy of type %s must have a label", policy.GetType())
		}

		var operation PolicyOperation
		var existingPolicy pkgmodel.Policy

		// Check if this standalone policy already exists
		if pg.datastore != nil {
			existing, err := pg.datastore.GetStandalonePolicy(policy.GetLabel())
			if err != nil {
				slog.Warn("Failed to check for existing standalone policy",
					"label", policy.GetLabel(),
					"error", err)
			}
			if existing != nil {
				existingPolicy = existing
				// Check if the policy has actually changed
				if policiesEqual(existing, policy) {
					slog.Debug("Standalone policy unchanged, skipping",
						"label", policy.GetLabel())
					continue
				}
				operation = PolicyOperationUpdate
			} else {
				operation = PolicyOperationCreate
			}
		} else {
			operation = PolicyOperationCreate
		}

		update := PolicyUpdate{
			Policy:         policy,
			ExistingPolicy: existingPolicy,
			Operation:      operation,
			State:          PolicyUpdateStateNotStarted,
			StackLabel:     "", // Empty = standalone
			StartTs:        now,
			ModifiedTs:     now,
		}

		updates = append(updates, update)
		slog.Debug("Generated standalone policy update",
			"label", policy.GetLabel(),
			"type", policy.GetType(),
			"operation", update.Operation)
	}

	return updates, nil
}

// policiesEqual compares two policies for equality
func policiesEqual(a, b pkgmodel.Policy) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.GetType() != b.GetType() {
		return false
	}
	if a.GetLabel() != b.GetLabel() {
		return false
	}

	// Type-specific comparison
	switch pa := a.(type) {
	case *pkgmodel.TTLPolicy:
		pb, ok := b.(*pkgmodel.TTLPolicy)
		if !ok {
			return false
		}
		return pa.TTLSeconds == pb.TTLSeconds && pa.OnDependents == pb.OnDependents
	default:
		// For unknown types, assume not equal to be safe
		return false
	}
}

// generatePolicyAttachments creates associations between stacks and standalone policies
func (pg *PolicyUpdateGenerator) generatePolicyAttachments(forma *pkgmodel.Forma) ([]PolicyUpdate, error) {
	var updates []PolicyUpdate
	now := util.TimeNow()

	for _, stack := range forma.Stacks {
		for _, raw := range stack.Policies {
			if !pkgmodel.IsPolicyReference(raw) {
				continue
			}

			policyLabel, err := pkgmodel.ParsePolicyReference(raw)
			if err != nil {
				return nil, fmt.Errorf("failed to parse policy reference in stack %s: %w", stack.Label, err)
			}

			// Check if this attachment already exists
			if pg.datastore != nil {
				attached, err := pg.datastore.IsPolicyAttachedToStack(stack.Label, policyLabel)
				if err != nil {
					slog.Warn("Failed to check if policy is attached",
						"stack", stack.Label,
						"policyLabel", policyLabel,
						"error", err)
				} else if attached {
					slog.Debug("Policy already attached to stack, skipping",
						"stack", stack.Label,
						"policyRef", policyLabel)
					continue
				}
			}

			// Create an attachment update
			update := PolicyUpdate{
				Policy:     nil, // Will be resolved from standalone policy
				Operation:  PolicyOperationAttach,
				State:      PolicyUpdateStateNotStarted,
				StackLabel: stack.Label,
				PolicyRef:  policyLabel, // Reference to standalone policy
				StartTs:    now,
				ModifiedTs: now,
			}
			updates = append(updates, update)
			slog.Debug("Generated policy attachment",
				"stack", stack.Label,
				"policyRef", policyLabel)
		}
	}

	return updates, nil
}
