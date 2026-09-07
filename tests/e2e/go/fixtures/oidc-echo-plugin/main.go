// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Command oidc-echo is a resource plugin for the e2e suite. Its one resource
// type has no provider behind it: creating it asks the agent for an OIDC
// identity token and records what came back, so a test can assert the token
// travelled the whole chain from the broker to a plugin operation.
package main

import "github.com/platform-engineering-labs/formae/pkg/plugin/sdk"

func main() {
	sdk.RunWithManifest(&EchoPlugin{}, sdk.RunConfig{})
}
