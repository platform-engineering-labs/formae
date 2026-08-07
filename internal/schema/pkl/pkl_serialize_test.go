// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package pkl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// fakeawsDeps builds the local dependency list for SerializeForma tests that
// use the bundled fakeaws schema. Both deps are local so no pkl project resolve
// is required and the tests run without any installed plugin.
func fakeawsDeps(t *testing.T) ([]string, string) {
	t.Helper()
	// Go tests run with cwd = the package directory (internal/schema/pkl).
	formaePklProject, err := filepath.Abs("schema/PklProject")
	require.NoError(t, err)
	fakeawsPklProject, err := filepath.Abs("../../testplugin/fakeaws/schema/pkl/PklProject")
	require.NoError(t, err)
	deps := []string{
		"local:formae:" + formaePklProject,
		"local:fakeaws:" + fakeawsPklProject,
	}
	pluginDir, err := filepath.Abs("../../testplugin/fakeaws/schema/pkl")
	require.NoError(t, err)
	return deps, pluginDir
}

// hashedOpaqueProps builds a JSON properties blob carrying a $hashed:true opaque value.
func hashedOpaqueProps(t *testing.T, hex string) json.RawMessage {
	t.Helper()
	props := map[string]any{
		"SecretString": map[string]any{
			"$value":      hex,
			"$visibility": "Opaque",
			"$strategy":   "Update",
			"$hashed":     true,
		},
	}
	b, err := json.Marshal(props)
	require.NoError(t, err)
	return b
}

// fakeawsTarget returns a FakeAWS target with the minimal Config required.
func fakeawsTarget() model.Target {
	return model.Target{
		Label:     "aws",
		Namespace: "FakeAWS",
		Config:    json.RawMessage(`{"Type":"FakeAWS","Region":"us-east-1"}`),
	}
}

// TestSerializeForma_HashedOpaque_EmitsHashedMarker verifies that a resource
// whose property carries $hashed:true in its stored value is rendered with
// the .hashed fluent accessor in the serialized PKL output.
func TestSerializeForma_HashedOpaque_EmitsHashedMarker(t *testing.T) {
	deps, pluginDir := fakeawsDeps(t)
	hex := "a3f5b2c8d9e1f04712345678abcdef0123456789abcdef0123456789abcdef01"

	forma := &model.Forma{
		Stacks:  []model.Stack{{Label: "default"}},
		Targets: []model.Target{fakeawsTarget()},
		Resources: []model.Resource{{
			Label:      "my-secret",
			Type:       "FakeAWS::SecretsManager::Secret",
			Stack:      "default",
			Target:     "aws",
			Properties: hashedOpaqueProps(t, hex),
		}},
	}
	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginDir,
		Dependencies:   deps,
	}

	out, err := PKL{}.SerializeForma(forma, options)
	require.NoError(t, err)
	assert.NotContains(t, out, "message = \"applyResource",
		"a secret resource must render its own fields, not a leaked classification failure")
	assert.Contains(t, out, ".hashed", "hashed opaque field must emit the .hashed fluent accessor")
}

// TestSerializeForma_HashedOpaque_EmitsSentinelComment verifies that the
// exact sentinel inline comment is appended for a hashed opaque field.
func TestSerializeForma_HashedOpaque_EmitsSentinelComment(t *testing.T) {
	deps, pluginDir := fakeawsDeps(t)
	hex := "a3f5b2c8d9e1f04712345678abcdef0123456789abcdef0123456789abcdef01"

	forma := &model.Forma{
		Stacks:  []model.Stack{{Label: "default"}},
		Targets: []model.Target{fakeawsTarget()},
		Resources: []model.Resource{{
			Label:      "my-secret",
			Type:       "FakeAWS::SecretsManager::Secret",
			Stack:      "default",
			Target:     "aws",
			Properties: hashedOpaqueProps(t, hex),
		}},
	}
	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginDir,
		Dependencies:   deps,
	}

	out, err := PKL{}.SerializeForma(forma, options)
	require.NoError(t, err)
	const sentinel = "// hashed secret value — cannot be applied as-is; re-supply the plaintext to set it"
	assert.Contains(t, out, sentinel, "hashed opaque field must carry the sentinel inline comment")
}

