// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cloudapi"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// azureOptions is everything `connect azure` decides from, read once off the
// flags.
type azureOptions struct {
	Subscription string
	Location     string
	// LocationSet and ResourceGroupSet come from cobra's Changed, not from
	// whether the resolved value is non-empty: mode determination has to run
	// before either default is applied, or a defaulted --resource-group
	// would make every register-only invocation look like it carried a
	// provisioning flag.
	LocationSet      bool
	ResourceGroup    string
	ResourceGroupSet bool
	// TenantID is dual-purpose: alone, it is an authentication hint forwarded
	// into provisioning (an external or guest account can need it to
	// authenticate at all); alongside ClientID, it is half of the register-only
	// coordinate.
	TenantID string
	ClientID string
	NoInput  bool

	// ConfigFlag and ProfileFlag are the shared --config/--profile selection.
	ConfigFlag  string
	ProfileFlag string
}

// azureMode is the path a `connect azure` run takes.
type azureMode int

const (
	azureModeLocal azureMode = iota
	azureModeRegisterOnly
)

const (
	defaultAzureLocation      = "eastus"
	defaultAzureResourceGroup = "formae-ai"
)

var azureUUIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validateAzureUUID(value, flag string) error {
	if !azureUUIDRE.MatchString(value) {
		return clicmd.FlagErrorf("%s must be a UUID", flag)
	}
	return nil
}

// decideAzureMode validates the flag set and picks the path.
//
// --client-id is the register-only signal: it names an existing managed
// identity, which - unlike a tenant - can never be a mere hint. --tenant-id
// given alone does not select register-only; it is accepted as a
// provisioning-mode authentication hint instead, so it fails only when it
// travels without the client id it is meant to accompany.
func decideAzureMode(opts azureOptions) (azureMode, error) {
	if opts.Subscription == "" {
		return 0, clicmd.FlagErrorf("--subscription is required; it is the account being registered")
	}

	if opts.ClientID == "" {
		// --tenant-id alone is a provisioning-mode authentication hint, not a
		// coordinate; it still has to be a UUID, because it travels verbatim
		// into provx's New(), whose own contract is that an empty value means
		// derive - not that any string is acceptable.
		if opts.TenantID != "" {
			if err := validateAzureUUID(opts.TenantID, "--tenant-id"); err != nil {
				return 0, err
			}
		}
		return azureModeLocal, nil
	}

	if opts.TenantID == "" {
		return 0, clicmd.FlagErrorf("--client-id names an existing managed identity; pass --tenant-id too, " +
			"so both coordinates the control plane needs are registered")
	}
	if err := validateAzureUUID(opts.TenantID, "--tenant-id"); err != nil {
		return 0, err
	}
	if err := validateAzureUUID(opts.ClientID, "--client-id"); err != nil {
		return 0, err
	}
	if opts.LocationSet {
		return 0, clicmd.FlagErrorf("--location applies only when formae provisions the trust; " +
			"it does not apply in register-only mode")
	}
	if opts.ResourceGroupSet {
		return 0, clicmd.FlagErrorf("--resource-group applies only when formae provisions the trust; " +
			"it does not apply in register-only mode")
	}
	return azureModeRegisterOnly, nil
}

// applyAzureLocalDefaults fills the provisioning-only defaults. Called only
// after mode determination: nothing this path touches is regional in a way
// that matters (a managed identity holds no customer data and federates
// globally, so its region only places a metadata record), so a region is
// defaulted rather than demanded, the same reasoning connect aws's
// defaultRegion documents.
func applyAzureLocalDefaults(opts azureOptions) azureOptions {
	if opts.Location == "" {
		opts.Location = defaultAzureLocation
	}
	if opts.ResourceGroup == "" {
		opts.ResourceGroup = defaultAzureResourceGroup
	}
	return opts
}

// azureSovereignCloudEnvVar names the environment variable naming the active
// Azure cloud, the convention az CLI's automation profile and Terraform's
// azurerm provider both honour. Empty, or the public cloud's own name, means
// public.
const azureSovereignCloudEnvVar = "AZURE_ENVIRONMENT"

// refuseAzureSovereignCloud refuses explicitly rather than failing somewhere
// further in: the issuer, authority and ARM endpoints connect pins are
// public-cloud specific, and a subscription id does not identify its cloud.
//
// This is a heuristic, not an exhaustive detection: it trusts the operator's
// own environment to say which cloud it targets, the same convention az CLI
// and Terraform's azurerm provider rely on, rather than trying to infer the
// cloud from credentials or network reachability. An operator who has set
// AZURE_ENVIRONMENT correctly for their own tools gets the same answer here;
// one who has not configured it at all is assumed public, which is correct
// for the overwhelming majority of subscriptions.
func refuseAzureSovereignCloud() error {
	env := strings.TrimSpace(os.Getenv(azureSovereignCloudEnvVar))
	if env == "" || strings.EqualFold(env, "AzureCloud") || strings.EqualFold(env, "AzurePublicCloud") {
		return nil
	}
	return printer.Fail(printer.CodeUnsupportedPartition, fmt.Sprintf(
		"formae connect azure supports only the Azure public cloud; %s names %q", azureSovereignCloudEnvVar, env), nil)
}

