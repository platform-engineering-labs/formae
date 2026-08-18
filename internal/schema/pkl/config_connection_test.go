//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pklmodel "github.com/platform-engineering-labs/formae/internal/schema/pkl/model"
)

func evalConfig(t *testing.T, path string) *pklmodel.Config {
	t.Helper()
	cfg, err := PKL{}.rawConfig(path)
	require.NoError(t, err)
	return cfg
}

func TestConnectionDecodesHostedArm(t *testing.T) {
	cfg := evalConfig(t, "testdata/config/connection_hosted.pkl")

	hosted, ok := cfg.Cli.Connection.(*pklmodel.HostedConnection)
	require.True(t, ok, "expected *HostedConnection, got %T", cfg.Cli.Connection)
	assert.Equal(t, "https://cloud.formae.ai", hosted.Endpoint)
	assert.Equal(t, "3HzFPXfPDGhwLJJVtaHbmFs6vLa", hosted.Installation)
	require.NotNil(t, hosted.Auth)
	assert.Equal(t, "oidc", hosted.Auth.Properties["type"])
}

func TestConnectionDecodesClassicArm(t *testing.T) {
	cfg := evalConfig(t, "testdata/config/connection_classic.pkl")

	classic, ok := cfg.Cli.Connection.(*pklmodel.ClassicConnection)
	require.True(t, ok, "expected *ClassicConnection, got %T", cfg.Cli.Connection)
	assert.Equal(t, "http://agent.example", classic.URL)
	assert.Equal(t, int32(8080), classic.Port)
	assert.Nil(t, classic.Auth)
}

func TestConnectionAbsentDecodesToNil(t *testing.T) {
	cfg := evalConfig(t, "testdata/config/connection_absent.pkl")

	assert.Nil(t, cfg.Cli.Connection)
	assert.Nil(t, cfg.Cli.API)
}

// A profile written in the nested amendment form the CLI documents still
// evaluates, and its values still arrive in the decode model. The deprecated
// properties must stay nullable with no explicit default: declaring them
// `= null` makes this form fail to evaluate.
func TestLegacyNestedAPIAmendmentStillEvaluates(t *testing.T) {
	cfg := evalConfig(t, "testdata/config/legacy_api_nested.pkl")

	require.NotNil(t, cfg.Cli.API)
	assert.Equal(t, "http://agent.example", cfg.Cli.API.URL)
	assert.Equal(t, int32(8080), cfg.Cli.API.Port)
	assert.Nil(t, cfg.Cli.Connection)
}
