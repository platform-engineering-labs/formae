// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
//go:build unit

package dstest

import (
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// Inject a JSON-valid but unsupported/malformed declaration into the actual
// stored generator row through its marshaler. All four persistence paths are real.
type storedInvalidGenerator struct {
	pkgmodel.PasswordGenerator
	raw json.RawMessage
}

func (g *storedInvalidGenerator) MarshalJSON() ([]byte, error) { return g.raw, nil }

func RunDesiredMetadataCompleteness(t *testing.T, newDS func(*testing.T) TestDatastore) {
	for _, kind := range []string{"unsupported-policy", "malformed-policy", "unsupported-generator", "malformed-generator"} {
		t.Run("DesiredMetadataCompleteness/"+kind, func(t *testing.T) {
			td := newDS(t)
			defer func(cleanup func() error) { _ = cleanup() }(td.CleanUpFn)
			_, err := td.CreateStack(&pkgmodel.Stack{Label: "complete"}, "seed")
			require.NoError(t, err)
			valid, err := (&metastructure.Metastructure{Datastore: td.Datastore}).ExtractDesiredStacks("stack:complete")
			require.NoError(t, err)
			require.NotNil(t, valid)
			_, err = td.CreateTarget(&pkgmodel.Target{Label: "exact-target", Namespace: "Test", Config: json.RawMessage(`{"number":9007199254740993,"ref":{"$ref":"formae://source#/value","$visibility":"Opaque","$value":"must-not-persist"}}`)})
			require.NoError(t, err)
			target, err := td.LoadTarget("exact-target")
			require.NoError(t, err)
			require.Contains(t, string(target.Config), "9007199254740993")
			require.NotContains(t, string(target.Config), "must-not-persist")
			stack, err := td.GetStackByLabel("complete")
			require.NoError(t, err)
			if kind == "unsupported-policy" || kind == "malformed-policy" {
				p := &pkgmodel.TTLPolicy{Type: "ttl", Label: "important", StackID: stack.ID, TTLSeconds: 3600}
				if kind == "unsupported-policy" {
					require.NotNil(t, td.SetPolicyTypeForTest)
				}
				_, err = td.CreatePolicy(p, "seed")
				require.NoError(t, err)
				if kind == "unsupported-policy" {
					require.NoError(t, td.SetPolicyTypeForTest(p.Label, "future-policy"))
				}
				if kind == "malformed-policy" {
					require.NotNil(t, td.SetPolicyDataForTest)
					require.NoError(t, td.SetPolicyDataForTest(p.Label, `{"TTLSeconds":"invalid"}`))
				}
			} else {
				raw := `{"Type":"future-generator","Label":"unused","Stack":"complete"}`
				if kind == "malformed-generator" {
					raw = `{"Type":"password","Label":"unused","Stack":"complete","Length":"invalid"}`
				}
				_, err = td.CreateGenerator(&storedInvalidGenerator{PasswordGenerator: pkgmodel.PasswordGenerator{Label: "unused", Stack: "complete", StackID: stack.ID}, raw: json.RawMessage(raw)}, "seed")
				require.NoError(t, err)
			}
			result, err := (&metastructure.Metastructure{Datastore: td.Datastore}).ExtractDesiredStacks("stack:complete")
			require.ErrorContains(t, err, "invalid desired metadata", "a complete scope cannot silently omit stored metadata")
			require.Nil(t, result)
		})
	}
}
