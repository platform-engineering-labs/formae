// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package discovery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

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

// maxDiscoveryResolveAttempts is the maximum number of plugin Read attempts
// made by readWithRetry before giving up on a single opaque reference.
const maxDiscoveryResolveAttempts = 3

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

		progress, err := readWithRetry(proc, loadResult.Resource, srcCfg)
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
	// plain JSON the plugin can unmarshal directly. A convert failure falls
	// through and returns the partially-resolved (still-wrapped) config; the
	// existing ConvertToPluginFormat guard in the scan path will handle it.
	if plain, err := resolver.ConvertToPluginFormat(cfg); err == nil {
		cfg = plain
	}

	return cfg, nil
}

// readWithRetry calls ReadResourceViaPlugin up to maxDiscoveryResolveAttempts
// times. It retries with a short pause when the result is a recoverable
// failure; on success, non-recoverable failure, or exhausted budget it returns.
func readWithRetry(proc gen.Process, res pkgmodel.Resource, cfg json.RawMessage) (*plugin.TrackedProgress, error) {
	for attempt := 1; attempt <= maxDiscoveryResolveAttempts; attempt++ {
		progress, err := resource_update.ReadResourceViaPlugin(proc, res, cfg)
		if err != nil {
			return nil, err
		}

		if progress.OperationStatus == resource.OperationStatusFailure &&
			resource.IsRecoverable(progress.ErrorCode) {
			if attempt < maxDiscoveryResolveAttempts {
				proc.Log().Info(
					"readWithRetry: recoverable error, retrying errorCode=%s attempt=%d/%d",
					progress.ErrorCode, attempt, maxDiscoveryResolveAttempts,
				)
				time.Sleep(75 * time.Millisecond)
				continue
			}
			proc.Log().Error(
				"readWithRetry: exhausted attempts errorCode=%s attempts=%d",
				progress.ErrorCode, attempt,
			)
			return nil, fmt.Errorf(
				"read failed after %d attempts: recoverable error %s",
				attempt, progress.ErrorCode,
			)
		}

		// Non-recoverable failure or success — return immediately.
		if progress.OperationStatus == resource.OperationStatusFailure {
			return nil, fmt.Errorf("read failed: non-recoverable error %s", progress.ErrorCode)
		}

		return progress, nil
	}

	// Should not be reachable, but guard against the loop falling through.
	return nil, fmt.Errorf("read failed: exhausted %d attempts", maxDiscoveryResolveAttempts)
}