// TestSerializeForma_NonHashedOpaque_NoHashedMarker verifies that a non-hashed
// opaque value (absent $hashed key) does not emit .hashed or the sentinel comment.
func TestSerializeForma_NonHashedOpaque_NoHashedMarker(t *testing.T) {
	deps, pluginDir := fakeawsDeps(t)
	props := json.RawMessage(`{"SecretString":{"$value":"b4e6c3d7","$visibility":"Opaque","$strategy":"Update"}}`)

	forma := &model.Forma{
		Stacks:  []model.Stack{{Label: "default"}},
		Targets: []model.Target{fakeawsTarget()},
		Resources: []model.Resource{{
			Label:      "my-secret",
			Type:       "FakeAWS::SecretsManager::Secret",
			Stack:      "default",
			Target:     "aws",
			Properties: props,
		}},
	}
	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginDir,
		Dependencies:   deps,
	}

	out, err := PKL{}.SerializeForma(forma, options)
	require.NoError(t, err)
	assert.NotContains(t, out, ".hashed", "non-hashed opaque field must not emit .hashed")
	assert.NotContains(t, out, "// hashed secret value", "non-hashed opaque must not carry the sentinel comment")
}

// TestSerializeForma_HashedOpaque_StrictBoolean verifies that $hashed:false
// and absent $hashed are both treated as non-hashed (strict boolean check).
func TestSerializeForma_HashedOpaque_StrictBoolean(t *testing.T) {
	deps, pluginDir := fakeawsDeps(t)

	cases := []struct {
		name  string
		props json.RawMessage
	}{
		{
			name:  "$hashed false",
			props: json.RawMessage(`{"SecretString":{"$value":"c5f7d4e8","$visibility":"Opaque","$strategy":"Update","$hashed":false}}`),
		},
		{
			name:  "$hashed absent",
			props: json.RawMessage(`{"SecretString":{"$value":"d6e8f5a9","$visibility":"Opaque","$strategy":"Update"}}`),
		},
		{
			name:  "$hashed non-boolean string",
			props: json.RawMessage(`{"SecretString":{"$value":"e7f9a6ba","$visibility":"Opaque","$strategy":"Update","$hashed":"true"}}`),
		},
		{
			name:  "$hashed non-boolean number",
			props: json.RawMessage(`{"SecretString":{"$value":"f8abb7cb","$visibility":"Opaque","$strategy":"Update","$hashed":1}}`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			forma := &model.Forma{
				Stacks:  []model.Stack{{Label: "default"}},
				Targets: []model.Target{fakeawsTarget()},
				Resources: []model.Resource{{
					Label:      "my-secret",
					Type:       "FakeAWS::SecretsManager::Secret",
					Stack:      "default",
					Target:     "aws",
					Properties: tc.props,
				}},
			}
			options := &schema.SerializeOptions{
				Schema:         "pkl",
				SchemaLocation: schema.SchemaLocationLocal,
				LocalPluginDir: pluginDir,
				Dependencies:   deps,
			}
			out, err := PKL{}.SerializeForma(forma, options)
			require.NoError(t, err)
			assert.NotContains(t, out, ".hashed", "strict boolean: %s must not emit .hashed", tc.name)
			assert.NotContains(t, out, "// hashed secret value", "strict boolean: %s must not emit sentinel comment", tc.name)
		})
	}
}

