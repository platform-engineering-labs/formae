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
	"regexp"
	"strings"
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

func TestDesiredUnresolvedReferenceCanBeEditedBeforeApply(t *testing.T) {
	deps, _ := fakeawsDeps(t)
	ref := "formae://3J9W5cCOO9hsfDmzLXyLkJAAdVK#/Arn"
	context := &model.ExtractionContext{}
	require.NoError(t, json.Unmarshal([]byte(`{"CompleteStacks":[{"Label":"stack"}],"Diagnostics":[{"Code":"unresolved_desired_reference","Path":"/Resources/0/Properties/SecretString","Reference":"`+ref+`","Message":"missing desired reference"}]}`), context))
	f := &model.Forma{Extraction: context, Stacks: []model.Stack{{Label: "stack"}}, Targets: []model.Target{fakeawsTarget()}, Resources: []model.Resource{{Stack: "stack", Target: "aws", Label: "consumer", Type: "FakeAWS::SecretsManager::Secret", Properties: json.RawMessage(`{"Name":"kept","SecretString":{"$ref":"` + ref + `","$visibility":"Opaque","$json":"token","$value":"do-not-emit"}}`)}}}
	path := filepath.Join(t.TempDir(), "desired.pkl")
	_, err := (PKL{}).GenerateSourceCode(f, path, nil, &schema.SerializeOptions{Schema: "pkl", SchemaLocation: schema.SchemaLocationLocal, Dependencies: deps})
	require.NoError(t, err, "a broken reference must not prevent obtaining the desired document")
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(source), ref)
	_, err = (PKL{}).Evaluate(path, model.CommandApply, model.FormaApplyModeReconcile, nil)
	require.ErrorContains(t, err, "Unresolved desired reference")
	require.ErrorContains(t, err, "rewire")
	// Replace precisely the non-executable expression, leaving the declaration.
	repaired := regexp.MustCompile(`throw\("(?:[^"\\]|\\.)*"\)`).ReplaceAllString(string(source), `(new secret.SecretResolvable { label = "replacement"; stack = "owner" }).secretValue`)
	require.NotEqual(t, string(source), repaired)
	require.NoError(t, os.WriteFile(path, []byte(repaired), 0600))
	evaluated, err := (PKL{}).Evaluate(path, model.CommandApply, model.FormaApplyModeReconcile, nil)
	require.NoError(t, err)
	require.Len(t, evaluated.Resources, 1)
	var repairedProps map[string]any
	require.NoError(t, json.Unmarshal(evaluated.Resources[0].Properties, &repairedProps))
	repairedRef := repairedProps["SecretString"].(map[string]any)
	require.Equal(t, "Opaque", repairedRef["$visibility"])
	require.Equal(t, "token", repairedRef["$json"])
	require.Equal(t, "replacement", repairedRef["$label"])
	require.NotContains(t, string(source), "do-not-emit")
	require.Contains(t, string(f.Resources[0].Properties), `"$ref"`, "renderer must not mutate transport declaration")
	// Removing the dependent declaration also repairs the document.
	removed := regexp.MustCompile(`(?s)  new secret.Secret \{.*?\n  \}\n`).ReplaceAllString(string(source), "")
	require.NotEqual(t, string(source), removed)
	require.NoError(t, os.WriteFile(path, []byte(removed), 0600))
	evaluated, err = (PKL{}).Evaluate(path, model.CommandApply, model.FormaApplyModeReconcile, nil)
	require.NoError(t, err)
	require.Empty(t, evaluated.Resources)
	require.Len(t, evaluated.Stacks, 1)
}

