// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package descriptors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePklProject(t *testing.T, dir, contents string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	pkl := filepath.Join(dir, "PklProject")
	if err := os.WriteFile(pkl, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	return pkl
}

func TestStagePluginDependencies_RewritesURIForm(t *testing.T) {
	tmp := t.TempDir()

	// Plugin's PklProject pins formae at the flat path (URI form). After
	// staging, the plugin's formae dep should point at the agent-supplied URL
	// so `@formae` references inside plugin source files resolve to the
	// agent's formae.
	pluginDir := filepath.Join(tmp, "src", "aws")
	pluginPkl := writePklProject(t, pluginDir, `amends "pkl:Project"

package {
  name = "aws"
}

dependencies {
  ["formae"] {
    uri = "package://hub.platform.engineering/plugins/pkl/schema/pkl/formae/formae@0.84.0"
  }
}
`)

	stageDir := filepath.Join(tmp, "stage")
	if err := os.MkdirAll(stageDir, 0755); err != nil {
		t.Fatal(err)
	}

	agentURL := "package://hub.platform.engineering/plugins/pkl/schema/pkl/formae/dev/formae@0.85.0"
	out, err := stagePluginDependencies(stageDir, []Dependency{
		{Name: "formae", Value: agentURL},
		{Name: "aws", Value: pluginPkl},
	})
	if err != nil {
		t.Fatalf("stagePluginDependencies: %v", err)
	}

	// formae dep is returned untouched.
	if out[0].Value != agentURL {
		t.Errorf("formae dep should be untouched, got %q", out[0].Value)
	}

	// aws dep now points at the staged copy.
	stagedPkl := out[1].Value
	if !strings.HasPrefix(stagedPkl, stageDir) {
		t.Errorf("aws dep should point inside stageDir %q, got %q", stageDir, stagedPkl)
	}

	// Staged PklProject's formae dep is rewritten to the agent URL.
	contents, err := os.ReadFile(stagedPkl)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), agentURL) {
		t.Errorf("staged PklProject should contain agent URL %q; contents:\n%s", agentURL, contents)
	}
	if strings.Contains(string(contents), "formae@0.84.0") {
		t.Errorf("staged PklProject should not retain the old formae version pin; contents:\n%s", contents)
	}
}

func TestStagePluginDependencies_RewritesImportForm(t *testing.T) {
	tmp := t.TempDir()

	// Plugin uses `import("...")` form (typical for in-tree fakeaws).
	pluginDir := filepath.Join(tmp, "src", "fakeaws")
	pluginPkl := writePklProject(t, pluginDir, `amends "pkl:Project"

package {
  name = "fakeaws"
}

dependencies {
  ["formae"] = import("../../../../some/relative/path/PklProject")
}
`)

	stageDir := filepath.Join(tmp, "stage")
	if err := os.MkdirAll(stageDir, 0755); err != nil {
		t.Fatal(err)
	}

	agentLocalPath := "/abs/path/to/agent/formae/PklProject"
	out, err := stagePluginDependencies(stageDir, []Dependency{
		{Name: "formae", Value: agentLocalPath},
		{Name: "fakeaws", Value: pluginPkl},
	})
	if err != nil {
		t.Fatal(err)
	}

	stagedPkl := out[1].Value
	contents, err := os.ReadFile(stagedPkl)
	if err != nil {
		t.Fatal(err)
	}
	expectedEntry := `["formae"] = import("` + agentLocalPath + `")`
	if !strings.Contains(string(contents), expectedEntry) {
		t.Errorf("staged PklProject should contain rewritten import:\n  want: %s\n  got:\n%s", expectedEntry, contents)
	}
	if strings.Contains(string(contents), "../../../../some/relative/path") {
		t.Error("staged PklProject should not retain the old relative import path")
	}
}

func TestStagePluginDependencies_PassesThroughRemotePluginDeps(t *testing.T) {
	// Plugin deps with `package://` Values are pass-through — only locally-
	// pathed plugin deps need staging because they're the ones whose own
	// PklProject we can rewrite.
	tmp := t.TempDir()
	out, err := stagePluginDependencies(tmp, []Dependency{
		{Name: "formae", Value: "package://hub.platform.engineering/plugins/pkl/schema/pkl/formae/dev/formae@0.85.0"},
		{Name: "azure", Value: "package://hub.platform.engineering/plugins/azure/schema/pkl/azure/azure@0.1.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Value != "package://hub.platform.engineering/plugins/pkl/schema/pkl/formae/dev/formae@0.85.0" {
		t.Errorf("formae dep should be untouched")
	}
	if out[1].Value != "package://hub.platform.engineering/plugins/azure/schema/pkl/azure/azure@0.1.0" {
		t.Errorf("remote-uri plugin dep should be untouched")
	}
}

func TestStagePluginDependencies_NoFormaeDep(t *testing.T) {
	// If callers don't supply a formae dep we have nothing to override
	// against — pass-through.
	tmp := t.TempDir()
	in := []Dependency{{Name: "aws", Value: "/some/path/PklProject"}}
	out, err := stagePluginDependencies(tmp, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0] != in[0] {
		t.Errorf("expected pass-through, got %+v", out)
	}
}

func TestRewriteFormaeDepInFile_CopiesNestedDirectoriesIntoStage(t *testing.T) {
	// Source plugin has nested source files — copyTree should bring them
	// along so PKL can find them when evaluating @plugin/...
	tmp := t.TempDir()
	pluginDir := filepath.Join(tmp, "src", "aws")
	writePklProject(t, pluginDir, `dependencies {
  ["formae"] { uri = "package://hub.platform.engineering/plugins/pkl/schema/pkl/formae/formae@0.84.0" }
}`)

	// Nested .pkl file under the plugin
	if err := os.MkdirAll(filepath.Join(pluginDir, "ec2"), 0755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(pluginDir, "ec2", "vpc.pkl")
	if err := os.WriteFile(nested, []byte("module aws.ec2.vpc\n"), 0644); err != nil {
		t.Fatal(err)
	}

	stageDir := filepath.Join(tmp, "stage")
	out, err := stagePluginDependencies(stageDir, []Dependency{
		{Name: "formae", Value: "package://hub.platform.engineering/plugins/pkl/schema/pkl/formae/dev/formae@0.85.0"},
		{Name: "aws", Value: filepath.Join(pluginDir, "PklProject")},
	})
	if err != nil {
		t.Fatal(err)
	}

	stagedNested := filepath.Join(filepath.Dir(out[1].Value), "ec2", "vpc.pkl")
	if _, err := os.Stat(stagedNested); err != nil {
		t.Errorf("nested file %q was not staged: %v", stagedNested, err)
	}
}
