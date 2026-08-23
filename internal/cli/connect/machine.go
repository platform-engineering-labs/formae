// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
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
type registeredView struct {
	SchemaVersion int      `json:"schemaVersion" yaml:"schemaVersion"`
	Phase         string   `json:"phase" yaml:"phase"` // "registered"
	Status        string   `json:"status" yaml:"status"`
	Cloud         string   `json:"cloud" yaml:"cloud"`
	Account       string   `json:"account" yaml:"account"`
	RoleArn       string   `json:"roleArn" yaml:"roleArn"`
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