// permissionsProvisionedAzure names what provisioning grants, in the design's
// own terms: near-owner, because a principal that can edit resources and
// assign roles can grant itself more.
const permissionsProvisionedAzure = "Contributor + User Access Administrator on the subscription — near-owner, " +
	"and the same permissions a self-hosted formae agent runs with"

// unverifiedAzureCoordinateWarning states exactly what register-only did not
// check, so nobody reads "registered" as "working". Azure-specific wording:
// it names a managed identity, not a provider, and there is no separate
// existence check to describe.
const unverifiedAzureCoordinateWarning = "the coordinates were validated for shape only: formae did not check that " +
	"this managed identity exists, that it trusts the formae issuer, or that it grants this installation access. " +
	"The first use is where a wrong one shows up"

func azureCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "azure",
		Short:         "Connect an Azure subscription",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cc *cobra.Command, args []string) error {
			opts, err := readAzureOptions(cc)
			if err != nil {
				return err
			}
			return runConnectAzure(cc, opts)
		},
	}
	c.Flags().String("subscription", "", "Azure subscription id to connect (always explicit, never inferred from ambient credentials)")
	c.Flags().String("location", "", "Azure region for the managed identity's metadata record (default: eastus); provisioning-only")
	c.Flags().String("resource-group", "", "Resource group the connection resources live in (default: formae-ai); provisioning-only")
	c.Flags().String("tenant-id", "",
		"The subscription's Entra tenant: an authentication hint when provisioning, or half the coordinate when trust already exists")
	c.Flags().String("client-id", "",
		"Trust already exists (a deployed connect-azure.json template, or an identity you made yourself): validate and register only")
	c.Flags().Bool("no-input", false, "Disable prompts this command renders")
	clicmd.AddOutputFlags(c)
	c.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	c.AddCommand(azureTemplateCmd())
	return c
}

func readAzureOptions(cc *cobra.Command) (azureOptions, error) {
	sel, err := readSelection(cc)
	if err != nil {
		return azureOptions{}, err
	}
	opts := azureOptions{ConfigFlag: sel.ConfigFlag, ProfileFlag: sel.ProfileFlag}
	opts.Subscription, _ = cc.Flags().GetString("subscription")
	opts.Location, _ = cc.Flags().GetString("location")
	opts.LocationSet = cc.Flags().Changed("location")
	opts.ResourceGroup, _ = cc.Flags().GetString("resource-group")
	opts.ResourceGroupSet = cc.Flags().Changed("resource-group")
	opts.TenantID, _ = cc.Flags().GetString("tenant-id")
	opts.ClientID, _ = cc.Flags().GetString("client-id")
	opts.NoInput, _ = cc.Flags().GetBool("no-input")
	return opts, nil
}

// runConnectAzure validates the flag set, picks the path, and runs it.
func runConnectAzure(cc *cobra.Command, opts azureOptions) error {
	consumer, schema, err := resolveOutputOrHuman(cc)
	if err != nil {
		return err
	}

	mode, err := decideAzureMode(opts)
	if err != nil {
		var fe *clicmd.FlagError
		if errors.As(err, &fe) {
			return err
		}
		return report(cc.OutOrStdout(), consumer, schema, err, azureFallbackMessage)
	}
	if mode == azureModeLocal {
		opts = applyAzureLocalDefaults(opts)
	}

	if err := runAzureMode(cc, mode, opts, consumer, schema); err != nil {
		return report(cc.OutOrStdout(), consumer, schema, err, azureFallbackMessage)
	}
	return nil
}

const azureFallbackMessage = "formae could not connect the subscription; run it without --output-consumer machine to see why"

func runAzureMode(cc *cobra.Command, mode azureMode, opts azureOptions, consumer printer.Consumer, schema string) error {
	switch mode {
	case azureModeRegisterOnly:
		return runAzureRegisterOnly(cc, opts, consumer, schema)
	case azureModeLocal:
		return runAzureLocal(cc, opts, consumer, schema)
	default:
		return errors.New("this connect path is not implemented yet")
	}
}

// azureInteractiveRun mirrors interactiveRun: a TTY, no --no-input, and a
// human consumer.
func azureInteractiveRun(opts azureOptions, consumer printer.Consumer) bool {
	return !opts.NoInput && consumer != printer.ConsumerMachine && isInteractive()
}

