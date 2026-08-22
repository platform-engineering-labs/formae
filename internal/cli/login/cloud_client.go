// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package login

import (
	"context"

	"github.com/platform-engineering-labs/formae/internal/cli/cloudapi"
)

// The hardened control-plane HTTP client lives in internal/cli/cloudapi; this
// file is login's view of it. The record types are aliases, but the interface
// is deliberately narrower than cloudapi.Client: login reads the caller's
// grants and nothing else, and every stub in this package implements exactly
// that one call.

type (
	// Installation is one installation the caller's grants cover.
	Installation = cloudapi.Installation
	// Snapshot carries the installations and whether the response was complete
	// and fully valid. Only a complete response licenses removing anything.
	Snapshot = cloudapi.Snapshot
)

// CloudClient reads the installations the caller's grants cover. Satisfied by
// cloudapi.Client.
type CloudClient interface {
	ListInstallations(ctx context.Context, bearer string) (Snapshot, error)
}

// newCloudClient returns a client for the control plane at baseURL.
var newCloudClient = func(baseURL string) CloudClient { return cloudapi.NewClient(baseURL) }

// maxWarnedRunes bounds any value from a control-plane response that a warning
// repeats back, so a broken or hostile control plane cannot choose how much
// text lands in the user's terminal. The same bound the client itself applies.
const maxWarnedRunes = 64

// clip bounds a value taken from a response before a warning repeats it back.
// Call sites quote the result with %q where it is a value rather than prose,
// so it can neither hide itself nor rewrite the line around it.
func clip(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}
