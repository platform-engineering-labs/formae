// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package bundleexamples

import "regexp"

const hub = "package://hub.platform.engineering/plugins"

// selfURI is the published self-schema package pin for a member. The path uses
// the member id as both the Hub plugin id segment and the Pkl package name
// (verified against the aws and k8s schema PklProjects, whose package.name
// equals the member id). Only the GitHub repo slug diverges from the member id
// (e.g. formae-plugin-kubernetes for k8s), and that is resolved via the Hub API
// in the orchestrator, never here.
func selfURI(member, version string) string {
	return hub + "/" + member + "/schema/pkl/" + member + "/" + member + "@" + version
}

// RewritePklProject rewrites an example PklProject's dependency declarations so
// the bundled examples pin to published packages instead of relative imports.
//
//  1. The self-schema import form `["<alias>"] = import("../.../schema/pkl/...")`
//     becomes a package block `["<alias>"] { uri = "<selfURI>@<version>" }`. The
//     alias is captured verbatim rather than assumed to equal the member id, so a
//     divergent alias (e.g. ["kubernetes"] for member "k8s") is preserved while
//     the URI path uses the member/package id.
//  2. An already-present self-dep package pin inside a `uri = "..."` value has its
//     version bumped to <version>.
//  3. A formae pin inside a `uri = "..."` value is bumped to <formaeVersion>.
//
// All rewrites are scoped to `uri = "..."` values (and the import form), so
// comments, cross-plugin pins, and local `./` project imports are never touched.
func RewritePklProject(text, member, version, formaeVersion string) string {
	q := regexp.QuoteMeta(member)
	h := regexp.QuoteMeta(hub)

	// Rule 1: self-schema import form -> package block. The import must climb via
	// "../" into a "schema/pkl" path (local "./" project imports and cross-plugin
	// package pins do not match). The alias key is captured (group 2) and
	// preserved. \s* tolerates newlines between tokens. (?m) so ^ and the
	// leading-indent capture (group 1) are per-line.
	selfImport := regexp.MustCompile(
		`(?m)^([ \t]*)\["([^"]+)"\]\s*=\s*import\(\s*"\.\.[^"]*schema/pkl[^"]*"\s*\)`)
	text = selfImport.ReplaceAllString(text,
		"${1}[\"${2}\"] {\n${1}  uri = \""+selfURI(member, version)+"\"\n${1}}")

	// Rule 2: self-dep package pin inside a uri value -> version. Keyed on the
	// member/package path so cross-plugin pins are untouched. The old version is
	// any run of non-quote chars, so build metadata is handled.
	selfPin := regexp.MustCompile(
		`(uri\s*=\s*"[^"]*` + h + `/` + q + `/schema/pkl/` + q + `/` + q + `)@[^"]*(")`)
	text = selfPin.ReplaceAllString(text, "${1}@"+version+"${2}")

	// Rule 3: formae pin inside a uri value -> this build's formae version.
	formaePin := regexp.MustCompile(
		`(uri\s*=\s*"[^"]*` + h + `/pkl/schema/pkl/formae/formae)@[^"]*(")`)
	text = formaePin.ReplaceAllString(text, "${1}@"+formaeVersion+"${2}")

	return text
}
