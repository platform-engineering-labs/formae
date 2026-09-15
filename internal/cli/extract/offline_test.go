//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package extract

import (
	"bytes"
	"encoding/json"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/schema"
	"github.com/platform-engineering-labs/formae/internal/schema/pkl"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineBundleValidationAndExactNumbers(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"Forma":{},"Plugins":null}`, `{"Forma":{},"Plugins":[],"Unknown":true}`, `{"Forma":{},"Plugins":[]} {}`} {
		_, err := readRenderBundle(strings.NewReader(raw))
		require.Error(t, err, raw)
	}
	bundle, err := readRenderBundle(strings.NewReader(`{"Forma":{"Extraction":{"CompleteStacks":[]},"Targets":[{"Label":"t","Config":{"number":9007199254740993}}]},"Plugins":[]}`))
	require.NoError(t, err)
	require.Contains(t, string(bundle.Forma.Targets[0].Config), "9007199254740993")
	require.NotNil(t, bundle.Forma.Extraction)
}

func TestOfflineExtractRealStrictRenderWithoutProfile(t *testing.T) {
	for _, kind := range []string{"empty", "target", "generator-reference"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			core, err := filepath.Abs("../../schema/pkl/schema/PklProject")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "PklProject"), []byte("amends \"pkl:Project\"\ndependencies { [\"formae\"] = import(\""+core+"\") }\n"), 0600))
			f := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "empty"}}, Extraction: &pkgmodel.ExtractionContext{CompleteStacks: []pkgmodel.Stack{{Label: "empty"}}}}
			if kind != "empty" {
				f.Targets = []pkgmodel.Target{{Label: "t", Namespace: "test", Config: []byte(`{"number":9007199254740993}`)}}
			}
			if kind == "generator-reference" {
				f.Extraction.ReferenceGenerators = []json.RawMessage{json.RawMessage(`{"Type":"password","Label":"password","Stack":"other","Length":20,"Uppercase":true,"Lowercase":true,"Digits":true,"Symbols":false,"ExcludeCharacters":"","RequireEachIncludedType":true}`)}
				f.Targets[0].Config = []byte(`{"password":{"$gen":true,"$label":"password","$stack":"other","$output":"value"},"number":9007199254740993}`)
			}
			raw, err := json.Marshal(map[string]any{"Forma": f, "Plugins": []any{}})
			require.NoError(t, err)
			command := ExtractCmd()
			command.SilenceUsage = true
			var out bytes.Buffer
			command.SetOut(&out)
			command.SetIn(bytes.NewReader(raw))
			path := filepath.Join(dir, "desired.pkl")
			command.SetArgs([]string{"--from-json", "-", "--profile", "missing-profile-must-not-be-read", "--yes", path})
			require.NoError(t, command.Execute())
			evaluated, err := (pkl.PKL{}).Evaluate(path, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile, nil)
			require.NoError(t, err)
			require.Len(t, evaluated.Stacks, 1)
			require.Equal(t, "empty", evaluated.Stacks[0].Label)
			require.Empty(t, evaluated.Resources)
			if kind != "empty" {
				require.Contains(t, string(evaluated.Targets[0].Config), "9007199254740993")
			}
			if kind == "generator-reference" {
				require.Empty(t, evaluated.Generators)
				var config map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(evaluated.Targets[0].Config, &config))
				var reference map[string]any
				require.NoError(t, json.Unmarshal(config["password"], &reference))
				require.Equal(t, "other", reference["$stack"])
			}
			require.FileExists(t, path)
		})
	}
}

func TestDesiredEmptyStackIsRendered(t *testing.T) {
	old, gen := extractDesiredFn, generateFn
	t.Cleanup(func() { extractDesiredFn = old; generateFn = gen })
	called := false
	extractDesiredFn = func(_ *app.App, query string) (*pkgmodel.Forma, error) {
		return &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "empty"}}, Extraction: &pkgmodel.ExtractionContext{CompleteStacks: []pkgmodel.Stack{{Label: "empty"}}}}, nil
	}
	generateFn = func(_ *app.App, f *pkgmodel.Forma, path, output string, location schema.SchemaLocation) (schema.GenerateSourcesResult, error) {
		called = true
		require.Len(t, f.Extraction.CompleteStacks, 1)
		return schema.GenerateSourcesResult{}, nil
	}
	require.NoError(t, runExtractCore(&app.App{Config: &pkgmodel.Config{}}, &ExtractOptions{Desired: true, TargetPath: filepath.Join(t.TempDir(), "empty.pkl"), Query: "stack:empty", OutputSchema: "pkl", Yes: true}))
	require.True(t, called)
}

func TestOfflineExtractOutputConflictAndStrictFields(t *testing.T) {
	dir := t.TempDir()
	core, err := filepath.Abs("../../schema/pkl/schema/PklProject")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "PklProject"), []byte("amends \"pkl:Project\"\ndependencies { [\"formae\"] = import(\""+core+"\") }\n"), 0600))
	fake, err := filepath.Abs("../../testplugin/fakeaws/schema/pkl/PklProject")
	require.NoError(t, err)
	forma := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "stack"}}, Extraction: &pkgmodel.ExtractionContext{CompleteStacks: []pkgmodel.Stack{{Label: "stack"}}}, Targets: []pkgmodel.Target{{Label: "aws", Namespace: "FakeAWS", Config: json.RawMessage(`{"Type":"FakeAWS","Region":"us-east-1"}`)}}, Resources: []pkgmodel.Resource{{Stack: "stack", Target: "aws", Type: "FakeAWS::SecretsManager::Secret", Label: "secret", Properties: json.RawMessage(`{"Name":"retained"}`)}}}
	plugins := []apimodel.Plugin{{Type: "resource", Namespace: "FakeAWS", InstalledVersion: "1.2.3", LocalPath: fake}}
	execute := func(yes bool) error {
		raw, err := json.Marshal(RenderBundle{Forma: forma, Plugins: plugins})
		require.NoError(t, err)
		command := ExtractCmd()
		command.SilenceUsage = true
		command.SetIn(bytes.NewReader(raw))
		args := []string{"--from-json", "-", "--schema-location", "local", "--config", "/does/not/exist", filepath.Join(dir, "desired")}
		if yes {
			args = append(args, "--yes")
		}
		command.SetArgs(args)
		return command.Execute()
	}
	require.NoError(t, execute(true))
	path := filepath.Join(dir, "desired.pkl")
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	evaluated, err := (pkl.PKL{}).Evaluate(path, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile, nil)
	require.NoError(t, err)
	require.Len(t, evaluated.Resources, 1)
	require.Equal(t, "secret", evaluated.Resources[0].Label)
	require.ErrorContains(t, execute(false), "pass --yes")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	forma.Resources[0].Properties = json.RawMessage(`{"Name":"retained","UnknownEssential":"must not disappear"}`)
	require.Error(t, execute(true), "strict desired renderer must reject unknown essential fields")
	after, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestDesiredIncompleteDocumentReachesExistingExtractRenderer(t *testing.T) {
	old, gen := extractDesiredFn, generateFn
	t.Cleanup(func() { extractDesiredFn = old; generateFn = gen })
	ref := "formae://3J9W5cCOO9hsfDmzLXyLkJAAdVK#/Arn"
	extractDesiredFn = func(_ *app.App, query string) (*pkgmodel.Forma, error) {
		require.Equal(t, "stack:stack", query)
		return &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "stack"}}, Extraction: &pkgmodel.ExtractionContext{CompleteStacks: []pkgmodel.Stack{{Label: "stack"}}, Diagnostics: []pkgmodel.ExtractionDiagnostic{{Code: "unresolved_desired_reference", Path: "/Resources/0/Properties/SecretString", Reference: ref}}}, Targets: []pkgmodel.Target{{Label: "aws", Namespace: "FakeAWS", Config: json.RawMessage(`{"Type":"FakeAWS","Region":"us-east-1"}`)}}, Resources: []pkgmodel.Resource{{Label: "consumer", Stack: "stack", Target: "aws", Type: "FakeAWS::SecretsManager::Secret", Properties: json.RawMessage(`{"Name":"kept","SecretString":{"$ref":"` + ref + `"}}`)}}}, nil
	}
	generateFn = func(_ *app.App, f *pkgmodel.Forma, path, output string, location schema.SchemaLocation) (schema.GenerateSourcesResult, error) {
		core, err := filepath.Abs("../../schema/pkl/schema/PklProject")
		require.NoError(t, err)
		provider, err := filepath.Abs("../../testplugin/fakeaws/schema/pkl/PklProject")
		require.NoError(t, err)
		return (pkl.PKL{}).GenerateSourceCode(f, path, nil, &schema.SerializeOptions{Schema: "pkl", SchemaLocation: schema.SchemaLocationLocal, Dependencies: []string{"local:formae:" + core, "local:fakeaws:" + provider}})
	}
	path := filepath.Join(t.TempDir(), "desired.pkl")
	require.NoError(t, runExtractCore(&app.App{Config: &pkgmodel.Config{}}, &ExtractOptions{Desired: true, TargetPath: path, Query: "stack:stack", OutputSchema: "pkl", Yes: true}))
	_, err := (pkl.PKL{}).Evaluate(path, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile, nil)
	require.ErrorContains(t, err, "Unresolved desired reference")
	require.FileExists(t, path)
}
