// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func effectiveDesiredTokenSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Fields: []string{"Name", "Token"},
		Hints:  map[string]pkgmodel.FieldHint{"Token": {WriteOnly: true, CreateOnly: true}},
	}
}

// A declared writeOnly+createOnly value with no stored baseline is not part of
// the state this command drives the producer to: the producer's own patch
// generation strips it, so the effective desired document must strip it too,
// or reference consumers would be planned against a value that is never
// written.
func TestComputeEffectiveDesired_StripsDeclaredWriteOnlyCreateOnlyWithoutBaseline(t *testing.T) {
	schema := effectiveDesiredTokenSchema()
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Label: "s", Ksuid: "2sourceghijklmnopqrstuvwxyz", Type: "Test::Source", Schema: schema,
		Properties: json.RawMessage(`{"Name":"s","Token":"t"}`),
	}}}
	persisted := map[string][]*pkgmodel.Resource{"default": {{
		Label: "s", Ksuid: "2sourceghijklmnopqrstuvwxyz", Type: "Test::Source", Schema: schema,
		Properties: json.RawMessage(`{"Name":"s"}`),
	}}}

	eff, err := ComputeEffectiveDesired(forma, persisted)
	require.NoError(t, err)
	assert.False(t, gjson.GetBytes(eff["2sourceghijklmnopqrstuvwxyz"], "Token").Exists(),
		"a declared writeOnly+createOnly value with no stored baseline is not part of the state this command drives to")
	assert.Equal(t, "s", gjson.GetBytes(eff["2sourceghijklmnopqrstuvwxyz"], "Name").String())
}

// With a stored baseline the declared value is a genuine immutable change and
// must stay visible to the lookup, so the coordinated source and consumer
// replacement still plans.
func TestComputeEffectiveDesired_KeepsWriteOnlyCreateOnlyWithBaseline(t *testing.T) {
	schema := effectiveDesiredTokenSchema()
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Label: "s", Ksuid: "2sourceghijklmnopqrstuvwxyz", Type: "Test::Source", Schema: schema,
		Properties: json.RawMessage(`{"Name":"s","Token":"t"}`),
	}}}
	persisted := map[string][]*pkgmodel.Resource{"default": {{
		Label: "s", Ksuid: "2sourceghijklmnopqrstuvwxyz", Type: "Test::Source", Schema: schema,
		Properties: json.RawMessage(`{"Name":"s","Token":"t-old"}`),
	}}}

	eff, err := ComputeEffectiveDesired(forma, persisted)
	require.NoError(t, err)
	assert.Equal(t, "t", gjson.GetBytes(eff["2sourceghijklmnopqrstuvwxyz"], "Token").String())
}

// An import-shaped source (writeOnly+createOnly field stored without a
// baseline) declared with a value must not drive a consumer whose createOnly
// field references it into a replacement: the source's own patch strips the
// undeliverable value and the effective desired document strips it
// identically, so the consumer is not planned against a value that is never
// written and nothing is destroyed.
func TestGenerateResourceUpdates_ImportShapedWriteOnlySource_ConsumerIsNotReplaced(t *testing.T) {
	ds, _ := GetDeps(t)

	sourceSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Token"},
		Hints:      map[string]pkgmodel.FieldHint{"Token": {WriteOnly: true, CreateOnly: true}, "Name": {CreateOnly: true}},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "SourceToken"},
		Hints:      map[string]pkgmodel.FieldHint{"SourceToken": {CreateOnly: true}, "Name": {CreateOnly: true}},
	}

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "s", Type: "Test::Source", Stack: "test-stack", Target: "test-target",
				Schema: sourceSchema, Ksuid: util.NewID(),
				Properties: json.RawMessage(`{"Name":"s"}`),
			},
			{
				Label: "c", Type: "Test::Consumer", Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema, Ksuid: util.NewID(),
				Properties: json.RawMessage(`{"Name":"c","SourceToken":"old"}`),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks:  []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test", Config: json.RawMessage(`{}`)}},
		Resources: []pkgmodel.Resource{
			{
				Label: "s", Type: "Test::Source", Stack: "test-stack", Target: "test-target",
				Schema:     sourceSchema,
				Properties: json.RawMessage(`{"Name":"s","Token":"t"}`),
			},
			{
				Label: "c", Type: "Test::Consumer", Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema,
				Properties: json.RawMessage(`{
					"Name": "c",
					"SourceToken": {
						"$res": true,
						"$label": "s",
						"$type": "Test::Source",
						"$stack": "test-stack",
						"$property": "Token"
					}
				}`),
			},
		},
	}
	existingTargets := []*pkgmodel.Target{{Label: "test-target", Namespace: "test", Config: json.RawMessage(`{}`)}}

	updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser, existingTargets, ds, nil, nil)
	require.NoError(t, err)

	for _, u := range updates {
		label := u.DesiredState.Label
		if label == "" {
			label = u.PriorState.Label
		}
		assert.NotEqual(t, OperationDelete, u.Operation,
			"nothing may be planned for deletion over a value the source never writes (got delete for %q)", label)
	}
}
