// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseSubmitCommandErrorResponse_ResourceHasDependents asserts the client
// decodes a 409 ResourceHasDependents body into a typed
// FormaResourceHasDependentsError (so the CLI renders it) rather than falling
// through to "unknown error type".
func TestParseSubmitCommandErrorResponse_ResourceHasDependents(t *testing.T) {
	body, err := json.Marshal(apimodel.ErrorResponse[apimodel.FormaResourceHasDependentsError]{
		ErrorType: apimodel.ResourceHasDependents,
		Data: apimodel.FormaResourceHasDependentsError{
			Dependents: []apimodel.ResourceDependent{
				{ResourceLabel: "child-subnet", ResourceType: "FakeAWS::EC2::Subnet", Stack: "consumer-stack", CascadeSource: "parent-vpc"},
			},
		},
	})
	require.NoError(t, err)

	c := &Client{}
	_, perr := c.parseSubmitCommandErrorResponse(io.NopCloser(bytes.NewReader(body)))
	require.Error(t, perr)

	var got *apimodel.ErrorResponse[apimodel.FormaResourceHasDependentsError]
	require.ErrorAs(t, perr, &got, "must decode into a typed FormaResourceHasDependentsError")
	require.Len(t, got.Data.Dependents, 1)
	assert.Equal(t, "child-subnet", got.Data.Dependents[0].ResourceLabel)
	assert.Equal(t, "consumer-stack", got.Data.Dependents[0].Stack)
	assert.Equal(t, "parent-vpc", got.Data.Dependents[0].CascadeSource)
}

func TestFormatEndpointStandardPort(t *testing.T) {
	want := "http://localhost:49684"
	got := formatEndpoint("http://localhost", 49684)

	assert.Equal(t, want, got)
}

func TestFormatEndpointHTTPSDefault(t *testing.T) {
	want := "https://example.awsapprunner.com"
	got := formatEndpoint("https://example.awsapprunner.com", 443)

	assert.Equal(t, want, got)
}

func TestFormatEndpointHTTPDefault(t *testing.T) {
	want := "http://example.com"
	got := formatEndpoint("http://example.com", 80)

	assert.Equal(t, want, got)
}

func TestFormatEndpointHTTPSNonDefault(t *testing.T) {
	want := "https://example.com:8443"
	got := formatEndpoint("https://example.com", 8443)

	assert.Equal(t, want, got)
}

func TestFormatEndpointHTTPWith443(t *testing.T) {
	want := "http://example.com:443"
	got := formatEndpoint("http://example.com", 443)

	assert.Equal(t, want, got)
}

func TestFormatEndpointHTTPSWith80(t *testing.T) {
	want := "https://example.com:80"
	got := formatEndpoint("https://example.com", 80)

	assert.Equal(t, want, got)
}
