//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

func TestResolutionRestartRetainsExactDrawSet(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		path := t.TempDir() + "/draw.db"
		cfg := &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: path}}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		wrapper := &uncertainScopedCommit{scopedReadBarrier: withScopedBarrier(ds, nil)}
		var mu sync.Mutex
		cloud := map[string]json.RawMessage{}
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(r *resource.CreateRequest) (*resource.CreateResult, error) {
				mu.Lock()
				defer mu.Unlock()
				cloud[r.Label] = append(json.RawMessage(nil), r.Properties...)
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: r.Label, ResourceProperties: r.Properties}}, nil
			},
			Update: func(r *resource.UpdateRequest) (*resource.UpdateResult, error) {
				mu.Lock()
				defer mu.Unlock()
				var state map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(cloud[r.NativeID], &state))
				var patch []struct {
					Op, Path string
					Value    json.RawMessage
				}
				require.NoError(t, json.Unmarshal([]byte(*r.PatchDocument), &patch))
				for _, op := range patch {
					key := strings.TrimPrefix(op.Path, "/")
					require.NotContains(t, key, "/")
					if op.Op == "remove" {
						delete(state, key)
					} else {
						state[key] = op.Value
					}
				}
				raw, e := json.Marshal(state)
				require.NoError(t, e)
				cloud[r.NativeID] = raw
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationUpdate, OperationStatus: resource.OperationStatusSuccess, NativeID: r.NativeID, ResourceProperties: raw}}, nil
			},
			Read: func(r *resource.ReadRequest) (*resource.ReadResult, error) {
				mu.Lock()
				defer mu.Unlock()
				return &resource.ReadResult{ResourceType: r.ResourceType, Properties: string(cloud[r.NativeID])}, nil
			},
		}
		m := startScopedActor(t, wrapper, path, overrides)
		g := func(label string) json.RawMessage {
			raw, e := json.Marshal(&pkgmodel.PasswordGenerator{Stack: "scope", Label: label, Length: 24, Lowercase: true})
			require.NoError(t, e)
			return raw
		}
		bound := func(label string, second bool) pkgmodel.Resource {
			props := map[string]any{"Name": label, "SecretString": map[string]any{"$gen": true, "$label": "g1", "$stack": "scope", "$output": "value", "$visibility": "Opaque"}}
			if second {
				props["Description"] = map[string]any{"$gen": true, "$label": "g2", "$stack": "scope", "$output": "value", "$visibility": "Opaque"}
			}
			raw, e := json.Marshal(props)
			require.NoError(t, e)
			return pkgmodel.Resource{Stack: "scope", Label: label, Target: "target", Type: "FakeAWS::SecretsManager::Secret", Properties: raw, Schema: pkgmodel.Schema{Fields: []string{"Name", "SecretString", "Description"}, Hints: map[string]pkgmodel.FieldHint{"SecretString": {Opaque: true}, "Description": {Opaque: true}}}}
		}
		f := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "scope"}}, Targets: []pkgmodel.Target{{Label: "target", Namespace: "FakeAWS", Config: []byte(`{}`)}}, Generators: []json.RawMessage{g("g1"), g("g2")}, Resources: []pkgmodel.Resource{bound("a", false), bound("b", true)}}
		first, e := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "")
		require.NoError(t, e)
		require.Eventually(t, func() bool {
			c, e := ds.GetFormaCommandByCommandID(first.CommandID)
			return e == nil && c.State == forma_command.CommandStateSuccess
		}, 5*time.Second, 10*time.Millisecond)
		g2, err := ds.GetGeneratorIdentity("g2", "scope")
		require.NoError(t, err)
		require.NotEmpty(t, g2.GenerationID)
		f.Resources = append(f.Resources, bound("new", false))
		wrapper.lose.Store(true)
		_, err = m.applyFormaWithKey(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "", "lost")
		require.ErrorContains(t, err, "lost commit response")
		commands, err := ds.LoadIncompleteFormaCommands()
		require.NoError(t, err)
		require.Len(t, commands, 1)
		pending := commands[0]
		require.True(t, pending.DrawIntentKnown)
		require.Len(t, pending.DrawGeneratorUpdates, 1)
		require.Equal(t, "g1", pending.DrawGeneratorUpdates[0].Generator.GetLabel())
		require.Len(t, pending.ResourceUpdates, 3, "both existing destinations must be co-planned")
		m.Stop(true)
		reopened, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "restart")
		require.NoError(t, err)
		restarted := startScopedActor(t, reopened, path, overrides)
		require.Eventually(t, func() bool {
			c, e := reopened.GetFormaCommandByCommandID(pending.ID)
			return e == nil && c.State == forma_command.CommandStateSuccess
		}, 5*time.Second, 10*time.Millisecond)
		after, err := reopened.GetGeneratorIdentity("g2", "scope")
		require.NoError(t, err)
		require.Equal(t, g2.GenerationID, after.GenerationID, "restart must not rotate a stable second generator from a co-planned resource")
		retry, err := restarted.applyFormaWithKey(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "client", "subject", "", "lost")
		require.NoError(t, err)
		require.Equal(t, pending.ID, retry.CommandID)
	})
}
