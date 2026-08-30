// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import _ "embed"

// azureTemplateJSON is the ARM template that establishes the trust without
// formae ever holding a provisioning credential: a customer deploys it
// themselves (portal, their own az, or their own pipeline) and gives formae
// the clientId and tenantId outputs to register.
//
//go:embed assets/connect-azure.json
var azureTemplateJSON []byte
