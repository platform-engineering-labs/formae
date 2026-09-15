// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"
)

func TestLegacyDesiredExtractionMCP(t *testing.T) {
	mcpBin, cliBin := os.Getenv("FORMAE_INTEGRATION_MCP_BIN"), os.Getenv("FORMAE_INTEGRATION_BIN")
	if mcpBin == "" || cliBin == "" {
		t.Skip("set local MCP and versioned CLI paths")
	}
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		h := newTestHarness(t, 15*time.Second, true)
		defer h.Cleanup()
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

		initialDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(initialDir, "PklProject"), []byte(project), 0600))
		initialPath := filepath.Join(initialDir, "main.pkl")
		require.NoError(t, os.WriteFile(initialPath, []byte(`extends "@formae/forma.pkl"
import "@formae/formae.pkl"
import "@test/test.pkl"
forma {
 local s = new formae.Stack { label = "default" }
 s
 local t = new formae.Target { label = "test-target"; namespace = "Test"; config = new test.Config {} }
 t
 new test.GenericResource { label = "res-a"; stack = s.res; target = t.res; Name = "res-a"; Value = "v1" }
}
`), 0600))
		initial := evalSharedPkl(t, env, initialPath)
		id := h.ApplyForma(initial, pkgmodel.FormaApplyModeReconcile)
		require.Equal(t, "Success", h.WaitForCommandDone(id, 30*time.Second).State)
		// Seed the legacy format, before command_stacks existed. Command start
		// precedes stack persistence, as with an apply that creates its own stack.
		ds, e := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: h.dbPath}}, "legacy-fixture")
		require.NoError(t, e)
		c, e := ds.GetFormaCommandByCommandID(id)
		require.NoError(t, e)
		old := *c
		old.ID = util.NewID()
		old.Stacks = nil
		old.State = forma_command.CommandStateFailed
		old.StartTs = time.Now().Add(-time.Hour)
		old.ModifiedTs = old.StartTs
		old.ResourceUpdates = append([]resource_update.ResourceUpdate(nil), c.ResourceUpdates...)
		old.ResourceUpdates[0].DesiredState.Ksuid = util.NewID()
		old.ResourceUpdates[0].DesiredState.Label = "res-a"
		old.ResourceUpdates[0].State = resource_update.ResourceUpdateStateFailed
		old.ResourceUpdates[0].Version = ""
		require.NoError(t, ds.StoreFormaCommand(&old, old.ID))
		old.ID = util.NewID()
		old.StartTs = old.StartTs.Add(-time.Hour)
		old.ModifiedTs = old.StartTs
		old.ResourceUpdates[0].DesiredState.Ksuid = util.NewID()
		require.NoError(t, ds.StoreFormaCommand(&old, old.ID))
		ds.Close()
		changeResolutionCloud(t, h, "outside")
		baseline := h.SyncCommandBaseline()
		_, ok := h.WaitForSyncCommandAfter(baseline, 10*time.Second, 30*time.Second)
		require.True(t, ok)
		mcp := startStdioMCP(t, mcpBin, env)
		var prepared preparedSource
		require.Empty(t, mcp.call("prepare_authoring", map[string]any{"temporary_directory": t.TempDir(), "stacks": []string{"default"}}, &prepared))
		desired := evalSharedPkl(t, env, prepared.FilePath)
		require.Len(t, desired.Resources, 1, "successful retry supersedes a legacy failed create of the same logical resource")
		for _, r := range desired.Resources {
			require.NotContains(t, string(r.Properties), "outside")
		}
		source, e := os.ReadFile(prepared.FilePath)
		require.NoError(t, e)
		// SetTags is independent of the out-of-band Value edit, analogous to two
		// distinct label keys. The successful retry is the single desired declaration.
		require.NoError(t, os.WriteFile(prepared.FilePath, regexp.MustCompile(`SetTags = new Listing\s*\{\s*\}`).ReplaceAll(source, []byte(`SetTags = new Listing { "app=demo" }`)), 0600))
		edited := evalSharedPkl(t, env, prepared.FilePath)
		require.NotEqual(t, string(desired.Resources[0].Properties), string(edited.Resources[0].Properties))
		args := map[string]any{"file_path": prepared.FilePath, "context": prepared.Context, "mode": "reconcile", "simulate": true}
		failure := mcp.call("apply_forma", args, nil)
		require.Contains(t, failure, "ReconcileRejected")
		require.NotContains(t, failure, "HTTP 500")
		var rejection *apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]
		_, e = h.client.ApplyForma(edited, pkgmodel.FormaApplyModeReconcile, true, clientID, false)
		require.ErrorAs(t, e, &rejection)
		writesBefore := providerWrites(h.GetOperationLog(t))
		for _, action := range []string{"absorb", "revert"} {
			var decisions []pkgmodel.DriftDecision
			for _, stack := range rejection.Data.ModifiedStacks {
				for _, r := range stack.ModifiedResources {
					decisions = append(decisions, pkgmodel.DriftDecision{ResourceID: r.ResourceID, Action: action})
				}
			}
			args["resolution"] = pkgmodel.DriftResolution{ObservationID: rejection.Data.ObservationID, Decisions: decisions}
			var preview apimodel.SubmitCommandResponse
			require.Empty(t, mcp.call("apply_forma", args, &preview))
			require.NotEmpty(t, preview.Review.ReviewID)
			found := false
			for _, update := range preview.Simulation.Command.ResourceUpdates {
				if update.ResourceLabel != "res-a" {
					continue
				}
				found = true
				var props map[string]any
				require.NoError(t, json.Unmarshal(update.Properties, &props))
				require.Contains(t, props["SetTags"], "app=demo")
				expected := "v1"
				if action == "absorb" {
					expected = "outside"
				}
				require.Equal(t, expected, props["Value"])
			}
			require.True(t, found, "preview must include the requested change")

		}
		require.Equal(t, writesBefore, providerWrites(h.GetOperationLog(t)), "previews perform no provider writes")
		t.Log("real MCP + CLI/Pkl + agent: legacy failed intent extracted, external state excluded, ordinary edit rejected for drift, keep/revert previews passed")
	})
}
