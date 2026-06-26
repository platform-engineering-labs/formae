// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package pkl

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/schema"
	"github.com/platform-engineering-labs/formae/pkg/model"
)

// --- preprocessFormaEmbeds tests ---

// TestPreprocessFormaEmbeds_SingleSpan verifies that a resource property
// containing a $embed field whose $template has one framed span is split into
// the correct $templateParts: [literal-before, $res-map, literal-after].
func TestPreprocessFormaEmbeds_SingleSpan(t *testing.T) {
	resEnv := `{"$res":true,"$label":"kvs","$type":"AWS::CloudFront::KeyValueStore","$stack":"default","$property":"id"}`
	framed := model.FrameEnvelope(resEnv)
	template := "cf.kvs('" + framed + "')"

	embedObj := map[string]any{
		"$embed":     true,
		"$template":  template,
	}
	propsBytes, err := json.Marshal(map[string]any{"functionCode": embedObj})
	require.NoError(t, err)

	forma := &model.Forma{
		Resources: []model.Resource{
			{
				Label:      "my-fn",
				Type:       "AWS::Lambda::Function",
				Stack:      "default",
				Properties: propsBytes,
			},
		},
	}

	result, err := preprocessFormaEmbeds(forma)
	require.NoError(t, err)

	// Unmarshal the processed properties
	var props map[string]any
	require.NoError(t, json.Unmarshal(result.Resources[0].Properties, &props))

	fc, ok := props["functionCode"].(map[string]any)
	require.True(t, ok, "functionCode must be a map")

	assert.Equal(t, true, fc["$embed"], "$embed must remain true")
	assert.Nil(t, fc["$template"], "$template must be replaced by $templateParts")

	parts, ok := fc["$templateParts"].([]any)
	require.True(t, ok, "$templateParts must be a list")
	require.Len(t, parts, 3, "expected [literal, $res-map, literal]")

	// Part 0: literal before the span
	assert.Equal(t, "cf.kvs('", parts[0], "first part must be the literal prefix")

	// Part 1: the $res envelope map
	resMap, ok := parts[1].(map[string]any)
	require.True(t, ok, "second part must be a map (the $res envelope)")
	assert.Equal(t, true, resMap["$res"], "$res must be true")
	assert.Equal(t, "kvs", resMap["$label"])
	assert.Equal(t, "default", resMap["$stack"])
	assert.Equal(t, "id", resMap["$property"])

	// Part 2: literal after the span
	assert.Equal(t, "')", parts[2], "third part must be the literal suffix")
}

// TestPreprocessFormaEmbeds_NilFormaIsNoop verifies nil input is handled safely.
func TestPreprocessFormaEmbeds_NilFormaIsNoop(t *testing.T) {
	result, err := preprocessFormaEmbeds(nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}

// TestPreprocessFormaEmbeds_NoEmbedFieldsUnchanged verifies that resources
// without $embed fields are passed through without modification.
func TestPreprocessFormaEmbeds_NoEmbedFieldsUnchanged(t *testing.T) {
	propsBytes := json.RawMessage(`{"bucketName":"my-bucket"}`)
	forma := &model.Forma{
		Resources: []model.Resource{
			{Label: "b", Type: "AWS::S3::Bucket", Stack: "default", Properties: propsBytes},
		},
	}
	result, err := preprocessFormaEmbeds(forma)
	require.NoError(t, err)
	assert.JSONEq(t, `{"bucketName":"my-bucket"}`, string(result.Resources[0].Properties))
}

// TestSplitEmbedTemplate_TwoSpans verifies that two spans produce five parts:
// [literal, $res, literal, $res, literal].
func TestSplitEmbedTemplate_TwoSpans(t *testing.T) {
	a := model.FrameEnvelope(`{"$res":true,"$label":"a","$stack":"s","$property":"p"}`)
	b := model.FrameEnvelope(`{"$res":true,"$label":"b","$stack":"s","$property":"q"}`)
	tmpl := "prefix" + a + "middle" + b + "suffix"

	parts, err := splitEmbedTemplate(tmpl)
	require.NoError(t, err)
	require.Len(t, parts, 5)

	assert.Equal(t, "prefix", parts[0])
	mapA, ok := parts[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "a", mapA["$label"])

	assert.Equal(t, "middle", parts[2])
	mapB, ok := parts[3].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "b", mapB["$label"])

	assert.Equal(t, "suffix", parts[4])
}

