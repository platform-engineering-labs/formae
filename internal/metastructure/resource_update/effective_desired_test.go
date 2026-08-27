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

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// The effective desired document for an existing resource is the forma
// declaration after SetOnce filtering: a populated SetOnce property keeps its
// persisted value no matter what the forma resubmits. Resources with no
// persisted row (creates) get no entry.
func TestComputeEffectiveDesired_SetOncePreservedCreatesAbsent(t *testing.T) {
	existing := &pkgmodel.Resource{
		Label: "parent",
		Ksuid: "ksuid-parent",
		Stack: "test-stack",
		Properties: json.RawMessage(`{
			"Name": "parent-1",
			"Value": {"$value": "hello", "$strategy": "SetOnce"}
		}`),
	}
	forma := &pkgmodel.Forma{
		Resources: []pkgmodel.Resource{
			{
				Label: "parent",
				Ksuid: "ksuid-parent",
				Stack: "test-stack",
				Properties: json.RawMessage(`{
					"Name": "parent-1",
					"Value": {"$value": "world-resubmitted", "$strategy": "SetOnce"}
				}`),
			},
			{
				Label:      "brand-new",
				Ksuid:      "ksuid-new",
				Stack:      "test-stack",
				Properties: json.RawMessage(`{"Name": "n"}`),
			},
		},
	}
	all := map[string][]*pkgmodel.Resource{"test-stack": {existing}}

	eff, err := ComputeEffectiveDesired(forma, all)
	require.NoError(t, err)

	filtered, ok := eff["ksuid-parent"]
	require.True(t, ok, "an existing declared resource must have an effective desired entry")
	assert.JSONEq(t, `{
		"Name": "parent-1",
		"Value": {"$value": "hello", "$strategy": "SetOnce"}
	}`, string(filtered), "a populated SetOnce property keeps its persisted value")

	_, ok = eff["ksuid-new"]
	assert.False(t, ok, "a resource with no persisted row has no entry")
}
