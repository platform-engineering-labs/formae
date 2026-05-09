// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

// MinFormaeVersion is the minimum formae agent version compatible with this SDK.
// Plugins built against this SDK should check this at startup to ensure
// the agent they're running under is compatible.
const MinFormaeVersion = "0.84.0"

// SDKVersion is the version of this SDK package.
// It is embedded in plugin manifests and used for compatibility checks.
const SDKVersion = "0.2.1"
