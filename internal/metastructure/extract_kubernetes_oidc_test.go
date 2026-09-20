// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/schema"
	"github.com/platform-engineering-labs/formae/internal/schema/pkl"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// Seeds the datastore with targets evaluated from the actual plugin examples,
// then runs real observed-state extraction, rendering and Pkl re-evaluation.
// No cloud exchange, token or client secret is needed for this public metadata.
func TestExtractKubernetesOIDCExamples(t *testing.T) {
	source := os.Getenv("FORMAE_PACKAGED_K8S_SOURCE")
	if source == "" {
		t.Skip("set FORMAE_PACKAGED_K8S_SOURCE to the Kubernetes checkout")
	}
	for _, mode := range []string{"eks", "aks", "gke", "direct"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(source, "examples", "oidc-"+mode)
			raw, e := exec.Command("pkl", "eval", "--project-dir", dir, "--format", "json", filepath.Join(dir, "main.pkl")).CombinedOutput()
			require.NoError(t, e, "%s", raw)
			var original pkgmodel.Forma
			require.NoError(t, json.Unmarshal(raw, &original))
			require.Len(t, original.Targets, 1)
			ds := newSQLiteTestDatastore(t)
			_, e = ds.CreateStack(&original.Stacks[0], "seed")
			require.NoError(t, e)
			_, e = ds.CreateTarget(&original.Targets[0])
			require.NoError(t, e)
			// The Namespace has no unresolved resource references and associates this
			// public target with the selected stack, as actual extraction requires.
			ns := original.Resources[0]
			ns.Ksuid = util.NewID()
			ns.NativeID = "formae-oidc-example"
			ns.Managed = true
			_, e = ds.StoreResource(&ns, "seed")
			require.NoError(t, e)
			m := &Metastructure{Datastore: ds, Cfg: &pkgmodel.Config{}}
			extracted, e := m.ExtractResources("stack:" + original.Stacks[0].Label)
			require.NoError(t, e)
			require.Len(t, extracted.Targets, 1)
			require.JSONEq(t, string(original.Targets[0].Config), string(extracted.Targets[0].Config))
			root, e := filepath.Abs("../..")
			require.NoError(t, e)
			dest := filepath.Join(t.TempDir(), "roundtrip.pkl")
			_, e = (pkl.PKL{}).GenerateSourceCode(extracted, dest, nil, &schema.SerializeOptions{Schema: "pkl", SchemaLocation: schema.SchemaLocationLocal, Dependencies: []string{"local:formae:" + filepath.Join(root, "internal/schema/pkl/schema/PklProject"), "local:k8s:" + filepath.Join(source, "schema/pkl/PklProject")}})
			require.NoError(t, e)
			round, e := (pkl.PKL{}).Evaluate(dest, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile, nil)
			require.NoError(t, e)
			require.Len(t, round.Targets, 1)
			require.JSONEq(t, string(original.Targets[0].Config), string(round.Targets[0].Config))
			// Observed extraction's generic target renderer may omit hints. Core's
			// default remains immutable: prove the effective replacement classification.
			var changed map[string]any
			require.NoError(t, json.Unmarshal(round.Targets[0].Config, &changed))
			changed["Auth"].(map[string]any)["Endpoint"] = "https://replacement.example.invalid"
			changedRaw, e := json.Marshal(changed)
			require.NoError(t, e)
			require.Equal(t, target_update.ConfigImmutableChange, target_update.ClassifyConfigChange(round.Targets[0].Config, changedRaw, round.Targets[0].ConfigSchema))
			for _, forbidden := range []string{"AccessToken", "RefreshToken", "ClientSecret", "BearerToken", "Assertion"} {
				require.NotContains(t, string(round.Targets[0].Config), forbidden)
			}
		})
	}
}
