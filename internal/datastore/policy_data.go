// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"encoding/json"
	"fmt"
	"log/slog"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// TTLPolicyData builds the policy_data payload for a TTL policy.
//
// It writes the key for the variant that is set and only that key: a relative
// policy stores TTLSeconds, an absolute one stores a canonical ExpiresAt. Key
// presence is what the expiry queries and the deserializers branch on, so the
// four backends share this one function rather than each building the map — the
// presence invariant has to be identical in all of them.
func TTLPolicyData(p *pkgmodel.TTLPolicy) map[string]any {
	data := map[string]any{
		"OnDependents": p.OnDependents,
	}
	if p.IsAbsolute() {
		data["ExpiresAt"] = p.CanonicalExpiresAt()
	} else {
		data["TTLSeconds"] = p.TTLSeconds
	}
	return data
}

// DedupeExpiredStacks reduces expiry-scan rows to one per stack.
//
// A stack can carry several TTL policies — an inline one and an attached
// standalone one — and the scan yields a row per expired policy. Callers destroy
// once per entry, so returning both would start two concurrent destroys of the
// same stack. The earliest deadline wins, which is the one that actually expired
// the stack; the scan is ordered by anchor, so the choice is stable within a
// scan. Which policy's OnDependents should govern when they disagree is a
// separate question this does not try to answer.
func DedupeExpiredStacks(stacks []ExpiredStackInfo) []ExpiredStackInfo {
	if len(stacks) < 2 {
		return stacks
	}

	seen := make(map[string]int, len(stacks))
	deduped := make([]ExpiredStackInfo, 0, len(stacks))
	for _, stack := range stacks {
		if at, ok := seen[stack.StackID]; ok {
			if stack.Deadline() < deduped[at].Deadline() {
				deduped[at] = stack
			}
			continue
		}
		seen[stack.StackID] = len(deduped)
		deduped = append(deduped, stack)
	}
	return deduped
}

// TTLPolicyFromData rebuilds a TTL policy from a stored policy_data payload.
//
// It is deliberately lenient where TTLPolicyData's counterpart in ParsePolicy is
// strict. ParsePolicy is the gate on the way in, so anything that reaches
// storage is well formed; a row that is not — hand-edited or corrupt — must
// still be readable, because failing here would make the stack that carries it
// unloadable. Payloads outside the one-of therefore degrade rather than error,
// and resolve exactly as the expiry queries resolve them: a parsable ExpiresAt
// wins over TTLSeconds, and anything else leaves the policy with no deadline,
// which means it never expires.
func TTLPolicyFromData(label, policyDataStr, stackID string) (*pkgmodel.TTLPolicy, error) {
	var data struct {
		TTLSeconds   *int64  `json:"TTLSeconds"`
		ExpiresAt    *string `json:"ExpiresAt"`
		OnDependents string  `json:"OnDependents"`
	}
	if err := json.Unmarshal([]byte(policyDataStr), &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal TTL policy data: %w", err)
	}

	policy := &pkgmodel.TTLPolicy{
		Type:         "ttl",
		Label:        label,
		OnDependents: data.OnDependents,
		StackID:      stackID,
	}

	if data.ExpiresAt != nil {
		expiresAt, err := pkgmodel.CanonicalizeExpiresAt(*data.ExpiresAt)
		if err == nil {
			policy.ExpiresAt = expiresAt
			return policy, nil
		}
		slog.Warn("TTL policy has an unreadable ExpiresAt and will never expire",
			"label", label, "stackID", stackID, "error", err)
		return policy, nil
	}

	if data.TTLSeconds == nil {
		slog.Warn("TTL policy sets neither TTLSeconds nor ExpiresAt and will never expire",
			"label", label, "stackID", stackID)
		return policy, nil
	}

	policy.TTLSeconds = *data.TTLSeconds
	return policy, nil
}
