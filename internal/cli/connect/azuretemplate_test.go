// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The published ARM template is the credential-less path: a customer who
// will not hand a CLI provisioning credentials deploys it themselves and
// gives formae the two outputs. Its shape is pinned here because a change
// that silently breaks the nested deployment's scope, or drops an output,
// fails only when someone tries to deploy it.
func TestConnectTemplateShape(t *testing.T) {
	var doc map[string]any
	require.NoError(t, json.Unmarshal(azureTemplateJSON, &doc), "the template must parse as JSON")

	schema, _ := doc["$schema"].(string)
	assert.Contains(t, schema, "subscriptionDeploymentTemplate.json",
		"the template must target the subscription-scoped deployment schema")

	resources, ok := doc["resources"].([]any)
	require.True(t, ok, "the template must declare a resources array")

	var nested map[string]any
	for _, r := range resources {
		res, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if res["type"] == "Microsoft.Resources/deployments" {
			nested = res
			break
		}
	}
	require.NotNil(t, nested, "the template must nest nest a deployment for the identity resources")

	props, ok := nested["properties"].(map[string]any)
	require.True(t, ok, "the nested deployment must carry a properties object")
	evalOpts, ok := props["expressionEvaluationOptions"].(map[string]any)
	require.True(t, ok, "the nested deployment must set expressionEvaluationOptions; "+
		"without inner scope, resourceId() resolves at subscription scope and the deployment fails")
	assert.Equal(t, "inner", evalOpts["scope"],
		"the nested deployment's resourceId() calls must resolve inside its own resource group")

	outputs, ok := doc["outputs"].(map[string]any)
	require.True(t, ok, "the template must declare top-level outputs")
	assert.Contains(t, outputs, "clientId", "the template must output the managed identity's client id")
	assert.Contains(t, outputs, "tenantId", "the template must output the subscription's tenant id")
}

// `connect azure template` is how a customer who will not hand the CLI
// provisioning credentials gets the template out of it: the credential-less
// path has nothing to fetch a URL from, so the binary that already shipped
// the template is the only source for it.
func TestAzureTemplateCommandPrintsTheEmbeddedTemplate(t *testing.T) {
	c := ConnectCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"azure", "template"})

	require.NoError(t, c.Execute())
	assert.Equal(t, azureTemplateJSON, out.Bytes())
}
