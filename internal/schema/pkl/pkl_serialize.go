// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	pklgo "github.com/apple/pkl-go/pkl"
	formae "github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/schema"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/pklrun"
)

// serializeWithPKL is a generic helper function that can serialize any data structure
func (p PKL) serializeWithPKL(data *model.Forma, options *schema.SerializeOptions) (string, error) {
	preprocessed, err := preprocessFormaEmbeds(data)
	if err != nil {
		return "", fmt.Errorf("error pre-processing embed fields: %w", err)
	}

	input, err := json.Marshal(preprocessed)
	if err != nil {
		return "", fmt.Errorf("error marshalling JSON: %w", err)
	}

	properties := map[string]string{
		"Json": string(input),
	}
	// Create temporary directory and extract embedded files
	tempDir, err := os.MkdirTemp("", "pkl-generator-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	includes := resolveIncludes(data, options)

	schemaLocation := schema.SchemaLocationRemote
	if options != nil && options.SchemaLocation != "" {
		schemaLocation = options.SchemaLocation
	}

	// Resolve schema versions early — needed before ProjectInit so the
	// generated PklProject can swap remote deps to local for namespaces
	// that have a resolved version. Hub-published packages don't ship
	// v*/ subtrees today; narrowing only resolves against the on-disk
	// install. resolveSchemaVersions is a no-op outside SchemaLocationLocal,
	// so versions is non-empty only when schemaLocation is already Local.
	versions, err := resolveSchemaVersions(data, options)
	if err != nil {
		return "", err
	}
	includes = swapVersionedDepsToLocal(includes, versions, options)

	err = fs.WalkDir(generator, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip files that will be generated dynamically
		if strings.Contains(path, "PklProject") ||
			path == "generator/resources.pkl" ||
			path == "generator/resolvables.pkl" {
			return nil
		}

		targetPath := filepath.Join(tempDir, path)

		if d.IsDir() {
			return os.MkdirAll(targetPath, 0755)
		}

		data, err := fs.ReadFile(generator, path)
		if err != nil {
			return err
		}

		return os.WriteFile(targetPath, data, 0644)
	})
	if err != nil {
		return "", fmt.Errorf("failed to extract generator files: %w", err)
	}

	generatorDir := filepath.Join(tempDir, "generator")

	// Step 1: Generate PklProject with correct dependencies
	err = p.ProjectInit(generatorDir, includes, schemaLocation)
	if err != nil {
		return "", fmt.Errorf("failed to initialize project: %w", err)
	}

	// Re-resolve project deps to ensure deps.json reflects the
	// (possibly swapped-to-local) deps. ProjectInit's resolve runs but
	// in observed cases the pkl-go evaluator's first-pass project load
	// returns an empty package mapping for newly-added local deps; an
	// explicit second resolve normalizes deps.json so the evaluator
	// picks up the local v*/ subtrees.
	if len(versions) > 0 {
		depsJSON := filepath.Join(generatorDir, "PklProject.deps.json")
		if err := os.Remove(depsJSON); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("failed to clear stale deps.json: %w", err)
		}
		if err := pklrun.ProjectResolve(generatorDir, pklrun.WithPklCommand(bundledPklCommand())); err != nil {
			return "", fmt.Errorf("re-resolve after dep swap: %w", err)
		}
	}

	// Step 2: Generate imports.pkl from PklProject dependencies.
	importsProps := map[string]string{
		"schemaVersions": formatVersionsForProperty(versions),
	}
	if err := p.generatePklFileWithProps(generatorDir, "ImportsGenerator.pkl", "imports.pkl", importsProps); err != nil {
		return "", fmt.Errorf("failed to generate imports.pkl: %w", err)
	}

	// Step 3: Generate resources.pkl with dynamic imports
	if err := p.generatePklFile(generatorDir, "ResourcesGenerator.pkl", "resources.pkl"); err != nil {
		return "", fmt.Errorf("failed to generate resources.pkl: %w", err)
	}

	// Step 4: Generate resolvables.pkl with dynamic imports
	if err := p.generatePklFile(generatorDir, "ResolvablesGenerator.pkl", "resolvables.pkl"); err != nil {
		return "", fmt.Errorf("failed to generate resolvables.pkl: %w", err)
	}

	// Step 5: Run the main generator
	evaluator, cleanup, err := newSafeProjectEvaluator(
		context.Background(),
		&url.URL{Scheme: "file", Path: tempDir + "/generator"},
		pklgo.PreconfiguredOptions,
		pklgo.WithResourceReader(libExtension{}),
		func(opts *pklgo.EvaluatorOptions) {
			opts.Properties = properties
			opts.Logger = pklgo.NoopLogger
		},
	)
	if err != nil {
		return "", err
	}

	defer cleanup()

	textOutput, err := evaluator.EvaluateOutputText(context.Background(), pklgo.FileSource(filepath.Join(tempDir, "generator/runPklGenerator.pkl")))
	if err != nil {
		return "", fmt.Errorf("error evaluating PKL: %w", err)
	}

	return Format(textOutput), nil
}

