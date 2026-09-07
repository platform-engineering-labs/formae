// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/masterminds/semver"
)

// remoteURIPattern matches the URI shape emitted by PklProjectTemplate.pkl:
// package://<host>/plugins/<plugin>/schema/pkl/<name>/<name>@<version>
var remoteURIPattern = regexp.MustCompile(`^package://[^/]+/plugins/([^/]+)/schema/pkl/[^/]+/([^@]+)@(.+)$`)

// importPattern matches: ["<name>"] = import("<path>")
var importPattern = regexp.MustCompile(`\[\s*"([^"]+)"\s*\]\s*=\s*import\(\s*"([^"]+)"\s*\)`)

// uriPattern matches the inner uri assignment of a remote dep:
// uri = "package://..."
var uriPattern = regexp.MustCompile(`uri\s*=\s*"([^"]+)"`)

// nameKeyPattern matches the start of a remote dep block: ["<name>"] {
var nameKeyPattern = regexp.MustCompile(`\[\s*"([^"]+)"\s*\]\s*\{`)

// formaeCoreDepPrefix is the package-spec prefix for the formae core schema
// dependency, matching what PackageResolver emits (see buildDependencyStrings).
const formaeCoreDepPrefix = "pkl.formae@"

// bumpFormaeCoreDep returns a copy of deps with the formae core dependency
// pinned to version, and the version it previously carried. current is empty
// when deps has no formae core dep (nothing bumped). Only the in-memory spec
// list is touched — no file is written.
func bumpFormaeCoreDep(deps []string, version string) (out []string, current string) {
	out = make([]string, len(deps))
	copy(out, deps)
	for i, d := range out {
		if strings.HasPrefix(d, formaeCoreDepPrefix) {
			current = strings.TrimPrefix(d, formaeCoreDepPrefix)
			out[i] = formaeCoreDepPrefix + version
			return out, current
		}
	}
	return out, ""
}

// depKey returns the PklProject dependency key (the ["<key>"]) for a package
// spec: the <name> in "<plugin>.<name>@<version>" or "local:<name>:<path>".
func depKey(spec string) string {
	if rest, ok := strings.CutPrefix(spec, "local:"); ok {
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			return rest[:i]
		}
		return rest
	}
	dot := strings.IndexByte(spec, '.')
	at := strings.IndexByte(spec, '@')
	if dot < 0 || at < 0 || at < dot {
		return spec
	}
	return spec[dot+1 : at]
}

// missingPluginDeps returns the specs in computed whose dependency key is absent
// from parsed, preserving computed's order. formae core is excluded — it is
// governed by the schema-version rule, not added here.
func missingPluginDeps(parsed, computed []string) []string {
	have := make(map[string]bool, len(parsed))
	for _, p := range parsed {
		have[depKey(p)] = true
	}
	var missing []string
	for _, c := range computed {
		if k := depKey(c); k != "formae" && !have[k] {
			missing = append(missing, c)
		}
	}
	return missing
}

// renderDepBlock renders one package spec as its PklProject dependency entry,
// matching PklProjectTemplate.pkl: remote specs become a uri block, local specs
// become an import(). Two-space indented, no trailing newline.
func renderDepBlock(spec string) (string, error) {
	if rest, ok := strings.CutPrefix(spec, "local:"); ok {
		i := strings.IndexByte(rest, ':')
		if i < 0 {
			return "", fmt.Errorf("malformed local dep spec %q", spec)
		}
		name, path := rest[:i], rest[i+1:]
		return fmt.Sprintf("  [%q] = import(%q)", name, path), nil
	}
	dot := strings.IndexByte(spec, '.')
	at := strings.IndexByte(spec, '@')
	if dot < 0 || at < 0 || at < dot {
		return "", fmt.Errorf("malformed remote dep spec %q", spec)
	}
	plugin, name, version := spec[:dot], spec[dot+1:at], spec[at+1:]
	uri := fmt.Sprintf("package://hub.platform.engineering/plugins/%s/schema/pkl/%s/%s@%s", plugin, name, name, version)
	return fmt.Sprintf("  [%q] {\n    uri = %q\n  }", name, uri), nil
}

// addDepsToPklProject inserts the given package specs as dependency entries into
// the PklProject at path, before the closing brace of its dependencies block
// (appending a fresh block if the file has none). Everything else in the file is
// preserved.
func addDepsToPklProject(path string, specs []string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read PklProject %q: %w", path, err)
	}
	content := string(data)

	blocks := make([]string, 0, len(specs))
	for _, s := range specs {
		b, rErr := renderDepBlock(s)
		if rErr != nil {
			return rErr
		}
		blocks = append(blocks, b)
	}
	insert := strings.Join(blocks, "\n")

	depsStart := strings.Index(content, "dependencies")
	if depsStart < 0 {
		return os.WriteFile(path, []byte(content+"\ndependencies {\n"+insert+"\n}\n"), 0640)
	}
	open := strings.Index(content[depsStart:], "{")
	if open < 0 {
		return fmt.Errorf("malformed dependencies block in %q", path)
	}
	body, ok := scanBalancedBraces(content[depsStart+open:])
	if !ok {
		return fmt.Errorf("unbalanced dependencies block in %q", path)
	}
	closeIdx := depsStart + open + 1 + len(body) // index of the block's closing '}'

	prefix := strings.TrimRight(content[:closeIdx], " \t")
	if !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	return os.WriteFile(path, []byte(prefix+insert+"\n"+content[closeIdx:]), 0640)
}

