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
