// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package discovery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"ergo.services/ergo/gen"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// resolveTargetConfigForList returns an ephemeral copy of target.Config with
// every opaque $ref replaced by its live plaintext value. When the config
// carries no opaque references it is returned unchanged. Any resolve failure
// returns a redacted error that names the reference and target but never the
// plaintext secret.
func resolveTargetConfigForList(proc gen.Process, target pkgmodel.Target) (json.RawMessage, error) {
	uris := resolver.ExtractOpaqueResolvableURIsFromJSON(target.Config)
	if len(uris) == 0 {
		return target.Config, nil
	}

	// Work on a copy so the caller's original config is never mutated.
	cfg := bytes.Clone(target.Config)

	for _, uri := range uris {
		// Load the source resource from the persister.
		rawResult, err := proc.Call(
			gen.ProcessID{Name: actornames.ResourcePersister, Node: proc.Node().Name()},
			messages.LoadResource{ResourceURI: uri.Stripped()},
		)
		if err != nil {
			proc.Log().Error(
				"failed to load resource for opaque ref resolution uri=%s target=%s: %v",
				uri, target.Label, err,
			)
			return nil, fmt.Errorf(
				"failed to resolve opaque reference %q for target %q: load error",
				uri, target.Label,
			)
		}

		loadResult, ok := rawResult.(messages.LoadResourceResult)
		if !ok {
			proc.Log().Error(
				"unexpected result type from resource persister resultType=%v uri=%s target=%s",
				reflect.TypeOf(rawResult), uri, target.Label,
			)
			return nil, fmt.Errorf(
				"failed to resolve opaque reference %q for target %q: unexpected persister response",
				uri, target.Label,
			)
		}

		// Strip resolvable metadata from the source target's config before
		// sending to the plugin — mirrors the pattern in resolve_cache.go.
		srcCfg := loadResult.Target.Config
		if cleanCfg, err := resolver.ConvertToPluginFormat(srcCfg); err == nil {
			srcCfg = cleanCfg
		}

		progress, err := readSource(proc, loadResult.Resource, srcCfg)
		if err != nil {
			proc.Log().Error(
				"failed to read resource for opaque ref resolution uri=%s target=%s: %v",
				uri, target.Label, err,
			)
			return nil, fmt.Errorf(
				"failed to resolve opaque reference %q for target %q: read error",
				uri, target.Label,
			)
		}

		// Extract the property value from the read result.
		parsed := gjson.ParseBytes([]byte(progress.ResourceProperties))
		value := parsed.Get(uri.PropertyPath())
		if !value.Exists() {
			proc.Log().Error(
				"opaque ref property not found in read result property=%s uri=%s target=%s",
				uri.PropertyPath(), uri, target.Label,
			)
			return nil, fmt.Errorf(
				"failed to resolve opaque reference %q for target %q: property %q absent from read result",
				uri, target.Label, uri.PropertyPath(),
			)
		}

		// Inject the resolved plaintext back into the working config copy.
		cfg, err = resolver.ResolvePropertyReferences(uri, cfg, value.String())
		if err != nil {
			proc.Log().Error(
				"failed to inject resolved value uri=%s target=%s configRedacted=%v: %v",
				uri, target.Label, pkgmodel.RedactOpaqueForLog(cfg), err,
			)
			return nil, fmt.Errorf(
				"failed to resolve opaque reference %q for target %q: inject error",
				uri, target.Label,
			)
		}
	}

	// Strip the $ref/$value/$visibility wrappers so the returned config is
	// plain JSON the plugin can unmarshal directly. A failure here indicates
	// an unresolvable envelope (e.g. a $hashed field whose plaintext is
	// irrecoverable); surface it rather than returning the still-wrapped config.
	plain, err := resolver.ConvertToPluginFormat(cfg)
	if err != nil {
		proc.Log().Error(
			"failed to strip opaque wrappers from resolved target config target=%s configRedacted=%v: %v",
			target.Label, pkgmodel.RedactOpaqueForLog(cfg), err,
		)
		return nil, fmt.Errorf(
			"failed to prepare resolved config for target %q: convert error",
			target.Label,
		)
	}

	return plain, nil
}

// readSource performs a single ReadResourceViaPlugin for opaque-ref resolution.
// It does not retry and never sleeps: a recoverable failure is surfaced to the
// caller, and discovery skips the target for this cycle and retries it on the
// next cycle, so a transient failure is absorbed without blocking the discovery
// actor.
func readSource(proc gen.Process, res pkgmodel.Resource, cfg json.RawMessage) (*plugin.TrackedProgress, error) {
	progress, err := resource_update.ReadResourceViaPlugin(proc, res, cfg)
	if err != nil {
		return nil, err
	}
	if progress.OperationStatus == resource.OperationStatusFailure {
		return nil, fmt.Errorf("read failed: error %s", progress.ErrorCode)
	}
	return progress, nil
}