// TestSerializeForma_HashedOpaque_CommentIsTrailingLineComment verifies the
// placement-safety invariant: the sentinel is emitted only as a trailing line
// comment (nothing follows it on its line), so it can never comment out a
// sibling in a single-line construct such as `new Listing { … }`.
func TestSerializeForma_HashedOpaque_CommentIsTrailingLineComment(t *testing.T) {
	deps, pluginDir := fakeawsDeps(t)
	hex := "a3f5b2c8d9e1f04712345678abcdef0123456789abcdef0123456789abcdef01"

	forma := &model.Forma{
		Stacks:  []model.Stack{{Label: "default"}},
		Targets: []model.Target{fakeawsTarget()},
		Resources: []model.Resource{{
			Label:      "my-secret",
			Type:       "FakeAWS::SecretsManager::Secret",
			Stack:      "default",
			Target:     "aws",
			Properties: hashedOpaqueProps(t, hex),
		}},
	}
	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginDir,
		Dependencies:   deps,
	}

	out, err := PKL{}.SerializeForma(forma, options)
	require.NoError(t, err)

	const sentinel = "// hashed secret value — cannot be applied as-is; re-supply the plaintext to set it"
	found := false
	for _, line := range strings.Split(out, "\n") {
		idx := strings.Index(line, sentinel)
		if idx < 0 {
			continue
		}
		found = true
		// Everything after the sentinel on this line must be blank — a trailing
		// line comment, never mid-expression where it could swallow siblings.
		assert.Empty(t, strings.TrimSpace(line[idx+len(sentinel):]),
			"sentinel must be a trailing line comment; got trailing content on line: %q", line)
	}
	assert.True(t, found, "expected the sentinel comment somewhere in the output")
}

// TestSerializeForma_ResolvableNested_NoHashedEmitted verifies that a $res
// resolvable renders as a live Resolvable reference and does not emit the
// .hashed marker or the sentinel comment (resolvables are never hashed at rest).
func TestSerializeForma_ResolvableNested_NoHashedEmitted(t *testing.T) {
	deps, pluginDir := fakeawsDeps(t)

	// A $res envelope referencing another resource's property.
	props := json.RawMessage(`{"SecretString":{"$res":true,"$label":"other-secret","$type":"FakeAWS::SecretsManager::Secret","$stack":"default","$property":"SecretString","$value":"sha256hexdigest"}}`)

	forma := &model.Forma{
		Stacks:  []model.Stack{{Label: "default"}},
		Targets: []model.Target{fakeawsTarget()},
		Resources: []model.Resource{{
			Label:      "my-secret",
			Type:       "FakeAWS::SecretsManager::Secret",
			Stack:      "default",
			Target:     "aws",
			Properties: props,
		}},
	}
	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginDir,
		Dependencies:   deps,
	}

	out, err := PKL{}.SerializeForma(forma, options)
	require.NoError(t, err)
	// A resolvable renders as a live Resolvable type declaration, not as a formae.value().
	assert.Contains(t, out, "SecretResolvable", "resolvable field must render as a Resolvable type, not a value")
	// The .hashed marker must not appear for a resolvable-backed field.
	assert.NotContains(t, out, ".hashed", "resolvable field must not emit the .hashed marker")
	// The sentinel comment must not appear for a resolvable-backed field.
	assert.NotContains(t, out, "// hashed secret value", "resolvable field must not emit the sentinel comment")
}

