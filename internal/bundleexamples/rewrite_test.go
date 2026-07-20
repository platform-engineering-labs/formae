// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package bundleexamples

import (
	"regexp"
	"strings"
	"testing"
)

func self(p, v string) string {
	return "package://hub.platform.engineering/plugins/" + p + "/schema/pkl/" + p + "/" + p + "@" + v
}
func formaePkg(v string) string {
	return "package://hub.platform.engineering/plugins/pkl/schema/pkl/formae/formae@" + v
}

func TestRewriteSelfImportAndFormae(t *testing.T) {
	src := "dependencies {\n  [\"aws\"] = import(\"../schema/pkl/PklProject\")\n  [\"formae\"] {\n    uri = \"" + formaePkg("0.84.0") + "\"\n  }\n}"
	out := RewritePklProject(src, "aws", "0.1.14", "0.87.1")
	if !strings.Contains(out, "uri = \""+self("aws", "0.1.14")+"\"") {
		t.Fatalf("self import not rewritten:\n%s", out)
	}
	if regexp.MustCompile(`import\(`).MatchString(out) {
		t.Fatalf("self import survived:\n%s", out)
	}
	if !strings.Contains(out, "uri = \""+formaePkg("0.87.1")+"\"") {
		t.Fatalf("formae not bumped:\n%s", out)
	}
}

func TestRewriteVariableDepthMainAndIndent(t *testing.T) {
	for _, src := range []string{
		"  [\"k8s\"] = import(\"../../schema/pkl/generated/PklProject\")",
		"  [\"k8s\"] = import(\"../../schema/pkl/main/PklProject\")",
		"  [\"k8s\"] = import(\"../../../schema/pkl/generated/PklProject\")",
	} {
		out := RewritePklProject(src, "k8s", "0.1.7", "0.87.1")
		if !strings.Contains(out, "uri = \""+self("k8s", "0.1.7")+"\"") {
			t.Fatalf("not rewritten: %q -> %q", src, out)
		}
		if !strings.HasPrefix(out, "  [\"k8s\"] {") {
			t.Fatalf("indent not preserved: %q", out)
		}
	}
}

func TestRewriteMultilineImport(t *testing.T) {
	src := "  [\"aws\"] =\n    import(\"../schema/pkl/PklProject\")"
	out := RewritePklProject(src, "aws", "0.1.14", "0.87.1")
	if !strings.Contains(out, self("aws", "0.1.14")) {
		t.Fatalf("multiline import not rewritten:\n%s", out)
	}
}

func TestRewriteExistingPinCrossPluginAndComments(t *testing.T) {
	az := "  [\"azure\"] {\n    uri = \"" + self("azure", "0.1.2") + "\"\n  }"
	if !strings.Contains(RewritePklProject(az, "azure", "0.1.7", "0.87.1"), self("azure", "0.1.7")) {
		t.Fatal("azure self pin not bumped")
	}
	// k8s: cross-plugin aws pin unchanged; k8s import rewritten; comment untouched.
	k := "// see aws@0.1.7 for details\n  [\"aws\"] {\n    uri = \"" + self("aws", "0.1.7") + "\"\n  }\n  [\"k8s\"] = import(\"../../schema/pkl/generated/PklProject\")"
	out := RewritePklProject(k, "k8s", "0.1.7", "0.87.1")
	if !strings.Contains(out, self("aws", "0.1.7")) {
		t.Fatal("cross-plugin aws pin altered")
	}
	if !strings.Contains(out, "// see aws@0.1.7 for details") {
		t.Fatal("comment was rewritten")
	}
	if !strings.Contains(out, self("k8s", "0.1.7")) {
		t.Fatal("k8s self import not rewritten")
	}
}

func TestRewriteLeavesIntraImports(t *testing.T) {
	src := "  [\"clusters\"] = import(\"./clusters/PklProject\")"
	if got := RewritePklProject(src, "k8s", "0.1.7", "0.87.1"); got != src {
		t.Fatalf("intra ./ import altered: %q", got)
	}
}

