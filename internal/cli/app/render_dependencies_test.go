//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package app

import (
	"encoding/json"
	"github.com/platform-engineering-labs/formae/internal/schema"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDependenciesIncludeTargetAndEmbeddedResourceReferences(t *testing.T) {
	reference := `{"$res":true,"$type":"FakeAWS::SecretsManager::Secret","$stack":"elsewhere","$label":"source","$property":"Name"}`
	embed, err := json.Marshal(map[string]any{"password": map[string]any{"$embed": true, "$template": pkgmodel.FrameEnvelope(reference)}})
	require.NoError(t, err)
	for _, config := range []json.RawMessage{json.RawMessage(`{"ref":` + reference + `}`), embed} {
		forma := &pkgmodel.Forma{Extraction: &pkgmodel.ExtractionContext{CompleteStacks: []pkgmodel.Stack{{Label: "empty"}}}, Targets: []pkgmodel.Target{{Config: config}}}
		_, err := BuildDependencyStrings(forma, nil, schema.SchemaLocationRemote)
		require.ErrorContains(t, err, "fakeaws")
		deps, err := BuildDependencyStrings(forma, map[string]PluginInfo{"fakeaws": {Version: "1.2.3"}}, schema.SchemaLocationRemote)
		require.NoError(t, err)
		require.Contains(t, deps, "fakeaws.fakeaws@1.2.3")
	}
}