// coreSchemaVersion strips any prerelease/build metadata from a binary version
// so it names the published core schema package. Schemas are only ever
// published at a base X.Y.Z — a prerelease binary like "0.88.0-dev.7" still
// resolves its schema against "0.88.0". Returns v unchanged if it carries no
// suffix.
func coreSchemaVersion(v string) string {
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		return v[:i]
	}
	return v
}

// RULE: extracted files require the target PklProject to pin formae core at
// 0.88.0 or newer.
//
// WHY 0.88.0: extracted forma files now use `extends "@formae/forma.pkl"`
// instead of `amends`. The extends-based shape landed in formae core 0.88.0
// and is NOT understood by earlier versions — a PklProject pinning formae
// < 0.88.0 cannot evaluate a freshly extracted file (the pkl evaluator fails
// to resolve the extends). So we require >= 0.88.0 and nag the user to update.
//
// This is a FIXED LITERAL tied to that specific schema change — it is NOT
// derived from the running binary version and must not drift with each
// release. The rule is "0.88.0", full stop. Only change it if a later,
// incompatible schema-shape change raises the floor again.
const requiredFormaeSchemaVersion = "0.88.0"

// isOlderVersion reports whether current is a strictly lower semver than
// target. It returns false when current is empty (no dep found), when either
// side fails to parse, or when current is equal to or newer than target — the
// nag fires only when the on-disk project is genuinely behind.
func isOlderVersion(current, target string) bool {
	if current == "" {
		return false
	}
	c, errC := semver.NewVersion(current)
	t, errT := semver.NewVersion(target)
	if errC != nil || errT != nil {
		return false
	}
	return c.LessThan(t)
}

// parsePklProjectDeps reads the `dependencies { ... }` block from an existing
// PklProject file and returns the entries as package specs in the format
// emitted by PackageResolver:
//   - remote: "<plugin>.<name>@<version>"
//   - local:  "local:<name>:<absolute path>"
//
// Returns an error if the file cannot be read.
func parsePklProjectDeps(pklProjectPath string) ([]string, error) {
	data, err := os.ReadFile(pklProjectPath)
	if err != nil {
		return nil, fmt.Errorf("read PklProject %q: %w", pklProjectPath, err)
	}

	content := string(data)

	// Find the dependencies { ... } block. We don't try to be a full Pkl parser —
	// we just scan from "dependencies {" to its matching "}".
	depsStart := strings.Index(content, "dependencies")
	if depsStart < 0 {
		return nil, nil
	}
	openBrace := strings.Index(content[depsStart:], "{")
	if openBrace < 0 {
		return nil, fmt.Errorf("malformed dependencies block in %q", pklProjectPath)
	}
	depsBody, ok := scanBalancedBraces(content[depsStart+openBrace:])
	if !ok {
		return nil, fmt.Errorf("unbalanced dependencies block in %q", pklProjectPath)
	}

	var out []string

	// Local deps: ["<name>"] = import("<path>")
	for _, m := range importPattern.FindAllStringSubmatch(depsBody, -1) {
		out = append(out, fmt.Sprintf("local:%s:%s", m[1], m[2]))
	}

	// Remote deps: ["<name>"] { uri = "package://..." }
	// Walk each ["<name>"] { block and look for the uri inside it.
	starts := nameKeyPattern.FindAllStringSubmatchIndex(depsBody, -1)
	for _, s := range starts {
		// Block body starts after the opening brace at index s[1]-1.
		blockBody, ok := scanBalancedBraces(depsBody[s[1]-1:])
		if !ok {
			continue
		}
		uriMatch := uriPattern.FindStringSubmatch(blockBody)
		if len(uriMatch) < 2 {
			continue
		}
		uriParts := remoteURIPattern.FindStringSubmatch(uriMatch[1])
		if len(uriParts) != 4 {
			continue
		}
		plugin, pkgName, version := uriParts[1], uriParts[2], uriParts[3]
		out = append(out, fmt.Sprintf("%s.%s@%s", plugin, pkgName, version))
	}

	return out, nil
}

// scanBalancedBraces returns the substring inside the first balanced { ... }
// pair starting at the first '{' in s, and a bool indicating success.
// The returned substring excludes the outer braces.
func scanBalancedBraces(s string) (string, bool) {
	open := strings.Index(s, "{")
	if open < 0 {
		return "", false
	}
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[open+1 : i], true
			}
		}
	}
	return "", false
}