// runAzureRegisterOnly is the --tenant-id/--client-id path: trust already
// exists (a deployed connect-azure.json template, or an identity made by
// hand), so this holds no Azure credentials and validates the coordinate's
// shape and nothing else, saying so in its output rather than implying the
// connection was checked.
func runAzureRegisterOnly(cc *cobra.Command, opts azureOptions, consumer printer.Consumer, schema string) error {
	s, err := openSession(cc.Context(), options{ConfigFlag: opts.ConfigFlag, ProfileFlag: opts.ProfileFlag})
	if err != nil {
		return err
	}

	warnings := append([]string{}, s.Warnings...)
	elsewhere := connectedElsewhere(s.Setup.AccountsConnectedHint, "azure", opts.Subscription, s.InstallationID)
	if len(elsewhere) > 0 {
		warnings = append(warnings, multiInstallationWarning(opts.Subscription, elsewhere))
	}
	warnings = append(warnings, unverifiedAzureCoordinateWarning)

	if azureInteractiveRun(opts, consumer) {
		th := clicmd.ResolveConfiguredTheme(cc)
		if err := confirmInteractive(th, "azure", "subscription", opts.Subscription, s.Setup.CloudSubject,
			permissionsAsApplied, elsewhere); err != nil {
			return err
		}
	}

	status, err := s.registerConnection(cc.Context(), cloudapi.CloudConnectionRegistration{
		Cloud:         "azure",
		Account:       opts.Subscription,
		AzureTenantID: opts.TenantID,
		AzureClientID: opts.ClientID,
	})
	if err != nil {
		return err
	}

	return emitAzureRegistered(cc, consumer, schema, status, opts.Subscription, opts.TenantID, opts.ClientID, warnings, s.InstallationID)
}

// runAzureLocal is the default path: formae obtains credentials, provisions
// the trust, and registers what it created.
func runAzureLocal(cc *cobra.Command, opts azureOptions, consumer printer.Consumer, schema string) error {
	ctx := cc.Context()

	if err := refuseAzureSovereignCloud(); err != nil {
		return err
	}

	state, err := usableCredentials(ctx, opts.Subscription, opts.TenantID)
	if err != nil {
		return err
	}
	if state != azureCredentialsUsable {
		return azureCredentialFailure(state, opts.TenantID)
	}

	s, err := openSession(ctx, options{ConfigFlag: opts.ConfigFlag, ProfileFlag: opts.ProfileFlag})
	if err != nil {
		return err
	}

	warnings := append([]string{}, s.Warnings...)
	elsewhere := connectedElsewhere(s.Setup.AccountsConnectedHint, "azure", opts.Subscription, s.InstallationID)
	if len(elsewhere) > 0 {
		warnings = append(warnings, multiInstallationWarning(opts.Subscription, elsewhere))
		warnings = append(warnings, sharedTrustDomainWarning)
	}

	if azureInteractiveRun(opts, consumer) {
		th := clicmd.ResolveConfiguredTheme(cc)
		if err := confirmInteractive(th, "azure", "subscription", opts.Subscription, s.Setup.CloudSubject,
			permissionsProvisionedAzure, elsewhere); err != nil {
			return err
		}
	}

	formaeTenantID, installationID, err := splitSubject(s.Setup.CloudSubject)
	if err != nil {
		return err
	}

	result, err := provisionAzure(ctx, opts.Subscription, opts.TenantID, formaeTenantID, installationID, opts.ResourceGroup, opts.Location)
	if err != nil {
		return err
	}

	status, err := s.registerConnection(ctx, cloudapi.CloudConnectionRegistration{
		Cloud:         "azure",
		Account:       opts.Subscription,
		AzureTenantID: result.TenantID,
		AzureClientID: result.ClientID,
	})
	if err != nil {
		// Provisioning succeeded and registration did not, so the subscription
		// now grants access to an installation the control plane does not know
		// about. There is no rollback, and what survives holds near-owner: said
		// plainly rather than left to be discovered, and as a dedicated
		// printer.Fail rather than a wrap - wrapping put whatever
		// registerConnection returned in front of errors.As, so a machine
		// consumer either saw that inner failure's own code (burying this
		// message entirely) or, when the inner error carried no *Failure at
		// all, a generic "internal" that named none of the surviving
		// coordinates. Re-running converges.
		return printer.Fail(printer.CodeOrphanedTrust,
			fmt.Sprintf("the subscription now grants access to an installation the control plane does not know "+
				"about; there is no rollback, and this identity holds near-owner access until it is registered. "+
				"Re-run this command to finish: %v", err),
			map[string]any{
				"resourceGroup": opts.ResourceGroup,
				"identity":      result.IdentityID,
				"clientId":      result.ClientID,
			})
	}

	return emitAzureRegistered(cc, consumer, schema, status, opts.Subscription, result.TenantID, result.ClientID, warnings, s.InstallationID)
}

func emitAzureRegistered(cc *cobra.Command, consumer printer.Consumer, schema, status, subscription, tenantID, clientID string,
	warnings []string, installationID string) error {
	v := azureRegisteredDocument(status, subscription, tenantID, clientID, warnings)
	if consumer == printer.ConsumerMachine {
		return emitRegistered(cc.OutOrStdout(), schema, v)
	}
	return printRegisteredHuman(cc.OutOrStdout(), isInteractive(), clicmd.ResolveConfiguredTheme(cc), v, installationID)
}
