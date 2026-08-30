// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cloudapi"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// listFallbackMessage is the internal-code text an undeclared error gets on
// the list path.
const listFallbackMessage = "formae could not list the cloud connections; run it without --output-consumer machine to see why"

func listCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "list",
		Short:         "List the cloud accounts registered on this installation",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cc *cobra.Command, args []string) error {
			opts, err := readSelection(cc)
			if err != nil {
				return err
			}
			return runConnectList(cc, opts)
		},
	}
	clicmd.AddOutputFlags(c)
	c.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	return c
}

// runConnectList reads the connections registered on the active installation
// and reports them. It is member-readable: it opens the control plane the way
// any read does, through openControlPlane, and never calls openSession or the
// admin-gated setup endpoint that only provisioning needs.
func runConnectList(cc *cobra.Command, opts options) error {
	consumer, schema, err := resolveOutputOrHuman(cc)
	if err != nil {
		return err
	}

	v, err := readConnections(cc, opts)
	if err != nil {
		return report(cc.OutOrStdout(), consumer, schema, err, listFallbackMessage)
	}

	if consumer == printer.ConsumerMachine {
		return emitConnections(cc.OutOrStdout(), schema, v)
	}
	return printConnectionsHuman(cc.OutOrStdout(), v)
}

// readConnections opens the control plane, reads the connections listing, and
// maps it onto the document a consumer sees. Connections is built as a
// non-nil slice regardless of how many rows the listing carried, so an empty
// listing and one that could not be read stay distinguishable only through
// Complete, never through null-versus-empty.
func readConnections(cc *cobra.Command, opts options) (connectionsView, error) {
	cp, err := openControlPlane(cc.Context(), opts)
	if err != nil {
		return connectionsView{}, err
	}

	snapshot, err := cp.Client.ListCloudConnections(cc.Context(), cp.Bearer, cp.InstallationID)
	if err != nil {
		return connectionsView{}, classifyListError(cc.Context(), cp.Client, cp.Bearer, cp.InstallationID, err)
	}

	connections := make([]connectionView, 0, len(snapshot.Connections))
	for _, c := range snapshot.Connections {
		connections = append(connections, connectionView{
			Cloud:         c.Cloud,
			Account:       c.Account,
			RoleArn:       c.RoleArn,
			AzureTenantID: c.AzureTenantID,
			AzureClientID: c.AzureClientID,
		})
	}

	return connectionsView{
		SchemaVersion: connectSchemaVersion,
		Phase:         "connections",
		Installation:  cp.InstallationID,
		Complete:      snapshot.Complete,
		Connections:   connections,
		Warnings:      snapshot.Warnings,
	}, nil
}

// classifyListError maps a connections-listing failure onto the declared
// codes.
//
// A 404 is ambiguous on its own, the same way the setup read's is: no grant
// and a control plane too old to carry the route answer identically, so the
// installations listing this run can already fetch disambiguates it, and only
// an authoritative listing may conclude anything about visibility.
func classifyListError(ctx context.Context, client cloudapi.Client, bearer, installationID string, err error) error {
	var lapsed *cloudapi.SessionLapsedError
	if errors.As(err, &lapsed) {
		return printer.Fail(printer.CodeAuthFailed, lapsed.Error(), nil)
	}

	var forbidden *cloudapi.InstallationForbiddenError
	if errors.As(err, &forbidden) {
		return printer.Fail(printer.CodeNotAuthorized, forbidden.Error(), nil)
	}

	var notFound *cloudapi.NotFoundError
	if errors.As(err, &notFound) {
		snapshot, lerr := client.ListInstallations(ctx, bearer)
		if lerr != nil || !snapshot.Authoritative {
			// An incomplete listing licenses no claim about visibility.
			return fmt.Errorf("the control plane answered 404 for the cloud connections request, "+
				"and the installations listing could not settle whether this installation is visible to you; try again: %w", err)
		}
		for _, installation := range snapshot.Installations {
			if installation.InstallationID == installationID {
				return printer.Fail(printer.CodeControlPlaneTooOld,
					"this installation is visible to you, but its control plane predates listing cloud connections; "+
						"upgrade it and re-run", nil)
			}
		}
		return printer.Fail(printer.CodeNotAuthorized,
			"this installation is not among the ones your grants cover; if you were granted access recently, "+
				"run `formae login` to refresh your session and try again",
			map[string]any{"reason": "not_visible"})
	}

	var transient *cloudapi.TransientError
	if errors.As(err, &transient) {
		return fmt.Errorf("the control plane could not answer the cloud connections request; try again: %w", err)
	}

	return err
}

// printConnectionsHuman renders the listing as prose: one line per registered
// connection, naming the installation so a run against a non-default profile
// says which installation it read.
//
// Empty-and-complete gets the one required sentence and nothing else: no
// header, no warning, because none was raised. Every other case (rows to
// show, or a listing that could not be read in full) shares the general path
// below, which never presents the rows it did read as though they were the
// whole count: the count itself is never stated, complete or not.
func printConnectionsHuman(w io.Writer, v connectionsView) error {
	if len(v.Connections) == 0 && v.Complete {
		_, err := fmt.Fprintf(w, "No cloud accounts are registered on %s.\n", v.Installation)
		return err
	}

	var lines []string
	if v.Complete {
		lines = append(lines, fmt.Sprintf("cloud accounts registered on %s:", v.Installation))
	} else {
		lines = append(lines, fmt.Sprintf(
			"cloud accounts registered on %s (this list is partial; it could not be read in full):", v.Installation))
	}
	for _, c := range v.Connections {
		line := "  " + c.Cloud + "  " + c.Account
		if c.RoleArn != "" {
			line += "  " + c.RoleArn
		}
		lines = append(lines, line)
	}
	for _, warning := range v.Warnings {
		lines = append(lines, "warning: "+warning)
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}
