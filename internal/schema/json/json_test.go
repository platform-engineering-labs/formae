// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package json

import (
	stdjson "encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/schema"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func frozenFormaFixture() *model.Forma {
	sensitive := true
	return &model.Forma{
		Extraction: &model.ExtractionContext{
			Diagnostics:         []model.ExtractionDiagnostic{{Code: "external-reference", Path: "/Resources/0/Properties/Secret", Reference: "formae://external#/Value", Message: "kept for source repair"}},
			CompleteStacks:      []model.Stack{{Label: "service"}},
			ReferenceGenerators: []stdjson.RawMessage{stdjson.RawMessage(`{"Type":"password","Label":"external","Stack":"external","Length":12,"Uppercase":true,"Lowercase":true,"Digits":true,"Symbols":false,"ExcludeCharacters":"","RequireEachIncludedType":true}`)},
		},
		Description: model.Description{Text: "reviewed deployment", Confirm: true},
		Properties: map[string]model.Prop{
			"generation": {Source: "declaration", Sensitive: &sensitive, Value: stdjson.Number("9007199254740993"), Type: "UInt"},
		},
		Stacks:     []model.Stack{{Label: "service", Description: "service stack", Policies: []stdjson.RawMessage{stdjson.RawMessage(`{"$ref":"policy://expiry"}`)}}},
		Targets:    []model.Target{{Label: "aws", Namespace: "aws", Config: stdjson.RawMessage(`{"region":"us-west-2","account":"123456789012"}`), Discoverable: true}},
		Policies:   []stdjson.RawMessage{stdjson.RawMessage(`{"Type":"ttl","Label":"expiry","TTLSeconds":3600}`)},
		Generators: []stdjson.RawMessage{stdjson.RawMessage(`{"Type":"password","Label":"token","Stack":"service","Length":12,"Uppercase":true,"Lowercase":true,"Digits":true,"Symbols":false,"ExcludeCharacters":"","RequireEachIncludedType":true}`)},
		Resources: []model.Resource{{
			Label: "task", Type: "AWS::ECS::TaskDefinition", Stack: "service", Target: "aws",
			Schema:     model.Schema{Identifier: "AWS::ECS::TaskDefinition", Fields: []string{"Secret"}, Hints: map[string]model.FieldHint{"Secret": {Opaque: true, EdgeKind: model.EdgeKindDefault}}},
			Properties: stdjson.RawMessage(`{"Secret":{"$value":"opaque-generated-once","$visibility":"Opaque","$strategy":"SetOnce"},"Count":9007199254740993}`),
		}},
	}
}

func TestFullFormaRoundTripsAcrossFreshJSONEvaluations(t *testing.T) {
	want := frozenFormaFixture()
	wire, err := (JSON{}).SerializeForma(want, &schema.SerializeOptions{Beautify: true})
	require.NoError(t, err)
	require.Contains(t, wire, `"Extraction"`)
	require.Contains(t, wire, `"Description"`)
	require.Contains(t, wire, `"Properties"`)
	emptyWire, err := (JSON{}).SerializeForma(&model.Forma{}, &schema.SerializeOptions{})
	require.NoError(t, err)
	require.JSONEq(t, `{"Description":{},"Properties":{}}`, emptyWire)

	path := filepath.Join(t.TempDir(), "frozen.json")
	require.NoError(t, os.WriteFile(path, []byte(wire), 0600))
	first, err := (JSON{}).Evaluate(path, model.CommandApply, model.FormaApplyModeReconcile, nil)
	require.NoError(t, err)
	second, err := (JSON{}).Evaluate(path, model.CommandEval, model.FormaApplyModePatch, map[string]string{})
	require.NoError(t, err)
	wantWire, err := (JSON{}).SerializeForma(want, &schema.SerializeOptions{})
	require.NoError(t, err)
	firstWire, err := (JSON{}).SerializeForma(first, &schema.SerializeOptions{})
	require.NoError(t, err)
	secondWire, err := (JSON{}).SerializeForma(second, &schema.SerializeOptions{})
	require.NoError(t, err)
	require.JSONEq(t, wantWire, firstWire)
	require.JSONEq(t, firstWire, secondWire)
	require.Equal(t, stdjson.Number("9007199254740993"), first.Properties["generation"].Value)
	require.JSONEq(t, `{"Secret":{"$value":"opaque-generated-once","$visibility":"Opaque","$strategy":"SetOnce"},"Count":9007199254740993}`, string(first.Resources[0].Properties))

	changed := strings.Replace(wire, "opaque-generated-once", "changed-intent", 1)
	require.NoError(t, os.WriteFile(path, []byte(changed), 0600))
	changedForma, err := (JSON{}).Evaluate(path, model.CommandApply, model.FormaApplyModeReconcile, nil)
	require.NoError(t, err)
	changedWire, err := (JSON{}).SerializeForma(changedForma, &schema.SerializeOptions{})
	require.NoError(t, err)
	require.NotEqual(t, firstWire, changedWire)
}

func TestEvaluateRejectsUnsupportedCommandsAndPropertyOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frozen.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"Description":{},"Properties":{}}`), 0600))

	for _, command := range []model.Command{model.CommandApply, model.CommandEval} {
		_, err := (JSON{}).Evaluate(path, command, model.FormaApplyModeReconcile, nil)
		require.NoError(t, err)
	}
	for _, command := range []model.Command{model.CommandDestroy, model.CommandSync, model.Command("future")} {
		_, err := (JSON{}).Evaluate(path, command, model.FormaApplyModeReconcile, nil)
		require.ErrorContains(t, err, "does not support command")
	}
	_, err := (JSON{}).Evaluate(path, model.CommandApply, model.FormaApplyModeReconcile, map[string]string{"token": "changed"})
	require.ErrorContains(t, err, "property overrides")
}

func TestEvaluateRejectsMalformedAmbiguousAndPartialJSON(t *testing.T) {
	tests := map[string]struct{ input, want string }{
		"empty":                  {input: "", want: "EOF"},
		"null":                   {input: "null", want: "top-level value must be an object"},
		"array":                  {input: `[]`, want: "top-level value must be an object"},
		"scalar":                 {input: `"forma"`, want: "top-level value must be an object"},
		"malformed":              {input: `{"Description":`, want: "EOF"},
		"second value":           {input: `{"Description":{},"Properties":{}} {}`, want: "multiple top-level values"},
		"unknown root field":     {input: `{"Description":{},"Properties":{},"Unknown":true}`, want: "unknown field"},
		"unknown typed field":    {input: `{"Description":{"Unknown":true},"Properties":{}}`, want: "unknown field"},
		"unknown property field": {input: `{"Description":{},"Properties":{"token":{"Value":"kept-secret","Unknown":true}}}`, want: "unknown field"},
		"unknown schema hint":    {input: `{"Description":{},"Properties":{},"Resources":[{"Label":"r","Type":"Test::R","Stack":"s","Target":"t","Schema":{"Identifier":"Test::R","Fields":["Name"],"Hints":{"Name":{"Unknown":true}},"Discoverable":false,"Extractable":false,"Portable":false,"Parent":"","ParentMappings":[]},"Properties":{}}]}`, want: "unknown field"},
		"duplicate root field":   {input: `{"Description":{},"Description":{},"Properties":{}}`, want: "duplicate object member"},
		"duplicate typed field":  {input: `{"Description":{"Text":"a","Text":"b"},"Properties":{}}`, want: "duplicate object member"},
		"duplicate opaque field": {input: `{"Description":{},"Properties":{},"Resources":[{"Label":"r","Type":"Test::R","Stack":"s","Target":"t","Schema":{"Identifier":"Test::R","Fields":[],"Hints":{},"Discoverable":false,"Extractable":false,"Portable":false,"Parent":"","ParentMappings":[]},"Properties":{"secret":"one","secret":"two"}}]}`, want: "duplicate object member"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "frozen.json")
			require.NoError(t, os.WriteFile(path, []byte(test.input), 0600))
			_, err := (JSON{}).Evaluate(path, model.CommandApply, model.FormaApplyModeReconcile, nil)
			require.ErrorContains(t, err, test.want)
			require.NotContains(t, err.Error(), "one")
			require.NotContains(t, err.Error(), "two")
			require.NotContains(t, err.Error(), "kept-secret")
		})
	}
}

func TestProjectPropertiesIsEmptyAndSupportsDesiredExtraction(t *testing.T) {
	properties, err := (JSON{}).ProjectProperties("ignored.json")
	require.NoError(t, err)
	require.NotNil(t, properties)
	require.Empty(t, properties)
	require.True(t, (JSON{}).SupportsExtract())
}

func TestGenerateSourceCodeAtomicallyWritesProtectedFullForma(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "desired.json")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0644))

	result, err := (JSON{}).GenerateSourceCode(frozenFormaFixture(), target, nil, &schema.SerializeOptions{Schema: "json"})
	require.NoError(t, err)
	require.Equal(t, target, result.TargetPath)
	require.Equal(t, 1, result.ResourceCount)
	info, err := os.Stat(target)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	roundTripped, err := (JSON{}).Evaluate(target, model.CommandApply, model.FormaApplyModeReconcile, nil)
	require.NoError(t, err)
	wantWire, err := (JSON{}).SerializeForma(frozenFormaFixture(), &schema.SerializeOptions{})
	require.NoError(t, err)
	gotWire, err := (JSON{}).SerializeForma(roundTripped, &schema.SerializeOptions{})
	require.NoError(t, err)
	require.JSONEq(t, wantWire, gotWire)

	broken := frozenFormaFixture()
	broken.Resources[0].Properties = stdjson.RawMessage(`{"unterminated"`)
	before, err := os.ReadFile(target)
	require.NoError(t, err)
	_, err = (JSON{}).GenerateSourceCode(broken, target, nil, &schema.SerializeOptions{Schema: "json"})
	require.Error(t, err)
	after, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	require.Equal(t, before, after)
	matches, err := filepath.Glob(filepath.Join(dir, ".desired.json-*"))
	require.NoError(t, err)
	require.Empty(t, matches)
}
