// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
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
	require.NotNil(t, nested, "the template must nest a deployment for the identity resources")

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

	// The source asset carries no defaults for these two: they are properties
	// of the caller's formae installation, not something baked into a
	// binary shipped to everyone. `connect azure template` is what fills
	// them in, from the session, at emit time.
	params, ok := doc["parameters"].(map[string]any)
	require.True(t, ok, "the template must declare a parameters object")
	installationParam, ok := params["installationId"].(map[string]any)
	require.True(t, ok, "the template must declare an installationId parameter")
	_, hasDefault := installationParam["defaultValue"]
	assert.False(t, hasDefault, "installationId must have no default in the source asset")
	tenantParam, ok := params["formaeTenantId"].(map[string]any)
	require.True(t, ok, "the template must declare a formaeTenantId parameter")
	_, hasDefault = tenantParam["defaultValue"]
	assert.False(t, hasDefault, "formaeTenantId must have no default in the source asset")
}

// `connect azure template` is how a customer who will not hand the CLI
// provisioning credentials gets the template out of it. A session is
// required even on this path: installationId and formaeTenantId are
// properties of the installation the deployment gets registered against,
// not something a customer could otherwise supply, so the command resolves
// one and fills both in as defaults - the same way every other connect
// subcommand resolves its session.
func TestAzureTemplateCommandEmitsTheTemplateWithDefaults(t *testing.T) {
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	stdout, stderr, err := runConnectSplit(t, "azure", "template")
	require.NoError(t, err, "stderr: %s", stderr)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &doc), "stdout must be valid JSON and nothing else")

	params, ok := doc["parameters"].(map[string]any)
	require.True(t, ok, "the emitted template must declare a parameters object")
	installationParam, ok := params["installationId"].(map[string]any)
	require.True(t, ok, "the emitted template must declare an installationId parameter")
	assert.Equal(t, contractInstallation, installationParam["defaultValue"],
		"installationId must default to the resolved session's installation")
	tenantParam, ok := params["formaeTenantId"].(map[string]any)
	require.True(t, ok, "the emitted template must declare a formaeTenantId parameter")
	assert.Equal(t, "acme", tenantParam["defaultValue"],
		"formaeTenantId must default to the resolved session's formae tenant")

	assert.Contains(t, stderr, "az deployment sub create", "the deploy command must be printed")
	assert.Contains(t, stderr, "formae connect azure --subscription",
		"the register follow-up command must be printed")
	assert.Contains(t, stderr, "outputs", "the guidance must say the ids come from the deployment's outputs")
	assert.NotContains(t, stdout, "az deployment sub create",
		"guidance must never land on stdout, which a caller redirects straight to a file")
}

// A bare machine with no profile at all gets the same hosted-profile failure
// every other connect subcommand reports: a template nobody can register
// against an installation is not a usable template, so this path requires a
// session exactly as strictly as the others do.
func TestAzureTemplateCommandRequiresASession(t *testing.T) {
	t.Setenv("FORMAE_CONFIG_DIR", t.TempDir())

	_, _, err := runConnectSplit(t, "azure", "template")

	require.Error(t, err)
	var f *printer.Failure
	require.ErrorAs(t, err, &f)
	assert.Equal(t, printer.CodeHostedRequired, f.Code)
}
