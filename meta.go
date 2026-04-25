// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package formae

var Version = "0.0.0"

// Channel is the release channel (e.g., "stable", "dev"), populated at
// build time from the Makefile's CHANNEL variable. Used by the PKL package
// resolver to construct channel-namespaced schema URLs — PKL has no native
// channel concept, so the channel is encoded in the URL path. Defaults to
// "stable" for untagged or stable builds, in which case URLs use the flat
// path (back-compat).
var Channel = "stable"

const DefaultInstallPrefix = "/opt/pel"
const DefaultInstallPath = "/opt/pel/formae"
