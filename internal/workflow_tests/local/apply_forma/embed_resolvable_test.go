// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

func TestMetastructure_RejectsOpaqueEmbed(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// No plugin overrides needed — the rejection happens before any plugin call.
		m, def, err := test_helpers.NewTestMetastructure(t, &plugin.ResourcePluginOverrides{})
		defer def()
		require.NoError(t, err)

		// Build a $res envelope with $visibility:"Opaque", then frame it in a
		// $template so it looks like an embedded opaque resolvable.
		opaqueEnvelope := `{"$res":true,"$label":"some-host","$type":"FakeAWS::S3::Host","$stack":"test-stack","$property":"SecretKey","$visibility":"Opaque"}`
		framedSpan := pkgmodel.FrameEnvelope(opaqueEnvelope)

		embedField := map[string]any{
			"$embed":     true,
			"$template":  "prefix-" + framedSpan + "-suffix",
		}
		embedFieldJSON, err := json.Marshal(embedField)
		require.NoError(t, err)

		propsJSON, err := json.Marshal(map[string]any{
			"Name":         "consumer-resource",
			"functionCode": json.RawMessage(embedFieldJSON),
		})
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{{
				Label:      "consumer",
				Type:       "FakeAWS::S3::Bucket",
				Stack:      "test-stack",
				Target:     "test-target",
				Properties: json.RawMessage(propsJSON),
			}},
			Targets: []pkgmodel.Target{{
				Label:     "test-target",
				Namespace: "test-namespace",
			}},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client-id")

		require.Error(t, err, "apply should be rejected when an opaque resolvable is embedded in a string field")
		assert.Contains(t, err.Error(), "opaque")
		assert.Contains(t, err.Error(), "embed")

		// No FormaCommand or resource row should have been persisted.
		fas, dsErr := m.Datastore.LoadFormaCommands()
		require.NoError(t, dsErr)
		assert.Empty(t, fas, "no FormaCommand should be persisted on opaque-embed rejection")

		resources, dsErr := m.Datastore.LoadAllResources()
		require.NoError(t, dsErr)
		assert.Empty(t, resources, "no resource row should be persisted on opaque-embed rejection")
	})
}
