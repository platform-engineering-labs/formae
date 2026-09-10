//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package pkl

import (
	"encoding/json"
	"github.com/platform-engineering-labs/formae/internal/schema"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestDesiredExtractionRejectsUnknownEssentialDeclaration(t *testing.T) {
	deps, _ := fakeawsDeps(t)
	f := &model.Forma{Extraction: &model.ExtractionContext{}, Stacks: []model.Stack{{Label: "stack"}}, Targets: []model.Target{fakeawsTarget()}, Resources: []model.Resource{{Stack: "stack", Target: "aws", Label: "secret", Type: "FakeAWS::SecretsManager::Secret", Properties: json.RawMessage(`{"Name":"secret","NotInInstalledSchema":"must-not-disappear"}`)}}}
	_, err := PKL{}.SerializeForma(f, &schema.SerializeOptions{Schema: "pkl", SchemaLocation: schema.SchemaLocationLocal, Dependencies: deps})
	require.ErrorContains(t, err, "unsupported desired declaration fields", "complete desired extraction must not silently drop authored fields")
	for _, raw := range []string{`{"Type":"future-policy","Label":"important"}`, `{"Type":"ttl","Label":"broken","TTLSeconds":1,"ExpiresAt":"2030-01-01T00:00:00Z"}`} {
		f.Resources[0].Properties = json.RawMessage(`{"Name":"secret"}`)
		f.Policies = []json.RawMessage{json.RawMessage(raw)}
		_, err = PKL{}.SerializeForma(f, &schema.SerializeOptions{Schema: "pkl", SchemaLocation: schema.SchemaLocationLocal, Dependencies: deps})
		require.Error(t, err)
	}
}
func TestDesiredExtractionPreprocessingPreservesExactIntegers(t *testing.T) {
	f := &model.Forma{Extraction: &model.ExtractionContext{}, Resources: []model.Resource{{Properties: json.RawMessage(`{"value":9007199254740993}`)}}}
	processed, err := prepareDesiredExtraction(f)
	require.NoError(t, err)
	require.JSONEq(t, `{"value":9007199254740993}`, string(processed.Resources[0].Properties))
	require.Contains(t, string(processed.Resources[0].Properties), "9007199254740993")
}

func TestDesiredExtractionRejectsUnsupportedReferenceAnnotations(t *testing.T) {
	for _, property := range []string{
		`{"$res":true,"$type":"FakeAWS::SecretsManager::Secret","$label":"r","$stack":"s","$property":"Name","$transform":{"unknown":true}}`,
		`{"$res":true,"$type":"FakeAWS::SecretsManager::Secret","$label":"r","$stack":"s","$property":"Name","$strategy":"SetOnce"}`,
	} {
		_, err := prepareDesiredExtraction(&model.Forma{Extraction: &model.ExtractionContext{}, Resources: []model.Resource{{Properties: json.RawMessage(`{"Name":` + property + `}`)}}})
		require.ErrorContains(t, err, "unsupported desired reference")
	}
}

func TestDesiredExtractionRejectsUnsupportedEmbeddedSelector(t *testing.T) {
	envelope := `{"$res":true,"$type":"FakeAWS::SecretsManager::Secret","$label":"r","$stack":"s","$property":"SecretString","$json":"key"}`
	raw, err := json.Marshal(map[string]any{"SecretString": map[string]any{"$embed": true, "$template": model.FrameEnvelope(envelope)}})
	require.NoError(t, err)
	_, err = prepareDesiredExtraction(&model.Forma{Extraction: &model.ExtractionContext{}, Resources: []model.Resource{{Properties: raw}}})
	require.ErrorContains(t, err, "unsupported desired reference JSON selector inside an embed")
}

func TestDesiredTargetConfigStrictPreprocessing(t *testing.T) {
	for _, target := range []model.Target{
		{Config: json.RawMessage(`{"profile":"x"}`), ConfigSchema: model.ConfigSchema{Hints: map[string]model.ConfigFieldHint{"profile": {CreateOnly: true}}}},
		{Config: json.RawMessage(`{"Profile":"x","profile":"y"}`)},
		{Config: json.RawMessage(`{"ref":{"$res":true,"$transform":{"unknown":true}}}`)},
		{Config: json.RawMessage(`{"password":{"$gen":true,"$label":"missing","$stack":"owner","$output":"value"}}`)},
	} {
		_, err := prepareDesiredExtraction(&model.Forma{Extraction: &model.ExtractionContext{}, Targets: []model.Target{target}})
		require.Error(t, err)
	}
}

func TestDesiredTargetNestedHashedLiteralRoundTrip(t *testing.T) {
	for _, test := range []struct{ name, config string }{
		{"mapping", `{"Auth":{"token":{"$value":"digest","$hashed":true,"$visibility":"Opaque","$strategy":"Update"},"zSibling":"kept"},"After":"retained"}`},
		{"listing", `{"Auth":[{"$value":"digest","$hashed":true,"$visibility":"Opaque","$strategy":"Update"},"kept"],"After":"retained"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps, _ := fakeawsDeps(t)
			target := fakeawsTarget()
			target.Config = json.RawMessage(test.config)
			f := &model.Forma{Extraction: &model.ExtractionContext{}, Stacks: []model.Stack{{Label: "stack"}}, Targets: []model.Target{target}}
			path := filepath.Join(t.TempDir(), "desired.pkl")
			_, err := PKL{}.GenerateSourceCode(f, path, nil, &schema.SerializeOptions{Schema: "pkl", SchemaLocation: schema.SchemaLocationLocal, Dependencies: deps})
			require.NoError(t, err)
			source, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Contains(t, string(source), ".opaque.hashed")
			require.Contains(t, string(source), "// hashed secret value")
			evaluated, err := PKL{}.Evaluate(path, model.CommandApply, model.FormaApplyModeReconcile, nil)
			require.NoError(t, err)
			require.Len(t, evaluated.Targets, 1)
			require.JSONEq(t, test.config, string(evaluated.Targets[0].Config), "hashed marker and following siblings must survive")
		})
	}
}

func TestDesiredMembershipNamesDoNotShadow(t *testing.T) {
	deps, _ := fakeawsDeps(t)
	for _, labels := range [][2]string{{"stack", "aws"}, {"production", "target"}, {"stack", "target"}} {
		t.Run(labels[0]+"-"+labels[1], func(t *testing.T) {
			target := fakeawsTarget()
			target.Label = labels[1]
			generator, err := json.Marshal(&model.PasswordGenerator{Label: "password", Stack: labels[0], Length: 20, Uppercase: true, Lowercase: true, Digits: true})
			require.NoError(t, err)
			f := &model.Forma{Extraction: &model.ExtractionContext{CompleteStacks: []model.Stack{{Label: labels[0]}}}, Stacks: []model.Stack{{Label: labels[0]}}, Targets: []model.Target{target}, Generators: []json.RawMessage{generator}, Resources: []model.Resource{{Stack: labels[0], Target: labels[1], Label: "secret", Type: "FakeAWS::SecretsManager::Secret", Properties: json.RawMessage(`{"Name":"secret"}`)}}}
			path := filepath.Join(t.TempDir(), "desired.pkl")
			_, err = (PKL{}).GenerateSourceCode(f, path, nil, &schema.SerializeOptions{Schema: "pkl", SchemaLocation: schema.SchemaLocationLocal, Dependencies: deps})
			require.NoError(t, err)
			evaluated, err := (PKL{}).Evaluate(path, model.CommandApply, model.FormaApplyModeReconcile, nil)
			require.NoError(t, err)
			require.Len(t, evaluated.Resources, 1)
			require.Equal(t, labels[0], evaluated.Resources[0].Stack)
			require.Equal(t, labels[1], evaluated.Resources[0].Target)
			require.Len(t, evaluated.Stacks, 1)
			require.Equal(t, labels[0], evaluated.Stacks[0].Label)
			require.Len(t, evaluated.Targets, 1)
			require.Equal(t, labels[1], evaluated.Targets[0].Label)
			require.Len(t, evaluated.Generators, 1)
			g, err := model.ParseGenerator(evaluated.Generators[0])
			require.NoError(t, err)
			require.Equal(t, labels[0], g.GetStack())
		})
	}
}
