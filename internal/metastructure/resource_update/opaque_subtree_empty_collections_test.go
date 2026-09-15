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

// Empty members of a custom resource spec can select provider behavior:
// spec.selfSigned = {} is a complete cert-manager issuer declaration. The
// preserveEmptyValues hint protects that subtree in plugin-bound payloads,
// independently of the Atomic update method used by the shipped K8s schema.
// Schema.Fields alone does not tell the converter whether empties are meaningful.
func TestConvertResourceForPlugin_CustomResourcePreservesHintedEmptyMap(t *testing.T) {
	res := pkgmodel.Resource{
		Label: "selfsigned",
		Type:  "K8S::Custom::Resource",
		Schema: pkgmodel.Schema{
			Identifier: "$.formaeId",
			Fields: []string{
				"apiVersion", "kind", "metadata", "spec", "formaeId",
				"metadata.name", "metadata.namespace", "metadata.labels", "metadata.annotations",
			},
			Hints: map[string]pkgmodel.FieldHint{
				"apiVersion": {Required: true},
				"kind":       {Required: true},
				"metadata":   {Required: true},
				"spec":       {PreserveEmptyValues: true},
			},
		},
		Properties: json.RawMessage(`{"apiVersion":"cert-manager.io/v1","kind":"ClusterIssuer",` +
			`"metadata":{"name":"selfsigned"},"spec":{"selfSigned":{}},` +
			`"formaeId":"cert-manager.io/v1/ClusterIssuer//selfsigned"}`),
	}

	converted, err := convertResourceForPlugin(res)
	require.NoError(t, err)
	assert.JSONEq(t, `{"apiVersion":"cert-manager.io/v1","kind":"ClusterIssuer",
		"metadata":{"name":"selfsigned"},"spec":{"selfSigned":{}},
		"formaeId":"cert-manager.io/v1/ClusterIssuer//selfsigned"}`, string(converted.Properties),
		"the plugin must receive the empty issuer object and the surrounding resource intact")
}

// Unhinted fields retain cleanup of empty collections. Preserve the nonempty
// container and metadata while removing empty probe and node-selector objects.

func TestConvertResourceForPlugin_PodStripsUnhintedEmptyCollections(t *testing.T) {
	res := pkgmodel.Resource{
		Label: "pod",
		Type:  "K8S::Core::Pod",
		Schema: pkgmodel.Schema{
			Fields: []string{
				"metadata", "metadata.name",
				"spec", "spec.containers", "spec.containers.name",
				"spec.containers.livenessProbe", "spec.nodeSelector",
			},
			Hints: map[string]pkgmodel.FieldHint{"metadata": {Required: true}, "spec": {}},
		},
		Properties: json.RawMessage(`{"metadata":{"name":"p"},` +
			`"spec":{"containers":[{"name":"app","livenessProbe":{}}],"nodeSelector":{}}}`),
	}

	converted, err := convertResourceForPlugin(res)
	require.NoError(t, err)
	assert.JSONEq(t, `{"metadata":{"name":"p"},"spec":{"containers":[{"name":"app"}]}}`,
		string(converted.Properties), "cleanup must preserve the rest of the Pod")
}
