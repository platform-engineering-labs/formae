// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