// TestRewriteRealK8sRootShape exercises the exact shape of
// formae-plugin-kubernetes examples/PklProject at its release tag: the self
// dependency is an import aliased ["k8s"] climbing to ../schema/pkl; the block
// also carries cross-plugin package pins (aws/azure/gcp/oci/grafana) that MUST
// remain untouched, local ./ project imports that MUST remain imports, and a
// formae pin that MUST be bumped to this build's version.
func TestRewriteRealK8sRootShape(t *testing.T) {
	src := `amends "pkl:Project"

dependencies {
  ["formae"] {
    uri = "` + formaePkg("0.86.1") + `"
  }
  ["aws"] {
    uri = "` + self("aws", "0.1.7") + `"
  }
  ["azure"] {
    uri = "` + self("azure", "0.1.3") + `"
  }
  ["gcp"] {
    uri = "` + self("gcp", "0.1.7") + `"
  }
  ["oci"] {
    uri = "` + self("oci", "0.1.3") + `"
  }
  ["grafana"] {
    uri = "` + self("grafana", "0.1.3") + `"
  }
  ["k8s"] = import("../schema/pkl/PklProject")
  ["clusters"] = import("./clusters/PklProject")
  ["apps"] = import("./apps/PklProject")
  ["formae-formations"] = import("./formations/PklProject")
}

evaluatorSettings {
  externalResourceReaders {
    ["reader+helm"] {
      executable = "pkl-reader-helm"
    }
  }
}`
	out := RewritePklProject(src, "k8s", "0.1.7", "0.87.1")

	// self import -> package pin at the build tag
	if !strings.Contains(out, "[\"k8s\"] {") || !strings.Contains(out, self("k8s", "0.1.7")) {
		t.Fatalf("k8s self import not rewritten to a package pin:\n%s", out)
	}
	// formae bumped
	if !strings.Contains(out, formaePkg("0.87.1")) || strings.Contains(out, formaePkg("0.86.1")) {
		t.Fatalf("formae pin not bumped:\n%s", out)
	}
	// cross-plugin pins untouched
	for _, p := range []struct{ n, v string }{{"aws", "0.1.7"}, {"azure", "0.1.3"}, {"gcp", "0.1.7"}, {"oci", "0.1.3"}, {"grafana", "0.1.3"}} {
		if !strings.Contains(out, self(p.n, p.v)) {
			t.Fatalf("cross-plugin pin %s@%s was altered:\n%s", p.n, p.v, out)
		}
	}
	// local ./ project imports remain imports
	for _, imp := range []string{`["clusters"] = import("./clusters/PklProject")`, `["apps"] = import("./apps/PklProject")`, `["formae-formations"] = import("./formations/PklProject")`} {
		if !strings.Contains(out, imp) {
			t.Fatalf("local project import altered: %q\n%s", imp, out)
		}
	}
	// no self-schema import escape survives
	if regexp.MustCompile(`import\(\s*"\.\.[^"]*schema/pkl`).MatchString(out) {
		t.Fatalf("self-schema escape import survived:\n%s", out)
	}
	// evaluatorSettings untouched
	if !strings.Contains(out, `executable = "pkl-reader-helm"`) {
		t.Fatalf("evaluatorSettings altered:\n%s", out)
	}
}

// TestRewriteRealAwsShape exercises the aws examples/PklProject shape: a leading
// comment before the self import, and a single formae pin.
func TestRewriteRealAwsShape(t *testing.T) {
	src := `dependencies {
  // Reference local plugin schema during development
  ["aws"] = import("../schema/pkl/PklProject")

  // Formae schema - fetched from public registry
  ["formae"] {
    uri = "` + formaePkg("0.87.0") + `"
  }
}`
	out := RewritePklProject(src, "aws", "0.1.14", "0.87.1")
	if !strings.Contains(out, self("aws", "0.1.14")) {
		t.Fatalf("aws self import not rewritten:\n%s", out)
	}
	if !strings.Contains(out, formaePkg("0.87.1")) {
		t.Fatalf("formae not bumped:\n%s", out)
	}
	// comments preserved verbatim
	if !strings.Contains(out, "// Reference local plugin schema during development") ||
		!strings.Contains(out, "// Formae schema - fetched from public registry") {
		t.Fatalf("comment was altered:\n%s", out)
	}
}

// TestRewriteAliasDivergesFromMember is the carry-forward robustness guard: if
// an example ever aliases its self-schema import under a name other than the
// bundle member id (e.g. ["kubernetes"] for member "k8s"), the alias key MUST be
// preserved while the package URI path uses the member/package id.
func TestRewriteAliasDivergesFromMember(t *testing.T) {
	src := `  ["kubernetes"] = import("../schema/pkl/PklProject")`
	out := RewritePklProject(src, "k8s", "0.1.7", "0.87.1")
	if !strings.Contains(out, `["kubernetes"] {`) {
		t.Fatalf("diverging alias not preserved:\n%s", out)
	}
	if !strings.Contains(out, self("k8s", "0.1.7")) {
		t.Fatalf("package URI should use member id k8s:\n%s", out)
	}
}