// resolveIncludes returns the package specs to use for the PKL generator's temp
// PklProject. Caller-supplied options.Dependencies take priority; otherwise we
// build the spec list from options.LocalPluginDir (when SchemaLocation == Local
// and dir is non-empty) or fall back to remote-only.
func resolveIncludes(data *model.Forma, options *schema.SerializeOptions) []string {
	if options != nil && len(options.Dependencies) > 0 {
		return options.Dependencies
	}

	resolver := NewPackageResolver()

	schemaLocation := schema.SchemaLocationRemote
	if options != nil && options.SchemaLocation != "" {
		schemaLocation = options.SchemaLocation
	}
	if schemaLocation == schema.SchemaLocationLocal && options != nil && options.LocalPluginDir != "" {
		resolver.WithLocalSchemas(options.LocalPluginDir)
	}

	resolver.Add("formae", "pkl", formae.Version)
	for ns := range extractNamespaces(data) {
		resolver.Add(ns, ns, resolver.InstalledVersion(ns))
	}
	return resolver.GetPackageStrings()
}

// resolveSchemaVersions computes the per-namespace schema-version map used
// by ImportsGenerator's glob narrowing. Resolution per namespace, in order:
//
//  1. `ApiVersion` field inside Forma.Targets[].Config (the plugin's own
//     Config schema declares it; formae just reads the JSON blob and
//     looks for the convention key). When set, narrows to that subtree.
//  2. Filesystem scan — highest `v*/` subdir under the plugin's installed
//     schema/pkl/ (semver-aware when keys parse, lexical otherwise).
//
// A namespace with no version source is omitted; ImportsGenerator falls back
// to the unrestricted "@<pkg>/**/*.pkl" glob for that package. Plugins that
// don't ship a versioned schema layout behave as before — no `v*/` subdirs,
// no scan match, legacy unrestricted glob.
//
// Errors when two targets in the same namespace declare different
// ApiVersion values: ImportsGenerator can only narrow a package one way per
// extract pass, so honoring both stamps would silently corrupt resources
// bound to the losing target. Per-target dispatch is a future redesign;
// this gate prevents the silent-wrong case in the meantime.
//
// Returns nil when SchemaLocation is anything other than
// SchemaLocationLocal: versioned dispatch is a local-plugin-development
// feature, and remote (the production default) must never be silently
// flipped to local just because a CLI-host plugin tree happens to exist.
func resolveSchemaVersions(data *model.Forma, options *schema.SerializeOptions) (map[string]string, error) {
	if data == nil {
		return nil, nil
	}
	if options == nil || options.SchemaLocation != schema.SchemaLocationLocal {
		return nil, nil
	}
	out := map[string]string{}
	// Track which target first set each namespace's version so we can name
	// the conflicting targets in the error message.
	firstTargetLabel := map[string]string{}

	for _, t := range data.Targets {
		if t.Namespace == "" || len(t.Config) == 0 {
			continue
		}
		ver := apiVersionFromConfig(t.Config)
		if ver == "" {
			continue
		}
		ns := strings.ToLower(t.Namespace)
		if existing, ok := out[ns]; ok {
			if existing != ver {
				return nil, fmt.Errorf(
					"namespace %s has conflicting ApiVersion across targets "+
						"(target %q: %s, target %q: %s); split into separate "+
						"formae or align versions — per-target schema "+
						"dispatch is not yet supported",
					t.Namespace, firstTargetLabel[ns], existing, t.Label, ver,
				)
			}
			continue
		}
		out[ns] = ver
		firstTargetLabel[ns] = t.Label
	}

	pluginDir := ""
	if options != nil {
		pluginDir = options.LocalPluginDir
	}
	if pluginDir == "" {
		pluginDir = defaultPluginDir()
	}
	if pluginDir != "" {
		resolver := NewPackageResolver().WithLocalSchemas(pluginDir)
		for ns := range extractNamespaces(data) {
			if _, ok := out[ns]; ok {
				continue
			}
			if m := resolver.SchemaManifestForNamespace(ns); m != nil && m.Default != "" {
				out[ns] = m.Default
			}
		}
	}

	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// swapVersionedDepsToLocal rewrites the PklProject dep specs so that any
// namespace with a resolved schema version is pulled from its local
// install (where v*/ subtrees live) instead of a hub-published package
// (which today ships only the unified, unnarrowed schema). For each ns
// in `versions`, look up the local install via PackageResolver:
//   - If the namespace already has a remote `<plugin>.<name>@<ver>`
//     entry in `includes`, replace it with `local:<name>:<path>`.
//   - If the namespace isn't represented in `includes` at all (e.g. the
//     App layer queried the agent for installed plugins and got an
//     empty list — common for ephemeral test agents that don't have
//     orbital-installed plugins), append a `local:<name>:<path>` entry
//     so the temp PklProject can resolve `@<name>/v*/...` imports.
//
// Other deps pass through unchanged.
func swapVersionedDepsToLocal(includes []string, versions map[string]string, options *schema.SerializeOptions) []string {
	if len(versions) == 0 {
		return includes
	}
	pluginDir := ""
	if options != nil {
		pluginDir = options.LocalPluginDir
	}
	if pluginDir == "" {
		pluginDir = defaultPluginDir()
	}
	if pluginDir == "" {
		return includes
	}
	resolver := NewPackageResolver().WithLocalSchemas(pluginDir)

	out := make([]string, 0, len(includes))
	swapped := map[string]bool{}
	for _, inc := range includes {
		entry := inc
		for ns := range versions {
			localPath, pkgName := resolver.findLocalSchema(ns)
			if localPath == "" {
				continue
			}
			name := pkgName
			if name == "" {
				name = strings.ToLower(ns)
			}
			prefix := strings.ToLower(ns) + "." + strings.ToLower(name) + "@"
			if strings.HasPrefix(strings.ToLower(inc), prefix) {
				entry = "local:" + name + ":" + localPath
				swapped[strings.ToLower(ns)] = true
				break
			}
			// Already a local: entry for this namespace — keep as-is, mark
			// swapped so the bottom loop doesn't append a duplicate.
			localPrefix := "local:" + strings.ToLower(name) + ":"
			if strings.HasPrefix(strings.ToLower(inc), localPrefix) {
				swapped[strings.ToLower(ns)] = true
				break
			}
		}
		out = append(out, entry)
	}

	// Append any versioned namespace that wasn't already represented.
	for ns := range versions {
		nsLower := strings.ToLower(ns)
		if swapped[nsLower] {
			continue
		}
		localPath, pkgName := resolver.findLocalSchema(ns)
		if localPath == "" {
			continue
		}
		name := pkgName
		if name == "" {
			name = nsLower
		}
		out = append(out, "local:"+name+":"+localPath)
	}
	return out
}

// apiVersionFromConfig extracts the schema-version key (e.g. "v1.30")
// from a target's Config blob. Plugins opt in by emitting `ApiVersion`
// (or lowercase `apiVersion`) at the top level of their Config schema.
// Both casings are accepted — Pkl tends to render `fixed` properties
// with the original casing (PascalCase by formae convention) while raw
// user input may be lowercase.
//
// Returns "" when the blob is empty, malformed, or doesn't carry the
// key. Malformed JSON is treated as missing rather than fatal — drift
// in plugin Config shape must not poison extract.
func apiVersionFromConfig(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	for _, key := range []string{"ApiVersion", "apiVersion"} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// formatVersionsForProperty encodes a versions map as a comma-separated
// "pkg=ver,pkg=ver" string for the ImportsGenerator Pkl property. Returns
// the empty string when no versions are selected; ImportsGenerator then
// falls back to the unrestricted "@<pkg>/**/*.pkl" glob.
func formatVersionsForProperty(versions map[string]string) string {
	if len(versions) == 0 {
		return ""
	}
	parts := make([]string, 0, len(versions))
	for k, v := range versions {
		if k == "" || v == "" {
			continue
		}
		parts = append(parts, strings.ToLower(k)+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// extractNamespaces extracts unique namespaces from the data.
// It handles both *model.Resource and *model.Forma types.
func extractNamespaces(data any) map[string]struct{} {
	namespaces := make(map[string]struct{})

	switch v := data.(type) {
	case *model.Resource:
		ns := strings.ToLower(v.Namespace())
		namespaces[ns] = struct{}{}
	case *model.Forma:
		for _, res := range v.Resources {
			ns := strings.ToLower(res.Namespace())
			namespaces[ns] = struct{}{}
		}
	}

	return namespaces
}

// generatePklFile evaluates a PKL generator file and writes the output to a target file.
// This is used in the multi-stage generation pipeline to create imports.pkl, resources.pkl, etc.
func (p PKL) generatePklFile(generatorDir, generatorName, outputName string) error {
	return p.generatePklFileWithProps(generatorDir, generatorName, outputName, nil)
}

// generatePklFileWithProps is generatePklFile but also injects external Pkl
// properties into the evaluator. Used to thread per-call inputs (e.g. the
// active schema version per package) into generator templates.
func (p PKL) generatePklFileWithProps(generatorDir, generatorName, outputName string, props map[string]string) error {
	evaluator, cleanup, err := newSafeProjectEvaluator(
		context.Background(),
		&url.URL{Scheme: "file", Path: generatorDir},
		pklgo.PreconfiguredOptions,
		func(opts *pklgo.EvaluatorOptions) {
			opts.Logger = pklgo.NoopLogger
			if len(props) > 0 {
				if opts.Properties == nil {
					opts.Properties = map[string]string{}
				}
				for k, v := range props {
					opts.Properties[k] = v
				}
			}
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create evaluator for %s: %w", generatorName, err)
	}
	defer cleanup()

	result, err := evaluator.EvaluateOutputText(
		context.Background(),
		pklgo.FileSource(filepath.Join(generatorDir, generatorName)),
	)
	if err != nil {
		return fmt.Errorf("failed to evaluate %s: %w", generatorName, err)
	}

	outputPath := filepath.Join(generatorDir, outputName)
	if err := os.WriteFile(outputPath, []byte(result), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputName, err)
	}

	return nil
}

// preprocessFormaEmbeds returns a shallow copy of forma with every resource's
// Properties pre-processed: any {"$embed":true,"$template":"..."} object whose
// template contains framed RS+base64+US spans is replaced with
// {"$embed":true,"$templateParts":[...]} where the parts array interleaves
// literal string segments (as JSON strings) with decoded $res envelope maps
// (as JSON objects). This avoids the PKL generator having to parse base64 or
// binary control characters inside PKL string literals.
func preprocessFormaEmbeds(f *model.Forma) (*model.Forma, error) {
	if f == nil {
		return nil, nil
	}
	out := *f // shallow copy
	out.Resources = make([]model.Resource, len(f.Resources))
	for i, r := range f.Resources {
		nr := r
		if len(r.Properties) > 0 {
			processed, err := preprocessEmbedInJSON(r.Properties)
			if err != nil {
				return nil, fmt.Errorf("resource %q: %w", r.Label, err)
			}
			nr.Properties = processed
		}
		out.Resources[i] = nr
	}
	return &out, nil
}

// preprocessEmbedInJSON walks arbitrary JSON and rewrites every
// {"$embed":true,"$template":"..."} object, replacing "$template" with
// "$templateParts" — a JSON array of alternating literal strings and $res maps.
func preprocessEmbedInJSON(raw json.RawMessage) (json.RawMessage, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw, nil // not parseable — leave unchanged
	}
	out, err := rewriteEmbedValue(v)
	if err != nil {
		return nil, err
	}
	result, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// rewriteEmbedValue recurses into arbitrary JSON values and rewrites embed objects.
func rewriteEmbedValue(v any) (any, error) {
	switch val := v.(type) {
	case map[string]any:
		// Check for an embed node: {"$embed": true, "$template": "..."}
		if embedFlag, ok := val["$embed"].(bool); ok && embedFlag {
			if tmpl, ok := val["$template"].(string); ok {
				parts, err := splitEmbedTemplate(tmpl)
				if err != nil {
					// Template is malformed — leave the node unchanged so
					// downstream can surface the error rather than silently
					// dropping embed fields.
					return val, nil
				}
				result := map[string]any{
					"$embed":         true,
					"$templateParts": parts,
				}
				return result, nil
			}
		}
		// Recurse into all keys
		out := make(map[string]any, len(val))
		for k, child := range val {
			rewritten, err := rewriteEmbedValue(child)
			if err != nil {
				return nil, err
			}
			out[k] = rewritten
		}
		return out, nil
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			rewritten, err := rewriteEmbedValue(item)
			if err != nil {
				return nil, err
			}
			out[i] = rewritten
		}
		return out, nil
	default:
		return v, nil
	}
}

// splitEmbedTemplate scans a $embed.$template string for framed RS+base64+US
// spans and returns an ordered slice of parts. Each part is either a plain
// string (a literal segment between/around spans) or a map[string]any (the
// decoded $res envelope for a span). Empty literal strings between adjacent
// spans are included so that the PKL renderer can join them faithfully.
func splitEmbedTemplate(tmpl string) ([]any, error) {
	spans, err := model.ScanEmbedSpans(tmpl)
	if err != nil {
		return nil, err
	}

	var parts []any
	cursor := 0
	for _, span := range spans {
		// Literal segment before this span
		literal := tmpl[cursor:span.Start]
		parts = append(parts, literal)

		// Decode the envelope JSON to a map
		var env map[string]any
		if jsonErr := json.Unmarshal([]byte(span.EnvelopeJSON), &env); jsonErr != nil {
			return nil, fmt.Errorf("embed: span envelope is not valid JSON: %w", jsonErr)
		}
		parts = append(parts, env)
		cursor = span.End
	}
	// Trailing literal after the last span
	parts = append(parts, tmpl[cursor:])
	return parts, nil
}
