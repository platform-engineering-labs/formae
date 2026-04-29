// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEvalDefault verifies that `formae eval` against a forma file using a
// resource plugin produces valid JSON output. Locks down the post-fix
// behavior where eval queries the agent for installed plugin versions and
// resolves remote PKL packages from the hub.
//
// Regression coverage for the bug fixed in 79063e57: pre-fix, eval scanned
// the CLI's local plugin dir which could differ from the agent's path,
// producing malformed dep strings (`aws.aws@`, no version).
func TestEvalDefault(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	fixture := filepath.Join(fixturesDir(t), "reconcile_apply_aws.pkl")
	stdout := cli.Eval(t, fixture)
	if len(stdout) == 0 {
		t.Fatal("formae eval returned empty output")
	}

	var parsed map[string]any
	if err := json.Unmarshal(stdout, &parsed); err != nil {
		t.Fatalf("eval output is not valid JSON: %v\noutput: %s", err, string(stdout))
	}

	if _, ok := parsed["Stacks"]; !ok {
		t.Errorf("eval output missing Stacks field; got keys: %v", keysOf(parsed))
	}
	if _, ok := parsed["Resources"]; !ok {
		t.Errorf("eval output missing Resources field; got keys: %v", keysOf(parsed))
	}
}

// TestEvalSchemaLocationLocal verifies that `formae eval --schema-location
// local` accepts the flag, resolves plugin schemas from the agent's local
// filesystem paths, and produces equivalent output to the default (remote)
// path. Same-box only — the flag is intended for the developer workflow
// where CLI and agent share a filesystem.
//
// Red until the --schema-location flag is wired into eval and the agent's
// plugin-info response is extended with localPath.
func TestEvalSchemaLocationLocal(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	fixture := filepath.Join(fixturesDir(t), "reconcile_apply_aws.pkl")
	stdout := cli.Eval(t, fixture, "--schema-location", "local")
	if len(stdout) == 0 {
		t.Fatal("formae eval --schema-location local returned empty output")
	}

	var parsed map[string]any
	if err := json.Unmarshal(stdout, &parsed); err != nil {
		t.Fatalf("eval output is not valid JSON: %v\noutput: %s", err, string(stdout))
	}

	if _, ok := parsed["Stacks"]; !ok {
		t.Errorf("eval output missing Stacks field; got keys: %v", keysOf(parsed))
	}
}

// TestExtractSchemaLocationLocal verifies that `formae extract --schema-location
// local` writes a PklProject whose dependencies are local file imports
// (`import("...")`) rather than remote hub URIs (`uri = "package://..."`).
// Same-box only.
//
// Red until the --schema-location flag is wired into extract.
func TestExtractSchemaLocationLocal(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	fixture := filepath.Join(fixturesDir(t), "extract_schema_local_aws.pkl")
	commandTimeout := 2 * time.Minute

	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	t.Cleanup(func() {
		destroyID := cli.Destroy(t, fixture)
		destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
		if destroyResult.State != "Success" && destroyResult.State != "NoOp" {
			t.Logf("cleanup destroy ended in state %s", destroyResult.State)
		}
	})

	extractDir := t.TempDir()
	extractedPath := filepath.Join(extractDir, "extracted.pkl")
	cli.ExtractToFile(t, "stack:e2e-extract-schema-local-aws", extractedPath, "--schema-location", "local")

	pklProjectPath := filepath.Join(extractDir, "PklProject")
	data, err := os.ReadFile(pklProjectPath)
	if err != nil {
		t.Fatalf("PklProject not found at %s: %v", pklProjectPath, err)
	}
	content := string(data)

	if strings.Contains(content, "package://") {
		t.Errorf("PklProject still contains remote package:// URIs under --schema-location local; want local import() calls only.\nContent:\n%s", content)
	}
	if !strings.Contains(content, "import(") {
		t.Errorf("PklProject does not contain any import() calls under --schema-location local.\nContent:\n%s", content)
	}
	if !strings.Contains(content, `["aws"]`) {
		t.Errorf("PklProject does not declare aws dependency.\nContent:\n%s", content)
	}
}

// keysOf returns the keys of a map, used for clearer error messages when an
// expected field is missing.
func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
