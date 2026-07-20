// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package bundleexamples

import (
	"regexp"
	"strings"
)

var (
	reqAssign = regexp.MustCompile(`requirements\s*=`)
	nameDecl  = regexp.MustCompile(`name\s*=\s*"([^"]+)"`)
)

// requirementsBlock returns the text between the braces of the
// `requirements = new Listing<...> { ... }` assignment, brace-matched so
// nested `new { ... }` records are included.
func requirementsBlock(text string) (string, bool) {
	loc := reqAssign.FindStringIndex(text)
	if loc == nil {
		return "", false
	}
	open := strings.IndexByte(text[loc[1]:], '{')
	if open < 0 {
		return "", false
	}
	start := loc[1] + open
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start+1 : i], true
			}
		}
	}
	return "", false
}

// Members extracts each requirement's name from the standard bundle Opkgfile,
// scoped to the brace-matched `requirements` Listing so the metapackage's own
// `name` and any out-of-block `name` fields are excluded.
func Members(opkgfileText string) []string {
	block, ok := requirementsBlock(opkgfileText)
	if !ok {
		return nil
	}
	var out []string
	for _, m := range nameDecl.FindAllStringSubmatch(block, -1) {
		out = append(out, m[1])
	}
	return out
}
