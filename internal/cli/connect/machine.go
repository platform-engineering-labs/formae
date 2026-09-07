// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// The machine protocol for a connect run: one stream, one document per run,
// either a `links` document when quick-create emits and stops, a
// `registered` document when a registration happened, a `connections`
// document when list reads what is registered, or a failure envelope through
// printer.PrintFailure. No free prose crosses: a consumer gets the fields
// declared here and renders its own text, and no document ever carries a
// credential.

// connectSchemaVersion identifies the shape of both documents. A consumer
// reads it before any other field, so a document it cannot understand is an
// error rather than a guess.
const connectSchemaVersion = 2

// The registration statuses: registered_unverified always — the CLI cannot
// verify the stack was applied and does not pretend otherwise — and
// already_registered marks the idempotent 409-same case.
const (
	statusRegisteredUnverified = "registered_unverified"
	statusAlreadyRegistered    = "already_registered"
)

// linksView is the quick-create emit: self-contained, so a consumer can drive
// the console flow and come back with the RoleArn. CreateProvider echoes the
// answer carried into the link, so a consumer sees which stack variant it is
// driving.
type linksView struct {
	SchemaVersion   int      `json:"schemaVersion" yaml:"schemaVersion"`
	Phase           string   `json:"phase" yaml:"phase"` // "links"
	Cloud           string   `json:"cloud" yaml:"cloud"`
	Account         string   `json:"account" yaml:"account"`
	Installation    string   `json:"installation" yaml:"installation"`
	StackURL        string   `json:"stackUrl" yaml:"stackUrl"`
	ExpectedRoleArn string   `json:"expectedRoleArn" yaml:"expectedRoleArn"`
	TemplateSha256  string   `json:"templateSha256" yaml:"templateSha256"`
	CreateProvider  bool     `json:"createProvider" yaml:"createProvider"`
	ResumeCommand   string   `json:"resumeCommand" yaml:"resumeCommand"`
	Warnings        []string `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

// registeredView reports registration. Status is the two-value enum above.
//
// Each cloud carries exactly its own trust coordinate, and the others are
// omitted rather than sent empty: a consumer reads the cloud first and then
// the field that cloud has, and an empty roleArn on a GCP document would
// invite it to read one that never existed. AWS documents are unchanged,
// because an AWS registration always sets roleArn, which is why the schema
// version does not move.
type registeredView struct {
	SchemaVersion int    `json:"schemaVersion" yaml:"schemaVersion"`
	Phase         string `json:"phase" yaml:"phase"` // "registered"
	Status        string `json:"status" yaml:"status"`
	Cloud         string `json:"cloud" yaml:"cloud"`
	Account       string `json:"account" yaml:"account"`
	RoleArn       string `json:"roleArn,omitempty" yaml:"roleArn,omitempty"`
	// WorkloadIdentityProvider is the GCP coordinate.
	WorkloadIdentityProvider string `json:"workloadIdentityProvider,omitempty" yaml:"workloadIdentityProvider,omitempty"`
	// AzureTenantID and AzureClientID are the Azure coordinate: the
	// subscription's Entra tenant and the managed identity's client id
	// (its "appId", distinct from its service principal object id).
	AzureTenantID string   `json:"azureTenantId,omitempty" yaml:"azureTenantId,omitempty"`
	AzureClientID string   `json:"azureClientId,omitempty" yaml:"azureClientId,omitempty"`
	Warnings      []string `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

// linksDocument builds the quick-create emit from the plan. The
// multi-installation and name-mismatch warnings ride Warnings.
func linksDocument(plan quickCreatePlan, account, installationID string, warnings []string) linksView {
	return linksView{
		SchemaVersion:   connectSchemaVersion,
		Phase:           "links",
		Cloud:           "aws",
		Account:         account,
		Installation:    installationID,
		StackURL:        plan.StackURL,
		ExpectedRoleArn: plan.ExpectedRoleArn,
		TemplateSha256:  plan.TemplateDigest,
		CreateProvider:  plan.CreateProvider,
		ResumeCommand:   plan.ResumeCommand,
		Warnings:        warnings,
	}
}

// gcpRegisteredDocument builds the registration report for a GCP project.
func gcpRegisteredDocument(status, project, provider string, warnings []string) registeredView {
	return registeredView{
		SchemaVersion:            connectSchemaVersion,
		Phase:                    "registered",
		Status:                   status,
		Cloud:                    "gcp",
		Account:                  project,
		WorkloadIdentityProvider: provider,
		Warnings:                 warnings,
	}
}

// azureRegisteredDocument builds the registration report for an Azure
// subscription.
func azureRegisteredDocument(status, subscription, tenantID, clientID string, warnings []string) registeredView {
	return registeredView{
		SchemaVersion: connectSchemaVersion,
		Phase:         "registered",
		Status:        status,
		Cloud:         "azure",
		Account:       subscription,
		AzureTenantID: tenantID,
		AzureClientID: clientID,
		Warnings:      warnings,
	}
}

// registeredDocument builds the registration report.
func registeredDocument(status, account, roleArn string, warnings []string) registeredView {
	return registeredView{
		SchemaVersion: connectSchemaVersion,
		Phase:         "registered",
		Status:        status,
		Cloud:         "aws",
		Account:       account,
		RoleArn:       roleArn,
		Warnings:      warnings,
	}
}

// connectionView is one registered connection as reported to a consumer.
// RoleArn is omitted for a cloud that carries no role (GCP, Azure), so a
// consumer never mistakes an absent field for an empty one.
type connectionView struct {
	Cloud   string `json:"cloud" yaml:"cloud"`
	Account string `json:"account" yaml:"account"`
	RoleArn string `json:"roleArn,omitempty" yaml:"roleArn,omitempty"`
	// WorkloadIdentityProvider is present for GCP and omitted elsewhere, for
	// the same reason roleArn is.
	WorkloadIdentityProvider string `json:"workloadIdentityProvider,omitempty" yaml:"workloadIdentityProvider,omitempty"`
	// AzureTenantID and AzureClientID are present for Azure and omitted
	// elsewhere, for the same reason.
	AzureTenantID string `json:"azureTenantId,omitempty" yaml:"azureTenantId,omitempty"`
	AzureClientID string `json:"azureClientId,omitempty" yaml:"azureClientId,omitempty"`
}

// connectionsView is the list emit. Connections is always a slice, never
// nil: a consumer branches on empty versus absent, and a null value would
// erase that distinction. Complete says whether the listing was read in
// full, mirroring cloudapi.ConnectionsSnapshot.Complete.
type connectionsView struct {
	SchemaVersion int              `json:"schemaVersion" yaml:"schemaVersion"`
	Phase         string           `json:"phase" yaml:"phase"` // "connections"
	Installation  string           `json:"installation" yaml:"installation"`
	Complete      bool             `json:"complete" yaml:"complete"`
	Connections   []connectionView `json:"connections" yaml:"connections"`
	Warnings      []string         `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

func emitLinks(w io.Writer, schema string, v linksView) error {
	return printer.NewMachineReadablePrinter[linksView](w, schema).Print(&v)
}

func emitRegistered(w io.Writer, schema string, v registeredView) error {
	return printer.NewMachineReadablePrinter[registeredView](w, schema).Print(&v)
}

func emitConnections(w io.Writer, schema string, v connectionsView) error {
	return printer.NewMachineReadablePrinter[connectionsView](w, schema).Print(&v)
}

// profileResolution is one local AWS profile's resolution: either the
// account its credentials authenticate to, or why that could not be
// determined. Never both, never neither.
type profileResolution struct {
	Name        string `json:"name" yaml:"name"`
	Account     string `json:"account,omitempty" yaml:"account,omitempty"`
	Unavailable string `json:"unavailable,omitempty" yaml:"unavailable,omitempty"`
}

// profilesView is the `connect aws profiles` emit: every profile the local
// shared AWS config names, alongside the account it resolves to. Profiles is
// always a slice, never nil, for the same reason Connections is on
// connectionsView. Warnings is always present too, even empty: this document
// carries no Complete flag to hedge against, so there is nothing else to
// signal a partial read with.
type profilesView struct {
	SchemaVersion int                 `json:"schemaVersion" yaml:"schemaVersion"`
	Phase         string              `json:"phase" yaml:"phase"` // "awsProfiles"
	Profiles      []profileResolution `json:"profiles" yaml:"profiles"`
	Warnings      []string            `json:"warnings" yaml:"warnings"`
}

func emitProfiles(w io.Writer, schema string, v profilesView) error {
	return printer.NewMachineReadablePrinter[profilesView](w, schema).Print(&v)
}

// azureTemplateView is the credential-less path's emit: the deep link a
// consumer shows the user, the coordinates baked into it, and the template
// itself.
//
// It mirrors linksView's job for AWS. The link is the field that matters: it
// is the only route that asks nothing of the machine running this, and before
// this document existed it appeared solely in human-readable stderr, where a
// harness driving the flow could not reach it without scraping.
//
// Template is a decoded map rather than raw JSON bytes so it renders as
// structured data under both output schemas; as json.RawMessage it would have
// marshalled to a base64 string under yaml.
type azureTemplateView struct {
	SchemaVersion  int            `json:"schemaVersion" yaml:"schemaVersion"`
	Phase          string         `json:"phase" yaml:"phase"` // "template"
	Cloud          string         `json:"cloud" yaml:"cloud"`
	Installation   string         `json:"installation" yaml:"installation"`
	FormaeTenantID string         `json:"formaeTenantId" yaml:"formaeTenantId"`
	DeepLink       string         `json:"deepLink" yaml:"deepLink"`
	TemplateURL    string         `json:"templateUrl" yaml:"templateUrl"`
	Template       map[string]any `json:"template" yaml:"template"`
}

// newAzureTemplateView assembles the emit from the same inputs the human
// rendering uses, so the two cannot describe different deployments.
func newAzureTemplateView(consoleOrigin, installationID, formaeTenantID string,
	template []byte) (azureTemplateView, error) {
	var decoded map[string]any
	if err := json.Unmarshal(template, &decoded); err != nil {
		return azureTemplateView{}, fmt.Errorf("decoding the ARM template for machine output: %w", err)
	}
	templateURL := azureTemplateConsoleURL(consoleOrigin, installationID, formaeTenantID)
	return azureTemplateView{
		SchemaVersion:  connectSchemaVersion,
		Phase:          "template",
		Cloud:          "azure",
		Installation:   installationID,
		FormaeTenantID: formaeTenantID,
		DeepLink:       azurePortalDeepLink(templateURL),
		TemplateURL:    templateURL,
		Template:       decoded,
	}, nil
}

func emitAzureTemplate(w io.Writer, schema string, v azureTemplateView) error {
	return printer.NewMachineReadablePrinter[azureTemplateView](w, schema).Print(&v)
}
