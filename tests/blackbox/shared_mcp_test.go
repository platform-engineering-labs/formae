// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// This is a protocol driver, not a substitute MCP server or agent. Both
// processes execute their production routing and the CLI evaluates real Pkl.
type stdioMCP struct {
	t      *testing.T
	input  io.WriteCloser
	output *json.Decoder
	id     int
	close  func()
}

func startStdioMCP(t *testing.T, bin string, env []string) *stdioMCP {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = env
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	output, err := cmd.StdoutPipe()
	require.NoError(t, err)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	s := &stdioMCP{t: t, input: input, output: json.NewDecoder(output)}
	var once sync.Once
	s.close = func() {
		once.Do(func() {
			_ = input.Close()
			cancel()
			_ = cmd.Wait()
			if t.Failed() {
				t.Logf("MCP stderr: %s", stderr.String())
			}
		})
	}
	t.Cleanup(s.close)
	s.request("initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "real-agent-test", "version": "1"}})
	require.NoError(t, json.NewEncoder(input).Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}))
	return s
}

func (s *stdioMCP) request(method string, params any) json.RawMessage {
	s.t.Helper()
	s.id++
	require.NoError(s.t, json.NewEncoder(s.input).Encode(map[string]any{"jsonrpc": "2.0", "id": s.id, "method": method, "params": params}))
	for {
		var reply struct {
			ID     int
			Result json.RawMessage
			Error  json.RawMessage
		}
		require.NoError(s.t, s.output.Decode(&reply))
		if reply.ID == 0 {
			continue
		}
		require.Equal(s.t, s.id, reply.ID)
		require.Empty(s.t, reply.Error, string(reply.Error))
		return reply.Result
	}
}

func (s *stdioMCP) call(name string, args map[string]any, out any) string {
	s.t.Helper()
	args["profile"] = "isolated"
	raw := s.request("tools/call", map[string]any{"name": name, "arguments": args})
	var result struct {
		IsError bool
		Content []struct {
			Type string
			Text string
		}
	}
	require.NoError(s.t, json.Unmarshal(raw, &result))
	var texts []string
	for _, content := range result.Content {
		if content.Type == "text" {
			texts = append(texts, content.Text)
		}
	}
	text := strings.Join(texts, "\n")
	if result.IsError {
		return text
	}
	if out != nil {
		// Routing notices may be additional text blocks; the first is the
		// full JSON tool result, never its truncated human presentation.
		require.NotEmpty(s.t, texts)
		require.NoError(s.t, json.Unmarshal([]byte(texts[0]), out), text)
	}
	return ""
}

type preparedSource struct {
	FilePath       string         `json:"file_path"`
	ProjectPath    string         `json:"project_path"`
	Context        map[string]any `json:"context"`
	CompleteStacks []string       `json:"complete_stacks"`
}

func evalSharedPkl(t *testing.T, env []string, path string) *pkgmodel.Forma {
	t.Helper()
	cmd := exec.Command(formaeBinary, "eval", path, "--profile", "isolated", "--output-schema", "json", "--output-consumer", "machine")
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	require.NoError(t, err, stderr.String())
	var f pkgmodel.Forma
	require.NoError(t, json.Unmarshal(out, &f), string(out))
	return &f
}

// applyReviewedFixtureEdit is a test harness edit seam: it checks the exact
// source revision reviewed by the caller. No product AST/conflict resolver is
// implied. The edit itself is supplied explicitly by this deterministic test.
func applyReviewedFixtureEdit(path string, reviewed []byte, edited []byte) error {
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, reviewed) {
		return fmt.Errorf("source catch-up conflict: %s changed after review", path)
	}
	return os.WriteFile(path, edited, 0600)
}

