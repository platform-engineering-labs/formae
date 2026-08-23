// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// The machine documents are the output: declared fields only, no free prose,
// no credential, and failure codes that always come from the registered set.

func decodeDoc(t *testing.T, out string) map[string]any {
	t.Helper()
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got), "output is not json: %s", out)
	return got
}

func TestLinksDocument_CarriesExactlyTheDeclaredFields(t *testing.T) {
	plan := buildQuickCreatePlan(defaultPlatform(t), testSetup(), testAccount, testInstallation, options{})
	var out bytes.Buffer

	require.NoError(t, emitLinks(&out, "json", linksDocument(plan, testAccount, testInstallation, []string{"careful"})))

	got := decodeDoc(t, out.String())
	assert.Equal(t, float64(2), got["schemaVersion"])
	assert.Equal(t, "links", got["phase"])
	assert.Equal(t, "aws", got["cloud"])
	assert.Equal(t, testAccount, got["account"])
	assert.Equal(t, testInstallation, got["installation"])
	assert.Equal(t, plan.StackURL, got["stackUrl"])
	assert.Equal(t, plan.ExpectedRoleArn, got["expectedRoleArn"])
	assert.Equal(t, plan.TemplateDigest, got["templateSha256"])
	assert.Equal(t, true, got["createProvider"])
	assert.Equal(t, plan.ResumeCommand, got["resumeCommand"])
	assert.Equal(t, []any{"careful"}, got["warnings"])

	// The declared keys and nothing else: prose this repo did not write never
	// rides the stream.
	assert.Len(t, got, 11)
}

func TestLinksDocument_OmitsEmptyWarnings(t *testing.T) {
	plan := buildQuickCreatePlan(defaultPlatform(t), testSetup(), testAccount, testInstallation, options{})
	var out bytes.Buffer

	require.NoError(t, emitLinks(&out, "json", linksDocument(plan, testAccount, testInstallation, nil)))

	assert.NotContains(t, out.String(), "warnings")
}

// Status is a two-value enum: registered_unverified always — the CLI cannot
// verify the stack was applied and does not pretend otherwise — and
// already_registered for the idempotent 409-same case.
func TestRegisteredDocument_PinsBothStatusValues(t *testing.T) {
	for _, status := range []string{statusRegisteredUnverified, statusAlreadyRegistered} {
		var out bytes.Buffer
		roleArn := "arn:aws:iam::" + testAccount + ":role/r"

		require.NoError(t, emitRegistered(&out, "json",
			registeredDocument(status, testAccount, roleArn, []string{"w"})))

		got := decodeDoc(t, out.String())
		assert.Equal(t, float64(2), got["schemaVersion"])
		assert.Equal(t, "registered", got["phase"])
		assert.Equal(t, status, got["status"])
		assert.Equal(t, "aws", got["cloud"])
		assert.Equal(t, testAccount, got["account"])
		assert.Equal(t, roleArn, got["roleArn"])
		assert.Equal(t, []any{"w"}, got["warnings"])
		assert.Len(t, got, 7)
	}
	assert.Equal(t, "registered_unverified", statusRegisteredUnverified)
	assert.Equal(t, "already_registered", statusAlreadyRegistered)
}

func TestDocuments_SupportYAML(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, emitRegistered(&out, "yaml",
		registeredDocument(statusRegisteredUnverified, testAccount, "arn:aws:iam::"+testAccount+":role/r", nil)))
	assert.Contains(t, out.String(), "phase: registered")
	assert.Contains(t, out.String(), "schemaVersion: 2")
}

// An error the flow did not declare reaches the wire as internal, with a
// message this repo wrote — never err.Error(), which can quote configuration
// source holding a credential.
func TestReport_MapsUndeclaredErrorsToInternal(t *testing.T) {
	var out bytes.Buffer

	err := report(&out, printer.ConsumerMachine, "json", errors.New("pkl: inline password hunter2"), awsFallbackMessage)

	require.Error(t, err)
	got := decodeDoc(t, out.String())
	assert.Equal(t, "internal", got["code"])
	assert.NotContains(t, out.String(), "hunter2")
}

func TestReport_PassesDeclaredFailuresThrough(t *testing.T) {
	var out bytes.Buffer

	err := report(&out, printer.ConsumerMachine, "json",
		printer.Fail(printer.CodeHostedRequired, "only a hosted installation can be connected", nil), awsFallbackMessage)

	require.Error(t, err)
	got := decodeDoc(t, out.String())
	assert.Equal(t, "hosted_required", got["code"])
}

// The human consumer gets no envelope from report; the error travels as-is.
func TestReport_HumanConsumerGetsNoEnvelope(t *testing.T) {
	var out bytes.Buffer

	err := report(&out, printer.ConsumerHuman, "json", errors.New("plain"), awsFallbackMessage)

	require.Error(t, err)
	assert.Zero(t, out.Len())
	assert.False(t, strings.Contains(out.String(), "schemaVersion"))
}
