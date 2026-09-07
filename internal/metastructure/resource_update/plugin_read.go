// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"

	"ergo.services/ergo/gen"
	"github.com/google/uuid"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ReadResourceViaPlugin spawns a PluginOperator for the given resource and
// executes a single Read call, returning the plugin's progress result.
// It is a pure dispatch helper — callers own caching, retry, and result
// interpretation.
func ReadResourceViaPlugin(proc gen.Process, res pkgmodel.Resource, targetConfig json.RawMessage) (*plugin.TrackedProgress, error) {
	// The provider boundary for this dispatch. A generator reference in the
	// target's config names a credential that was never drawn, and the envelope
	// is never that credential: handing it over puts a JSON object where a
	// token belongs. Refuse before the spawn rather than reading with
	// credentials formae does not have. Only the config is checked — the
	// resource's own properties are context for a Read, never values written.
	if err := resolver.GuardNoUnresolvedGenerators(targetConfig); err != nil {
		return nil, fmt.Errorf("cannot read %s: its target's configuration is bound to a generator whose value has not been drawn: %w", res.URI(), err)
	}

	operationID := uuid.New().String()
	spawnResult, err := messages.UnwrapCall(proc.Call(
		gen.ProcessID{Name: actornames.PluginCoordinator, Node: proc.Node().Name()},
		messages.SpawnPluginOperator{
			Namespace:   res.Namespace(),
			ResourceURI: string(res.URI()),
			Operation:   string(resource.OperationRead),
			OperationID: operationID,
			RequestedBy: proc.PID(),
		}))
	if err != nil {
		return nil, fmt.Errorf("failed to spawn plugin operator: %w", err)
	}
	spawnRes, ok := spawnResult.(messages.SpawnPluginOperatorResult)
	if !ok {
		return nil, fmt.Errorf("unexpected result type from PluginCoordinator: %T", spawnResult)
	}
	if spawnRes.Error != "" {
		return nil, fmt.Errorf("failed to spawn plugin operator: %s", spawnRes.Error)
	}

	// Use the same call budget as ResourceUpdater.doPluginOperation. The default
	// Ergo Call timeout (5s) is too short for live AWS API reads, which routinely
	// run longer than that — especially CloudControl GetResource immediately
	// after a Create, when SDK credential resolution and the read itself stack up.
	progressResult, err := proc.CallWithTimeout(
		spawnRes.PID,
		plugin.ReadResource{
			Namespace:         res.Namespace(),
			ResourceType:      res.Type,
			ResourceNamespace: res.Namespace(),
			ExistingResource:  res,
			Resource:          res,
			NativeID:          res.NativeID,
			TargetConfig:      targetConfig,
		},
		PluginOperationCallTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource: %w", err)
	}

	progress, ok := progressResult.(plugin.TrackedProgress)
	if !ok {
		return nil, fmt.Errorf("unexpected result type from plugin operator: %T", progressResult)
	}
	return &progress, nil
}