func TestSharedResolutionMCPRealAgent(t *testing.T) {
	mcpBin := os.Getenv("FORMAE_INTEGRATION_MCP_BIN")
	cliBin := os.Getenv("FORMAE_INTEGRATION_BIN")
	if mcpBin == "" || cliBin == "" {
		t.Skip("set FORMAE_INTEGRATION_MCP_BIN and FORMAE_INTEGRATION_BIN to actual MCP and versioned CLI executables")
	}
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := NewTestHarness(t, 15*time.Second)
		defer h.Cleanup()
		requireSharedCapabilities(t, h)
		root, err := os.Getwd()
		require.NoError(t, err)
		coreProject := filepath.Join(root, "internal/schema/pkl/schema/PklProject")
		testProject := filepath.Join(root, "tests/blackbox/testdata/shared_schema/PklProject")
		// The test plugin and source/dev core schema are unpublished. Supply
		// explicit local dependencies before the real renderer resolves them.
		// This wrapper does not replace Stats, CLI evaluation, or agent calls.
		project := "amends \"pkl:Project\"\ndependencies { [\"formae\"] = import(" + strconv.Quote(coreProject) + "); [\"test\"] = import(" + strconv.Quote(testProject) + ") }\n"
		wrapper := filepath.Join(t.TempDir(), "formae")
		script := "#!/usr/bin/env python3\nimport sys,pathlib,os\nargs=sys.argv[1:]\nif args[0]=='extract':\n (pathlib.Path(args[-1]).parent/'PklProject').write_text(" + strconv.Quote(project) + ")\nos.execv(" + strconv.Quote(cliBin) + ",[" + strconv.Quote(cliBin) + "]+args)\n"
		require.NoError(t, os.WriteFile(wrapper, []byte(script), 0700))
		configDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(configDir, "profiles"), 0700))
		profile := fmt.Sprintf("amends \"formae:/Config.pkl\"\ncli { connection = new Classic { url = \"http://localhost\"; port = %d } }\n", h.port)
		require.NoError(t, os.WriteFile(filepath.Join(configDir, "profiles/isolated.pkl"), []byte(profile), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(configDir, "active"), []byte("isolated"), 0600))
		env := append(os.Environ(), "FORMAE_BIN="+wrapper, "FORMAE_CONFIG_DIR="+configDir, "XDG_CONFIG_HOME="+t.TempDir())
		for _, bin := range []string{formaeBinary, cliBin, mcpBin, filepath.Join(h.pluginsDir, "test-plugin/v0.0.1/test-plugin")} {
			data, e := os.ReadFile(bin)
			require.NoError(t, e)
			t.Logf("executable SHA256 %x %s", sha256.Sum256(data), bin)
		}
		f := SimpleForma(2)
		original := h.ApplyForma(f, pkgmodel.FormaApplyModeReconcile)
		require.Equal(t, "Success", h.WaitForCommandDone(original, 30*time.Second).State)
		first := startStdioMCP(t, mcpBin, env)
		var prepared preparedSource
		dir := t.TempDir()
		require.Empty(t, first.call("prepare_authoring", map[string]any{"temporary_directory": dir, "stacks": []string{"default"}}, &prepared))
		require.Equal(t, []string{"default"}, prepared.CompleteStacks)
		require.Equal(t, "none", prepared.Context["mode"])
		marker, err := os.ReadFile(filepath.Join(dir, ".formae-authoring.json"))
		require.NoError(t, err)
		var ready struct{ Version int }
		require.NoError(t, json.Unmarshal(marker, &ready))
		require.Equal(t, 1, ready.Version)
		resolved, resolveErr := exec.Command("pkl", "project", "resolve", dir).CombinedOutput()
		require.NoError(t, resolveErr, string(resolved))
		for _, path := range []string{prepared.FilePath, prepared.ProjectPath, filepath.Join(dir, "PklProject.deps.json")} {
			_, err := os.Stat(path)
			require.NoError(t, err)
		}
		source, err := os.ReadFile(prepared.FilePath)
		require.NoError(t, err)
		t.Logf("generated source: %s", source)
		require.Contains(t, string(source), "v1")
		require.NoError(t, os.WriteFile(prepared.FilePath, bytes.ReplaceAll(source, []byte(`"v1"`), []byte(`"authored"`)), 0600))
		evaluated := evalSharedPkl(t, env, prepared.FilePath)
		require.Len(t, evaluated.Stacks, 1)
		require.Len(t, evaluated.Targets, 1)
		require.Equal(t, "test-target", evaluated.Targets[0].Label)
		require.Equal(t, "Test", evaluated.Targets[0].Namespace)
		for _, resource := range evaluated.Resources {
			require.ElementsMatch(t, testResourceSchema.Fields, resource.Schema.Fields)
			require.Equal(t, testResourceSchema.Identifier, resource.Schema.Identifier)
		}
		requireResourceValues(t, evaluated.Resources, map[string]string{"res-a": "authored", "res-b": "authored"})
		args := map[string]any{"file_path": prepared.FilePath, "context": prepared.Context, "mode": "reconcile", "simulate": true}
		var preview, accepted apimodel.SubmitCommandResponse
		require.Empty(t, first.call("apply_forma", args, &preview))
		args["simulate"] = false
		require.Empty(t, first.call("apply_forma", args, &accepted))
		require.Equal(t, "Success", h.WaitForCommandDone(accepted.CommandID, 30*time.Second).State)
		// Caller two uses a new process and directory, after caller one's
		// disposable source has been removed following its terminal outcome.
		require.NoError(t, os.RemoveAll(dir))
		first.close()
		second := startStdioMCP(t, mcpBin, env)
		var central preparedSource
		require.Empty(t, second.call("prepare_authoring", map[string]any{"temporary_directory": t.TempDir(), "stacks": []string{"default"}}, &central))
		requireResourceValues(t, evalSharedPkl(t, env, central.FilePath).Resources, map[string]string{"res-a": "authored", "res-b": "authored"})
		t.Logf("independent MCP process recovered centrally authored intent after first source cleanup")
		// Select one of two maintained projects by the real registry, then
		// consume its real command delta through the explicit harness edit.
		projects := []string{t.TempDir(), t.TempDir()}
		var bindings []string
		for _, path := range projects {
			for _, name := range []string{"main.pkl", "PklProject", "PklProject.deps.json"} {
				raw, e := os.ReadFile(filepath.Join(filepath.Dir(central.FilePath), name))
				require.NoError(t, e)
				require.NoError(t, os.WriteFile(filepath.Join(path, name), raw, 0600))
			}
			var binding struct {
				ID string `json:"id"`
			}
			require.Empty(t, second.call("register_codebase", map[string]any{"path": path, "stacks": []string{"default"}}, &binding))
			require.NotEmpty(t, binding.ID)
			bindings = append(bindings, binding.ID)
		}
		var selected struct {
			Selection struct {
				Binding struct {
					ID string `json:"id"`
				} `json:"binding"`
			} `json:"selection"`
		}
		require.Empty(t, second.call("get_codebase_context", map[string]any{"mode": "codebase", "binding_id": bindings[0]}, &selected))
		require.Equal(t, bindings[0], selected.Selection.Binding.ID)
		maintained := filepath.Join(projects[0], "main.pkl")
		reviewed, err := os.ReadFile(maintained)
		require.NoError(t, err)
		untouched := map[string][]byte{}
		for _, name := range []string{"main.pkl", "PklProject", "PklProject.deps.json"} {
			untouched[name], err = os.ReadFile(filepath.Join(projects[1], name))
			require.NoError(t, err)
		}
		for id, entry := range h.GetCloudStateSnapshot(t) {
			var props map[string]any
			require.NoError(t, json.Unmarshal([]byte(entry.Properties), &props))
			if props["Name"] != "res-a" {
				continue
			}
			props["Value"] = "accepted-cloud"
			raw, e := json.Marshal(props)
			require.NoError(t, e)
			h.PutCloudState(t, id, entry.ResourceType, string(raw))
		}
		baseline := h.SyncCommandBaseline()
		require.NoError(t, h.client.ForceSync())
		_, ok := h.WaitForSyncCommandAfter(baseline, 10*time.Second, 30*time.Second)
		require.True(t, ok)
		args = map[string]any{"file_path": maintained, "context": map[string]any{"mode": "codebase", "binding_id": bindings[0]}, "mode": "reconcile", "simulate": true}
		rejection := second.call("apply_forma", args, nil)
		require.Contains(t, rejection, "ObservationID")
		start := strings.Index(rejection, "{")
		require.NotEqual(t, -1, start, rejection)
		var failure struct {
			Data apimodel.FormaReconcileRejectedError `json:"data"`
		}
		require.NoError(t, json.Unmarshal([]byte(rejection[start:]), &failure), rejection)
		mods := failure.Data.ModifiedStacks["default"].ModifiedResources
		require.Len(t, mods, 1)
		resolution := pkgmodel.DriftResolution{ObservationID: failure.Data.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: mods[0].ResourceID, Action: "absorb"}}}
		args["resolution"] = &resolution
		require.Empty(t, second.call("apply_forma", args, &preview))
		require.NotNil(t, preview.Review)
		resolution.ReviewID = preview.Review.ReviewID
		resolution.IdempotencyKey = "mcp-maintained-absorb"
		args["simulate"] = false
		require.Empty(t, second.call("apply_forma", args, &accepted))
		require.Equal(t, "Success", h.WaitForCommandDone(accepted.CommandID, 30*time.Second).State)
		writesBeforeRetry := providerWrites(h.GetOperationLog(t))
		second.close()
		second = startStdioMCP(t, mcpBin, env)
		var retry apimodel.SubmitCommandResponse
		require.Empty(t, second.call("apply_forma", args, &retry))
		require.Equal(t, accepted.CommandID, retry.CommandID)
		require.Equal(t, writesBeforeRetry, providerWrites(h.GetOperationLog(t)))
		var delta apimodel.CommandDesiredDelta
		require.Empty(t, second.call("get_command_desired_delta", map[string]any{"command_id": accepted.CommandID}, &delta))
		require.True(t, delta.Partial)
		require.Equal(t, accepted.CommandID, delta.CommandID)
		require.Equal(t, "Success", delta.State)
		requireResourceValues(t, delta.Forma.Resources, map[string]string{"res-a": "accepted-cloud"})
		still, err := os.ReadFile(maintained)
		require.NoError(t, err)
		require.Equal(t, reviewed, still, "MCP returns guidance; it does not edit source")
		// Anchor this explicit fixture edit to the returned resource label:
		// generated resource order is not guaranteed. No model editing is simulated.
		var props map[string]any
		require.NoError(t, json.Unmarshal(delta.Forma.Resources[0].Properties, &props))
		resourceAnchor := []byte("label = " + strconv.Quote(delta.Forma.Resources[0].Label))
		require.Equal(t, 1, bytes.Count(reviewed, resourceAnchor))
		resourceStart := bytes.Index(reviewed, resourceAnchor)
		valueOffset := bytes.Index(reviewed[resourceStart:], []byte(`"authored"`))
		require.NotEqual(t, -1, valueOffset)
		valueStart := resourceStart + valueOffset
		require.NotContains(t, string(reviewed[resourceStart+len(resourceAnchor):valueStart]), "label = ", "the value must belong to the selected fixture resource")
		edited := append([]byte{}, reviewed[:valueStart]...)
		edited = append(edited, bytes.Replace(reviewed[valueStart:], []byte(`"authored"`), []byte(strconv.Quote(props["Value"].(string))), 1)...)
		conflict := append(append([]byte{}, reviewed...), []byte("\n// concurrent local edit\n")...)
		require.NoError(t, os.WriteFile(maintained, conflict, 0600))
		err = applyReviewedFixtureEdit(maintained, reviewed, edited)
		require.ErrorContains(t, err, "source catch-up conflict")
		t.Logf("central command %s: Success; harness source catch-up: %v", accepted.CommandID, err)
		require.Equal(t, "Success", h.WaitForCommandDone(accepted.CommandID, 5*time.Second).State)
		still, err = os.ReadFile(maintained)
		require.NoError(t, err)
		require.Equal(t, conflict, still)
		require.NoError(t, os.WriteFile(maintained, reviewed, 0600))
		require.NoError(t, applyReviewedFixtureEdit(maintained, reviewed, edited))
		requireResourceValues(t, evalSharedPkl(t, env, maintained).Resources, map[string]string{"res-a": "accepted-cloud", "res-b": "authored"})
		delete(args, "resolution")
		args["simulate"] = true
		require.Empty(t, second.call("apply_forma", args, &preview))
		require.False(t, preview.Simulation.ChangesRequired)
		for name, original := range untouched {
			still, err = os.ReadFile(filepath.Join(projects[1], name))
			require.NoError(t, err)
			require.Equal(t, original, still, "unselected project file: %s", name)
		}
		entries, err := os.ReadDir(projects[1])
		require.NoError(t, err)
		require.Len(t, entries, len(untouched), "unselected project must gain no files")
	})
}
