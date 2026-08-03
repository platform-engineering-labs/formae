// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package discovery

import (
	"encoding/json"

	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// resolveTargetConfigForList returns an ephemeral copy of target.Config with
// every opaque $ref replaced by its live plaintext value, so the List call
// authenticates. It delegates to the shared resolver used by every plugin-call
// path that loads a persisted target config (see
// resource_update.ResolveOpaqueTargetConfig).
func resolveTargetConfigForList(proc gen.Process, target pkgmodel.Target) (json.RawMessage, error) {
	return resource_update.ResolveOpaqueTargetConfig(proc, target)
}
