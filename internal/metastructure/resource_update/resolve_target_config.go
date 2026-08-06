// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

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
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// RecoverableResolveError marks a target-config resolution failure whose
// underlying plugin Read returned a recoverable error code. Resolution is
// single-shot and never blocks; a caller running on an actor loop should detect
// this (errors.As) and reschedule the resolve non-blockingly rather than fail.
type RecoverableResolveError struct {
	Code resource.OperationErrorCode
}

func (e *RecoverableResolveError) Error() string {
	return fmt.Sprintf("recoverable resolve read error: %s", e.Code)
}

// ResolveOpaqueTargetConfig returns an ephemeral copy of target.Config with
// every opaque $ref replaced by its live plaintext value, ready to hand to a
// plugin call. When the config carries no opaque references it is returned
// unchanged (after stripping any cached metadata).
//
// This is the single resolution routine for every plugin-call path that loads a
// PERSISTED target config and must authenticate a plugin operation: at rest an
// opaque secret-sourced credential is a bare $ref with no $value (reference-
// don't-store), and resolver.ConvertToPluginFormat only strips metadata — it
// does NOT read the source. So any such path (discovery List, the resolve-read
// path in ResolveCache, ...) must call this first; the primary apply/changeset
// path instead resolves via a synthetic Resolve target op and propagation.
//
// Any resolve failure returns a redacted error that names the reference and
// target but never the plaintext secret.
func ResolveOpaqueTargetConfig(proc gen.Process, target pkgmodel.Target) (json.RawMessage, error) {
	uris := resolver.ExtractOpaqueResolvableURIsFromJSON(target.Config)
	if len(uris) == 0 {
		// No opaque refs: still strip any cached $value/metadata so the plugin
		// receives plain JSON, mirroring the resolved path's final conversion.
		if plain, err := resolver.ConvertToPluginFormat(target.Config); err == nil {
			return plain, nil
		}
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
		// sending to the plugin. The source of a credential is itself a managed
		// resource (e.g. a secret) whose own target auth is a plain/bootstrap
		// credential, not another opaque ref (transitive-opaque is rejected at
		// admission), so a metadata strip is sufficient here.
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
			// Wrap with %w so a caller on an actor loop can detect a
			// RecoverableResolveError and reschedule non-blockingly.
			return nil, fmt.Errorf(
				"failed to resolve opaque reference %q for target %q: %w",
				uri, target.Label, err,
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

	// Strip the $ref/$value/$visibility wrappers so the returned config is plain
	// JSON the plugin can unmarshal directly.
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

// readSource performs a SINGLE plugin Read of a credential source resource. It
// never retries and never blocks: a recoverable failure is surfaced as a
// *RecoverableResolveError so the actor-loop caller can reschedule the whole
// resolve via SendAfter. A non-recoverable failure is a plain terminal error.
func readSource(proc gen.Process, res pkgmodel.Resource, cfg json.RawMessage) (*plugin.TrackedProgress, error) {
	progress, err := ReadResourceViaPlugin(proc, res, cfg)
	if err != nil {
		return nil, err
	}
	if progress.OperationStatus == resource.OperationStatusFailure {
		if resource.IsRecoverable(progress.ErrorCode) {
			return nil, &RecoverableResolveError{Code: progress.ErrorCode}
		}
		return nil, fmt.Errorf("read failed: non-recoverable error %s", progress.ErrorCode)
	}
	return progress, nil
}
