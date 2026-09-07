// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Command oidc-auth-stub stands in for the hosted auth plugin so the e2e
// suite can drive `formae connect` against a control plane of its own.
//
// It is installed under the name `oidc` because that is what the CLI looks
// for: a hosted profile's auth block names the plugin in its `type` field,
// and the connect gate requires that name exactly before it will send a
// credential anywhere. The real plugin mints its bearer through an
// interactive sign-in against a deployed issuer, which no CI run can carry
// out; this one hands back a fixed token the e2e's stub control plane
// recognises, so what the test exercises is the connect flow rather than the
// sign-in behind it.
package main

import (
	"github.com/platform-engineering-labs/formae/pkg/auth"
)

// Bearer is the credential this plugin hands back. The e2e's stub control
// plane compares against it byte for byte, so a run that reached the control
// plane with anything else did not come through the auth plugin.
const Bearer = "Bearer e2e-oidc-connect-token"

// stub answers GetAuthHeader and nothing else. Validate is what an agent
// would call to check an inbound credential, and no agent in this suite is
// configured with this plugin, so the embedded base's unsupported answer is
// the honest one.
type stub struct {
	auth.UnimplementedAuthPlugin
}

// Init accepts whatever the profile's auth block carries. The block is the
// oidc plugin's CLI configuration and this stub reads nothing out of it: the
// gate has already checked the fields that decide whether a credential may
// be minted at all.
func (s *stub) Init(_ *auth.InitRequest, _ *auth.InitResponse) error { return nil }

// GetAuthHeader returns the fixed credential under the canonical header key.
// The client attaches only "Authorization", so returning it under any other
// spelling would fail closed.
func (s *stub) GetAuthHeader(_ *auth.GetAuthHeaderRequest, resp *auth.GetAuthHeaderResponse) error {
	resp.Headers = map[string][]string{"Authorization": {Bearer}}
	return nil
}

func main() {
	auth.Run(&stub{})
}