// TestSerializeForma_ScalarSecretResolvableRef_ResolvesImportAndEvaluates covers
// a resolvable whose class extends one of the specialised bases rather than
// formae.Resolvable itself. Such a class only reaches the resource-type-URI map
// when the generator collects transitive subclasses, so without that the
// reference below has no import to resolve and serialization fails outright.
func TestSerializeForma_ScalarSecretResolvableRef_ResolvesImportAndEvaluates(t *testing.T) {
	deps, pluginDir := fakeawsDeps(t)

	// A DBInstance whose master password is a $res reference to a Secret — the
	// secret's resolvable extends formae.ScalarSecretResolvable.
	dbProps := json.RawMessage(`{"DbInstanceClass":"db.t3.micro","Engine":"postgres","MasterUserPassword":{"$res":true,"$label":"db-secret","$type":"FakeAWS::SecretsManager::Secret","$stack":"default","$property":"SecretString","$visibility":"Clear","$value":"hunter2"}}`)

	forma := &model.Forma{
		Stacks:  []model.Stack{{Label: "default"}},
		Targets: []model.Target{fakeawsTarget()},
		Resources: []model.Resource{
			{
				Label:      "db-secret",
				Type:       "FakeAWS::SecretsManager::Secret",
				Stack:      "default",
				Target:     "aws",
				Properties: json.RawMessage(`{"Name":"db-secret"}`),
			},
			{
				Label:      "app-db",
				Type:       "FakeAWS::RDS::DBInstance",
				Stack:      "default",
				Target:     "aws",
				Properties: dbProps,
			},
		},
	}
	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginDir,
		Dependencies:   deps,
	}

	out, err := PKL{}.SerializeForma(forma, options)
	require.NoError(t, err)

	// The import for the referenced resolvable's module must be emitted, under
	// the alias the reference expression uses.
	assert.Contains(t, out, `import "@fakeaws/secretsmanager/secret.pkl" as secret`,
		"the referenced resolvable's module must be imported")

	// The reference must render as a live Resolvable declaration terminating in
	// the mapped property accessor — asserted whole so a stray substring match
	// (a comment or the import line) cannot satisfy it.
	assert.Contains(t, out, strings.Join([]string{
		`    masterUserPassword = new secret.SecretResolvable {`,
		`      // RealValue: hunter2`,
		`      stack = default.res.label`,
		`      label = "db-secret"`,
		`    }.secretString`,
	}, "\n"), "the reference must render as a Resolvable declaration ending in the mapped accessor")

	// The emitted PKL must evaluate: write it out as a project and read it back.
	dir := t.TempDir()
	res, err := PKL{}.GenerateSourceCode(forma, filepath.Join(dir, "out.pkl"), nil, options)
	require.NoError(t, err)

	evaluated, err := PKL{}.Evaluate(res.TargetPath, model.CommandApply, model.FormaApplyModeReconcile, nil)
	require.NoError(t, err, "the emitted PKL must evaluate")
	require.Len(t, evaluated.Resources, 2)

	// The reference must survive the round trip as a $res envelope, not as the
	// baked-out literal value.
	var appDB *model.Resource
	for i := range evaluated.Resources {
		if evaluated.Resources[i].Label == "app-db" {
			appDB = &evaluated.Resources[i]
		}
	}
	require.NotNil(t, appDB, "the referencing resource must survive evaluation")

	var props map[string]any
	require.NoError(t, json.Unmarshal(appDB.Properties, &props))
	password, ok := props["MasterUserPassword"].(map[string]any)
	require.True(t, ok, "the referencing property must round-trip as an envelope, got %v", props["MasterUserPassword"])
	assert.Equal(t, true, password["$res"])
	assert.Equal(t, "db-secret", password["$label"])
	assert.Equal(t, "FakeAWS::SecretsManager::Secret", password["$type"])
	assert.Equal(t, "SecretString", password["$property"])
	assert.NotContains(t, string(appDB.Properties), "hunter2",
		"the referenced value must not be baked in as a literal")
}

