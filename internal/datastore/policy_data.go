// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
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