func TestResolveIncludes_PreResolvedDepsTakePrecedence(t *testing.T) {
	forma := &model.Forma{Resources: []model.Resource{{Type: "AWS::S3::Bucket"}}}
	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: "/this/path/should/not/be/touched",
		Dependencies: []string{
			"pkl.formae@0.85.0",
			"local:aws:/some/path/PklProject",
		},
	}

	got := resolveIncludes(forma, options)

	assert.ElementsMatch(t, []string{
		"pkl.formae@0.85.0",
		"local:aws:/some/path/PklProject",
	}, got)
}

func TestResolveIncludes_RemoteOnlyWhenNoDirAndNoDeps(t *testing.T) {
	forma := &model.Forma{Resources: []model.Resource{{Type: "AWS::S3::Bucket"}}}
	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationRemote,
	}

	got := resolveIncludes(forma, options)

	// formae version is the binary's compile-time version (test build = "0.0.0"),
	// so it's filtered out of the includes by the resolver. We expect only the
	// remote aws entry — and since aws version is "" (no installed version), it's
	// added as a plain namespace.
	assert.Contains(t, got, "aws.aws@")
}

func TestResolveSchemaVersions_NilForma(t *testing.T) {
	got, err := resolveSchemaVersions(nil, nil)
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = resolveSchemaVersions(nil, &schema.SerializeOptions{})
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestResolveSchemaVersions_TargetStampWinsOverFilesystemDefault(t *testing.T) {
	tmpDir := installVersionedPlugin(t, "K8S", "k8s",
		[]string{"v1.21", "v1.30", "v1.34"})
	forma := &model.Forma{
		Targets:   []model.Target{{Namespace: "K8S", Config: json.RawMessage(`{"ApiVersion":"v1.27"}`)}},
		Resources: []model.Resource{{Type: "K8S::Core::Pod"}},
	}
	got, err := resolveSchemaVersions(forma, &schema.SerializeOptions{
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: tmpDir,
	})
	require.NoError(t, err)
	assert.Equal(t, "v1.27", got["k8s"],
		"per-target stamp pins the version; filesystem default is the fallback")
}

func TestResolveSchemaVersions_FilesystemDefaultUsedWhenNoStamp(t *testing.T) {
	tmpDir := installVersionedPlugin(t, "K8S", "k8s",
		[]string{"v1.21", "v1.30", "v1.34"})
	forma := &model.Forma{
		Resources: []model.Resource{{Type: "K8S::Core::Pod"}},
	}
	got, err := resolveSchemaVersions(forma, &schema.SerializeOptions{
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: tmpDir,
	})
	require.NoError(t, err)
	assert.Equal(t, "v1.34", got["k8s"],
		"no target stamp → highest v*/ subdir wins (semver-aware)")
}

func TestResolveSchemaVersions_NamespaceWithNoSourceOmitted(t *testing.T) {
	forma := &model.Forma{
		Resources: []model.Resource{{Type: "AWS::S3::Bucket"}},
	}
	got, err := resolveSchemaVersions(forma, &schema.SerializeOptions{
		SchemaLocation: schema.SchemaLocationLocal,
	})
	require.NoError(t, err)
	assert.Nil(t, got, "no target stamp, no installed plugin → nil so ImportsGenerator falls back to unrestricted glob")
}

func TestResolveSchemaVersions_TargetStampOnlyForMatchingNamespace(t *testing.T) {
	// Stamp lives on the K8S target; AWS resources in the same Forma
	// must not pick it up.
	tmpDir := installVersionedPlugin(t, "K8S", "k8s",
		[]string{"v1.21", "v1.30", "v1.34"})
	forma := &model.Forma{
		Targets: []model.Target{{Namespace: "K8S", Config: json.RawMessage(`{"ApiVersion":"v1.27"}`)}},
		Resources: []model.Resource{
			{Type: "K8S::Core::Pod"},
			{Type: "AWS::S3::Bucket"},
		},
	}
	got, err := resolveSchemaVersions(forma, &schema.SerializeOptions{
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: tmpDir,
	})
	require.NoError(t, err)
	assert.Equal(t, "v1.27", got["k8s"])
	_, awsHas := got["aws"]
	assert.False(t, awsHas, "no AWS plugin install + no AWS target stamp → no entry")
}

// Two K8S targets at different ApiVersions cannot both be honored by a
// single ImportsGenerator pass (one PklProject resolves a package one way).
// Reject up front so users get an actionable error instead of a silent
// first-target-wins selection that misrenders resources bound to the other
// target. Per-target dispatch is a future redesign; this gate buys time.
func TestResolveSchemaVersions_RejectsConflictingApiVersionsWithinNamespace(t *testing.T) {
	forma := &model.Forma{
		Targets: []model.Target{
			{Namespace: "K8S", Label: "prod", Config: json.RawMessage(`{"ApiVersion":"v1.30"}`)},
			{Namespace: "K8S", Label: "dr", Config: json.RawMessage(`{"ApiVersion":"v1.27"}`)},
		},
		Resources: []model.Resource{{Type: "K8S::Core::Pod"}},
	}
	_, err := resolveSchemaVersions(forma, &schema.SerializeOptions{
		SchemaLocation: schema.SchemaLocationLocal,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "K8S")
	assert.Contains(t, err.Error(), "v1.30")
	assert.Contains(t, err.Error(), "v1.27")
	assert.Contains(t, err.Error(), "prod")
	assert.Contains(t, err.Error(), "dr")
}

// Two targets in the same namespace with the same ApiVersion are fine —
// they collapse to one entry and resolve the same way.
func TestResolveSchemaVersions_AllowsSameApiVersionAcrossTargets(t *testing.T) {
	forma := &model.Forma{
		Targets: []model.Target{
			{Namespace: "K8S", Label: "prod", Config: json.RawMessage(`{"ApiVersion":"v1.30"}`)},
			{Namespace: "K8S", Label: "dr", Config: json.RawMessage(`{"ApiVersion":"v1.30"}`)},
		},
	}
	got, err := resolveSchemaVersions(forma, &schema.SerializeOptions{SchemaLocation: schema.SchemaLocationLocal})
	require.NoError(t, err)
	assert.Equal(t, "v1.30", got["k8s"])
}

// When the user explicitly requests --schema-location remote, versioned
// dispatch must be a no-op even if targets carry ApiVersion stamps or a
// local plugin tree is reachable. Otherwise an explicit remote extract gets
// silently flipped to local, and output starts depending on the CLI host's
// plugin install instead of the agent-reported remote packages.
func TestResolveSchemaVersions_NoDispatchInRemoteMode(t *testing.T) {
	tmpDir := installVersionedPlugin(t, "K8S", "k8s",
		[]string{"v1.21", "v1.30", "v1.34"})
	forma := &model.Forma{
		Targets:   []model.Target{{Namespace: "K8S", Config: json.RawMessage(`{"ApiVersion":"v1.27"}`)}},
		Resources: []model.Resource{{Type: "K8S::Core::Pod"}},
	}
	got, err := resolveSchemaVersions(forma, &schema.SerializeOptions{
		SchemaLocation: schema.SchemaLocationRemote,
		LocalPluginDir: tmpDir,
	})
	require.NoError(t, err)
	assert.Nil(t, got,
		"explicit remote mode opts out of versioned dispatch — even when a local plugin tree is reachable and a target stamps ApiVersion")
}

func TestFormatVersionsForProperty_Empty(t *testing.T) {
	assert.Equal(t, "", formatVersionsForProperty(nil))
	assert.Equal(t, "", formatVersionsForProperty(map[string]string{}))
}

func TestFormatVersionsForProperty_SingleEntry(t *testing.T) {
	assert.Equal(t, "k8s=v1.30", formatVersionsForProperty(map[string]string{"k8s": "v1.30"}))
}

func TestFormatVersionsForProperty_StableOrderAcrossKeys(t *testing.T) {
	// Sorted ascending so the property string is deterministic for caching
	// and reproducible test output.
	assert.Equal(t, "aws=v2024-01-01,k8s=v1.30", formatVersionsForProperty(map[string]string{
		"k8s": "v1.30",
		"aws": "v2024-01-01",
	}))
}

func TestFormatVersionsForProperty_LowercasesNamespace(t *testing.T) {
	assert.Equal(t, "k8s=v1.30", formatVersionsForProperty(map[string]string{"K8S": "v1.30"}),
		"namespace is lowercased so ImportsGenerator's pkg-name comparison hits regardless of casing in the source map")
}

func TestFormatVersionsForProperty_DropsBlankEntries(t *testing.T) {
	assert.Equal(t, "k8s=v1.30", formatVersionsForProperty(map[string]string{
		"k8s": "v1.30",
		"":    "v9",
		"aws": "",
	}))
}

// Regression: when caller pre-resolves an include as `local:k8s:<path>` (e.g.
// resolveIncludes via resolver.WithLocalSchemas), the previous swap pass
// failed to recognize it and appended a duplicate `local:k8s:<path>` entry,
// producing `Duplicate definition of member "k8s"` from `pkl project resolve`.
func TestSwapVersionedDepsToLocal_DoesNotDuplicateExistingLocalEntry(t *testing.T) {
	pluginDir := installVersionedPlugin(t, "K8S", "k8s", []string{"v1.34"})
	localPath := filepath.Join(pluginDir, "k8s", "v0.1.1", "schema", "pkl", "PklProject")

	includes := []string{
		"pkl.formae@0.85.0",
		"local:k8s:" + localPath,
	}
	versions := map[string]string{"k8s": "v1.34"}
	options := &schema.SerializeOptions{LocalPluginDir: pluginDir}

	got := swapVersionedDepsToLocal(includes, versions, options)

	assert.ElementsMatch(t, []string{
		"pkl.formae@0.85.0",
		"local:k8s:" + localPath,
	}, got, "existing local: entry must pass through without a duplicate appended")
}

// When the include list has a remote `<ns>.<name>@<ver>` entry, the swap pass
// should rewrite it in place to `local:<name>:<path>` (not append).
func TestSwapVersionedDepsToLocal_RewritesRemoteToLocal(t *testing.T) {
	pluginDir := installVersionedPlugin(t, "K8S", "k8s", []string{"v1.34"})
	expectedPath := filepath.Join(pluginDir, "k8s", "v0.1.1", "schema", "pkl", "PklProject")

	includes := []string{
		"pkl.formae@0.85.0",
		"k8s.k8s@0.1.1",
	}
	versions := map[string]string{"k8s": "v1.34"}
	options := &schema.SerializeOptions{LocalPluginDir: pluginDir}

	got := swapVersionedDepsToLocal(includes, versions, options)

	assert.ElementsMatch(t, []string{
		"pkl.formae@0.85.0",
		"local:k8s:" + expectedPath,
	}, got)
}

// When the namespace is missing from includes entirely, the swap pass must
// append a fresh `local:<name>:<path>` so the temp PklProject can resolve it.
func TestSwapVersionedDepsToLocal_AppendsWhenNamespaceMissing(t *testing.T) {
	pluginDir := installVersionedPlugin(t, "K8S", "k8s", []string{"v1.34"})
	expectedPath := filepath.Join(pluginDir, "k8s", "v0.1.1", "schema", "pkl", "PklProject")

	includes := []string{"pkl.formae@0.85.0"}
	versions := map[string]string{"k8s": "v1.34"}
	options := &schema.SerializeOptions{LocalPluginDir: pluginDir}

	got := swapVersionedDepsToLocal(includes, versions, options)

	assert.ElementsMatch(t, []string{
		"pkl.formae@0.85.0",
		"local:k8s:" + expectedPath,
	}, got)
}