// TestSerializeForma_ReferenceToUndeclaredResolvable_FailsActionably covers a
// $res envelope naming a resource type that no schema declares a resolvable
// for. There is no import to emit, so generation must stop with a message
// naming the type and the reference that carried it, rather than either dying
// inside PKL or quietly emitting a forma file with a missing reference.
func TestSerializeForma_ReferenceToUndeclaredResolvable_FailsActionably(t *testing.T) {
	deps, pluginDir := fakeawsDeps(t)

	props := json.RawMessage(`{"DbInstanceClass":"db.t3.micro","Engine":"postgres","MasterUserPassword":{"$res":true,"$label":"ghost","$type":"FakeAWS::Nonexistent::Thing","$stack":"default","$property":"Value","$visibility":"Clear","$value":"hunter2"}}`)

	forma := &model.Forma{
		Stacks:  []model.Stack{{Label: "default"}},
		Targets: []model.Target{fakeawsTarget()},
		Resources: []model.Resource{{
			Label:      "app-db",
			Type:       "FakeAWS::RDS::DBInstance",
			Stack:      "default",
			Target:     "aws",
			Properties: props,
		}},
	}
	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginDir,
		Dependencies:   deps,
	}

	_, err := PKL{}.SerializeForma(forma, options)
	require.Error(t, err, "a reference to an undeclared resolvable must not serialize")

	msg := err.Error()
	assert.Contains(t, msg, "FakeAWS::Nonexistent::Thing", "the error must name the unresolvable type")
	assert.Contains(t, msg, `reference to resource "ghost" in stack "default"`,
		"the error must identify which reference failed")
	assert.NotContains(t, msg, "Cannot find property", "the failure must not surface as a raw PKL property error")
}

// TestSerializeForma_ResourceHintOnNonResourceClass_FailsActionably covers a
// schema class that carries a resource hint but does not extend
// formae.Resource. Rendering it is impossible, so generation must stop with a
// message naming the resource, its type and the schema class at fault, rather
// than folding the rejection text into the forma file as a property no class
// declares.
func TestSerializeForma_ResourceHintOnNonResourceClass_FailsActionably(t *testing.T) {
	deps, pluginDir := fakeawsDeps(t)

	forma := &model.Forma{
		Stacks:  []model.Stack{{Label: "default"}},
		Targets: []model.Target{fakeawsTarget()},
		Resources: []model.Resource{{
			Label:      "app-widget",
			Type:       "FakeAWS::Misdeclared::Widget",
			Stack:      "default",
			Target:     "aws",
			Properties: json.RawMessage(`{"WidgetId":"w-1"}`),
		}},
	}
	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginDir,
		Dependencies:   deps,
	}

	out, err := PKL{}.SerializeForma(forma, options)
	assert.NotContains(t, out, `message = "applyResource`,
		"the rejection must not be rendered into the forma file")
	require.Error(t, err, "a class that is not a formae.Resource must not serialize")

	msg := err.Error()
	assert.Contains(t, msg, `resource "app-widget" in stack "default"`,
		"the error must identify which resource failed")
	assert.Contains(t, msg, "FakeAWS::Misdeclared::Widget", "the error must name the resource type")
	assert.Contains(t, msg, "fakeaws.misdeclared.widget.Widget",
		"the error must name the schema class, qualified by its module")
	assert.Contains(t, msg, "must extend formae.Resource", "the error must say what the schema has to change")
	assert.NotContains(t, msg, "Cannot find property", "the failure must not surface as a raw PKL property error")
	assert.NotContains(t, msg, "docComment", "the error must name the class, not dump the reflected class graph")
}

// TestGenPkl_NoUnguardedResolvableLookup pins the invariant behind the test
// above. Every site that renders a reference reads the resolvable map, but only
// the first one reached can surface its error, so a behavioural test cannot
// tell whether the others are still dereferencing getOrNull directly. Assert on
// the source instead: no lookup may read a field straight off the nullable
// result. Guarded uses that bind the result and null-check it — the embed path
// deliberately falls back to a literal — do not match.
func TestGenPkl_NoUnguardedResolvableLookup(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("generator", "gen.pkl"))
	require.NoError(t, err)

	unguarded := regexp.MustCompile(`MapResolvableResourceUri\(\)\.getOrNull\([^()]*\)\.`)
	assert.Empty(t, unguarded.FindAllString(string(src), -1),
		"resolvable lookups must go through requireResolvableInfo, not dereference getOrNull directly")
}
