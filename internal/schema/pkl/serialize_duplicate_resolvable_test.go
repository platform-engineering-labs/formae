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

func TestSerializeForma_UnnarrowedVersionedPackage_ToleratesDuplicateResolvable(t *testing.T) {
	_, pklProject := installFakeVerPlugin(t)

	formaeProject, err := filepath.Abs(filepath.Join("schema", "PklProject"))
	require.NoError(t, err)

	forma := &model.Forma{
		Stacks: []model.Stack{{Label: "default"}},
		Targets: []model.Target{{
			Label:     "fv",
			Namespace: "FakeVer",
			Config:    json.RawMessage(`{"ApiVersion":"v1.1"}`),
		}},
		Resources: []model.Resource{
			{
				Label: "my-widget", Type: "FakeVer::Core::Widget",
				Stack: "default", Target: "fv", NativeID: "w-1",
				Properties: json.RawMessage(`{"Name":"my-widget","Replicas":2}`),
			},
			{
				Label: "my-gadget", Type: "FakeVer::Core::Gadget",
				Stack: "default", Target: "fv", NativeID: "g-1",
				Properties: json.RawMessage(`{"Name":"my-gadget","WidgetName":{"$res":true,"$label":"my-widget","$type":"FakeVer::Core::Widget","$stack":"default","$property":"Name","$value":"my-widget"}}`),
			},
		},
	}

	out, err := PKL{}.SerializeForma(forma, &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationRemote,
		Dependencies: []string{
			"local:formae:" + formaeProject,
			"local:fakever:" + pklProject,
		},
	})
	require.NoError(t, err,
		"a versioned package imported whole declares one entry-point resolvable "+
			"per version; before the fix this failed with "+
			"`Duplicate Resolvable Type URI: FakeVer::Core::Widget`")

	t.Logf("generated forma:\n%s", out)

	assert.Contains(t, out, "my-widget")
	assert.Contains(t, out, "my-gadget")
	assert.Contains(t, out, ".res",
		"the reference has to survive as a resolvable, not collapse to its $value")
}
