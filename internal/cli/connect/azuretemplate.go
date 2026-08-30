// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
)

// azureTemplateJSON is the ARM template that establishes the trust without
// formae ever holding a provisioning credential: a customer deploys it
// themselves (portal, their own az, or their own pipeline) and gives formae
// the clientId and tenantId outputs to register.
//
//go:embed assets/connect-azure.json
var azureTemplateJSON []byte

// azureTemplateCmd prints the embedded template, defaulted to the calling
// session's coordinates. It is the only way a customer who will not hand the
// CLI provisioning credentials gets the template out of it: the
// credential-less path has no URL to fetch it from, so the binary that
// already shipped it is the source.
//
// A session is required, not optional: installationId and formaeTenantId are
// properties of the formae installation the result gets registered against,
// not something a customer could otherwise know, and a template nobody can
// finish registering is not a usable template. Resolving one here is exactly
// what every other connect subcommand already does, so a bare machine gets
// the same hosted-profile failure it would from `connect azure`.
func azureTemplateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "template",
		Short:         "Print the ARM template that establishes trust without a provisioning credential",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cc *cobra.Command, args []string) error {
			return runAzureTemplate(cc)
		},
	}
	c.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	return c
}

func runAzureTemplate(cc *cobra.Command) error {
	opts, err := readSelection(cc)
	if err != nil {
		return err
	}
	s, err := openSession(cc.Context(), opts)
	if err != nil {
		return err
	}
	formaeTenantID, installationID, err := splitSubject(s.Setup.CloudSubject)
	if err != nil {
		return err
	}

	doc, err := azureTemplateWithDefaults(installationID, formaeTenantID)
	if err != nil {
		return err
	}
	if _, err := cc.OutOrStdout().Write(doc); err != nil {
		return err
	}

	// Guidance goes to stderr, never stdout: stdout is the template itself,
	// so `formae connect azure template > trust.json` must carry nothing
	// else.
	_, err = fmt.Fprintf(cc.ErrOrStderr(), "deploy this yourself, then register what it creates - installationId and "+
		"formaeTenantId are already filled in as defaults, so no --parameters are needed:\n\n"+
		"  az deployment sub create --location eastus --template-file trust.json\n\n"+
		"  formae connect azure --subscription <id> --tenant-id <t> --client-id <c>\n\n"+
		"<t> and <c> come from the deployment's outputs (tenantId and clientId).\n")
	return err
}

// azureTemplateWithDefaults returns the embedded template with
// installationId and formaeTenantId defaulted to the resolved session
// values. Neither is something a customer could otherwise supply - both are
// properties of the formae installation the deployment's outputs get
// registered against - so leaving them as bare required parameters hands
// out a template nobody can deploy without asking formae what to pass.
//
// Nothing else about the template is touched: it is decoded and re-encoded
// as a generic document, so every other parameter, resource, and the nested
// deployment's expressionEvaluationOptions (load-bearing: without inner
// scope its resourceId() calls resolve at subscription scope and the
// deployment fails) survive unchanged.
func azureTemplateWithDefaults(installationID, formaeTenantID string) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(azureTemplateJSON, &doc); err != nil {
		return nil, fmt.Errorf("connect-azure.json does not parse as JSON: %w", err)
	}
	params, ok := doc["parameters"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("connect-azure.json has no parameters object")
	}
	if err := setParameterDefault(params, "installationId", installationID); err != nil {
		return nil, err
	}
	if err := setParameterDefault(params, "formaeTenantId", formaeTenantID); err != nil {
		return nil, err
	}
	return json.MarshalIndent(doc, "", "  ")
}

// setParameterDefault fills one ARM template parameter's defaultValue.
func setParameterDefault(params map[string]any, name, value string) error {
	p, ok := params[name].(map[string]any)
	if !ok {
		return fmt.Errorf("connect-azure.json has no %q parameter", name)
	}
	p["defaultValue"] = value
	return nil
}