func TestDesiredUnresolvedReferenceNestedAndEmbedded(t *testing.T) {
	for _, location := range []string{"target-mapping", "target-list", "target-embed", "resource-embed", "resource-nested"} {
		t.Run(location, func(t *testing.T) {
			deps, pluginDir := fakeawsDeps(t)
			if location == "resource-embed" || location == "resource-nested" {
				fixture := filepath.Join(t.TempDir(), "fakeaws")
				require.NoError(t, os.CopyFS(fixture, os.DirFS(pluginDir)))
				project := filepath.Join(fixture, "PklProject")
				raw, err := os.ReadFile(project)
				require.NoError(t, err)
				core, err := filepath.Abs("schema/PklProject")
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(project, []byte(strings.ReplaceAll(string(raw), "../../../../schema/pkl/schema/PklProject", core)), 0600))
				secret := filepath.Join(fixture, "secretsmanager/secret.pkl")
				raw, err = os.ReadFile(secret)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(secret, []byte(strings.ReplaceAll(string(raw), "(String|formae.ValueSource)?", "(String|formae.ValueSource|formae.Embedded)?")), 0600))
				types := filepath.Join(fixture, "types.pkl")
				raw, err = os.ReadFile(types)
				require.NoError(t, err)
				content := strings.ReplaceAll(string(raw), "module fakeaws.types", "module fakeaws.types\nimport \"@formae/formae.pkl\"")
				content = strings.ReplaceAll(content, "hidden value: Any", "hidden value: (String|formae.ValueSource)")
				require.NoError(t, os.WriteFile(types, []byte(content), 0600))
				deps[1] = "local:fakeaws:" + project
			}
			ref := "formae://3J9W5cCOO9hsfDmzLXyLkJAAdVK#/Arn"
			envelope := map[string]any{"$ref": ref, "$visibility": "Opaque"}
			raw, err := json.Marshal(envelope)
			require.NoError(t, err)
			embed := map[string]any{"$embed": true, "$template": "before-" + model.FrameEnvelope(string(raw)) + "-after"}
			f := &model.Forma{Extraction: &model.ExtractionContext{CompleteStacks: []model.Stack{{Label: "stack"}}, Diagnostics: []model.ExtractionDiagnostic{{Code: "unresolved_desired_reference", Reference: ref}}}, Stacks: []model.Stack{{Label: "stack"}}, Targets: []model.Target{fakeawsTarget()}}
			switch location {
			case "target-mapping":
				f.Targets[0].Config, err = json.Marshal(map[string]any{"Auth": map[string]any{"ref": envelope, "keep": "sibling"}})
			case "target-list":
				f.Targets[0].Config, err = json.Marshal(map[string]any{"Auth": []any{envelope, "sibling"}})
			case "target-embed":
				f.Targets[0].Config, err = json.Marshal(map[string]any{"Auth": embed})
			case "resource-nested":
				props, e := json.Marshal(map[string]any{"Name": "kept", "Tags": []any{map[string]any{"Key": "nested", "Value": envelope}}})
				require.NoError(t, e)
				f.Resources = []model.Resource{{Stack: "stack", Target: "aws", Label: "consumer", Type: "FakeAWS::SecretsManager::Secret", Properties: props}}
			case "resource-embed":
				props, e := json.Marshal(map[string]any{"Name": "kept", "SecretString": embed})
				require.NoError(t, e)
				f.Resources = []model.Resource{{Stack: "stack", Target: "aws", Label: "consumer", Type: "FakeAWS::SecretsManager::Secret", Properties: props}}
			}
			require.NoError(t, err)
			path := filepath.Join(t.TempDir(), "desired.pkl")
			_, err = (PKL{}).GenerateSourceCode(f, path, nil, &schema.SerializeOptions{Schema: "pkl", SchemaLocation: schema.SchemaLocationLocal, Dependencies: deps})
			require.NoError(t, err)
			_, err = (PKL{}).Evaluate(path, model.CommandApply, model.FormaApplyModeReconcile, nil)
			require.ErrorContains(t, err, "Unresolved desired reference")
			source, err := os.ReadFile(path)
			require.NoError(t, err)
			repaired := regexp.MustCompile(`throw\("(?:[^"\\]|\\.)*"\)`).ReplaceAllString(string(source), `(new formae.Resolvable { label = "replacement"; stack = "owner"; type = "FakeAWS::SecretsManager::Secret"; property = "Arn" })`)
			require.NoError(t, os.WriteFile(path, []byte(repaired), 0600))
			evaluated, err := (PKL{}).Evaluate(path, model.CommandApply, model.FormaApplyModeReconcile, nil)
			require.NoError(t, err)
			var repairedValue any
			var rawResult json.RawMessage
			if strings.HasPrefix(location, "resource-") {
				rawResult = evaluated.Resources[0].Properties
			} else {
				rawResult = evaluated.Targets[0].Config
			}
			require.NoError(t, json.Unmarshal(rawResult, &repairedValue))
			references := 0
			var check func(any)
			check = func(v any) {
				switch n := v.(type) {
				case []any:
					for _, child := range n {
						check(child)
					}
				case map[string]any:
					if n["$res"] == true {
						references++
						require.Equal(t, "Opaque", n["$visibility"])
						require.Equal(t, "replacement", n["$label"])
					}
					if template, ok := n["$template"].(string); ok {
						spans, e := model.ScanEmbedSpans(template)
						require.NoError(t, e)
						for _, span := range spans {
							var envelope any
							require.NoError(t, json.Unmarshal([]byte(span.EnvelopeJSON), &envelope))
							check(envelope)
						}
					}
					for _, child := range n {
						check(child)
					}
				}
			}
			check(repairedValue)
			require.Equal(t, 1, references)
		})
	}
}

func TestDesiredUnresolvedReferenceRejectsUnsupportedAnnotations(t *testing.T) {
	ref := "formae://3J9W5cCOO9hsfDmzLXyLkJAAdVK#/Arn"
	for _, annotation := range []string{`"$transform":{"unknown":true}`, `"$strategy":"SetOnce"`, `"$json":42`, `"$visibility":"unknown"`} {
		f := &model.Forma{Extraction: &model.ExtractionContext{Diagnostics: []model.ExtractionDiagnostic{{Code: "unresolved_desired_reference", Reference: ref}}}, Resources: []model.Resource{{Properties: json.RawMessage(`{"Name":{"$ref":"` + ref + `",` + annotation + `}}`)}}}
		_, err := prepareDesiredExtraction(f)
		require.ErrorContains(t, err, "unsupported desired reference", annotation)
	}
}
