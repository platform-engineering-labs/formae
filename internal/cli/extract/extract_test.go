// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package extract

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/schema"
	jsonschema "github.com/platform-engineering-labs/formae/internal/schema/json"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestValidateExtractOptions(t *testing.T) {
	t.Run("missing target path", func(t *testing.T) {
		opts := &ExtractOptions{
			TargetPath: "",
		}
		err := validateExtractOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "target file is required", err.Error())
	})

	t.Run("target path is a directory", func(t *testing.T) {
		dir := t.TempDir()
		opts := &ExtractOptions{
			TargetPath: dir,
			Query:      "type:AWS::S3::Bucket",
		}
		err := validateExtractOptions(opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "is a directory, not a file")
	})

	t.Run("missing query", func(t *testing.T) {
		opts := &ExtractOptions{
			TargetPath: "output.pkl",
			Query:      "",
		}
		err := validateExtractOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "query is required", err.Error())
	})

}

func TestValidateExtractOptionsBoundsJSONToCompleteDesiredState(t *testing.T) {
	tests := []struct {
		name string
		opts ExtractOptions
		want string
	}{
		{name: "complete desired", opts: ExtractOptions{Desired: true, TargetPath: "desired.json", Query: "stack:service", OutputSchema: "json", SchemaLocation: schema.SchemaLocationRemote}},
		{name: "plain query", opts: ExtractOptions{TargetPath: "desired.json", Query: "stack:service", OutputSchema: "json"}, want: "only supported with --desired"},
		{name: "recorded command", opts: ExtractOptions{CommandID: "command", TargetPath: "desired.json", OutputSchema: "json"}, want: "only supported with --desired"},
		{name: "offline bundle", opts: ExtractOptions{Desired: true, FromJSON: "bundle.json", TargetPath: "desired.json", OutputSchema: "json"}, want: "only supported with --desired"},
		{name: "local schema", opts: ExtractOptions{Desired: true, TargetPath: "desired.json", Query: "stack:service", OutputSchema: "json", SchemaLocation: schema.SchemaLocationLocal}, want: "schema-location local"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExtractOptions(&tt.opts)
			if tt.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.want)
			}
		})
	}
}

func TestRunExtractPromptsWithJSONDefaultPath(t *testing.T) {
	oldInteractive, oldPrompt := isInteractive, promptPath
	t.Cleanup(func() { isInteractive = oldInteractive; promptPath = oldPrompt })
	isInteractive = func() bool { return true }
	stop := errors.New("stop after prompt")
	var defaultPath string
	promptPath = func(_ *theme.Theme, suggested string) (string, error) {
		defaultPath = suggested
		return "", stop
	}
	err := runExtract(&app.App{Config: &pkgmodel.Config{}}, &ExtractOptions{OutputSchema: "json"})
	require.ErrorIs(t, err, stop)
	require.Equal(t, "./extracted.json", defaultPath)
}

func TestDesiredJSONExtractionWritesLosslessProtectedReplayWithoutAgentDependencies(t *testing.T) {
	oldExtract := extractDesiredFn
	t.Cleanup(func() { extractDesiredFn = oldExtract })
	want := &pkgmodel.Forma{
		Extraction:  &pkgmodel.ExtractionContext{CompleteStacks: []pkgmodel.Stack{{Label: "service"}}, ReferenceGenerators: []json.RawMessage{json.RawMessage(`{"Type":"password","Label":"outside","Stack":"outside","Length":12,"Uppercase":true,"Lowercase":true,"Digits":true,"Symbols":false,"ExcludeCharacters":"","RequireEachIncludedType":true}`)}},
		Description: pkgmodel.Description{Text: "review this", Confirm: true},
		Properties:  map[string]pkgmodel.Prop{},
		Stacks:      []pkgmodel.Stack{{Label: "service"}},
		Resources:   []pkgmodel.Resource{{Label: "task", Type: "AWS::ECS::TaskDefinition", Stack: "service", Target: "aws", Properties: json.RawMessage(`{"ImageRef":{"$value":"frozen","$visibility":"Opaque","$strategy":"SetOnce"}}`)}},
	}
	extractDesiredFn = func(_ *app.App, query string) (*pkgmodel.Forma, error) {
		require.Equal(t, "stack:service", query)
		return want, nil
	}
	targetBase := filepath.Join(t.TempDir(), "desired")
	target := targetBase + ".json"
	opts := &ExtractOptions{Desired: true, TargetPath: targetBase, pathExplicit: true, Query: "stack:service", OutputSchema: "json", SchemaLocation: schema.SchemaLocationRemote, Yes: true}
	require.NoError(t, runExtract(&app.App{Config: &pkgmodel.Config{}}, opts))
	info, err := os.Stat(target)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	got, err := (jsonschema.JSON{}).Evaluate(target, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile, nil)
	require.NoError(t, err)
	wantWire, err := json.Marshal(want)
	require.NoError(t, err)
	gotWire, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, string(wantWire), string(gotWire))
}

func TestSchemaVersionNag(t *testing.T) {
	u := &schema.SchemaVersionUpgrade{ProjectDir: "/tmp/proj", Current: "0.85.0", Target: "0.88.0"}
	msg := schemaVersionNag(u)
	assert.Contains(t, msg, "/tmp/proj/PklProject")
	assert.Contains(t, msg, "is using formae version 0.85.0")
	assert.Contains(t, msg, "CLI is at version 0.88.0")
	assert.Contains(t, msg, "update to 0.88.0 or greater")
	assert.Contains(t, msg, "pkl project resolve")
}
