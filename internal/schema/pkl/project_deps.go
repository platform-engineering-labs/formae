// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"fmt"
	"os"
	"regexp"
	"strings"
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

// formaeCoreVersionPattern matches the version segment of the formae core
// dependency URI inside a PklProject:
//
//	package://<host>/plugins/pkl/schema/pkl/formae/formae@<version>
//
// Only the core package (name "formae") ends in `/formae/formae@`, so plugin
// deps are never touched.
var formaeCoreVersionPattern = regexp.MustCompile(`(package://[^"]+/formae/formae@)[^"\s]+`)

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

// rewriteFormaeCoreVersion pins the formae core dependency in the PklProject
// at path to version. It returns true when the file changed (a formae core
// dep existed and its version differed). A PklProject without a formae core
// dep, or one already at version, is left untouched and returns (false, nil).
func rewriteFormaeCoreVersion(path, version string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read PklProject %q: %w", path, err)
	}
	replaced := formaeCoreVersionPattern.ReplaceAllString(string(data), "${1}"+version)
	if replaced == string(data) {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(replaced), 0640); err != nil {
		return false, fmt.Errorf("write PklProject %q: %w", path, err)
	}
	return true, nil
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
