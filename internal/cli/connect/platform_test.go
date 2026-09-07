// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearConnectEnv removes the connect override pair for the duration of a
// test. os.Unsetenv rather than t.Setenv(k, ""), because a variable present
// but empty deliberately counts as set.
func clearConnectEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"FORMAE_CONNECT_ISSUER", "FORMAE_CONNECT_TEMPLATE_BASE"} {
		k := k
		if old, ok := os.LookupEnv(k); ok {
			t.Cleanup(func() { _ = os.Setenv(k, old) })
		}
		require.NoError(t, os.Unsetenv(k))
	}
}

func TestResolveConnectPlatform_DefaultsToTheProductionPair(t *testing.T) {
	clearConnectEnv(t)

	p, err := resolveConnectPlatform()

	require.NoError(t, err)
	assert.Equal(t, "https://oidc.cloud.formae.ai", p.Issuer)
	assert.Equal(t, ProductionIssuer, p.Issuer)
	assert.Equal(t, "https://formae-connect-templates.s3.us-east-1.amazonaws.com", p.TemplateBase)
}

// Half a pair is refused, never silently completed: an issuer pointed at a
// test fixture with production templates (or the reverse) is exactly the
// mismatch the pair exists to prevent.
func TestResolveConnectPlatform_RefusesAHalfSetPair(t *testing.T) {
	clearConnectEnv(t)
	t.Setenv("FORMAE_CONNECT_ISSUER", "https://oidc.test.example")

	_, err := resolveConnectPlatform()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FORMAE_CONNECT_ISSUER")
	assert.Contains(t, err.Error(), "FORMAE_CONNECT_TEMPLATE_BASE")
}

func TestResolveConnectPlatform_RefusesAHalfSetBase(t *testing.T) {
	clearConnectEnv(t)
	t.Setenv("FORMAE_CONNECT_TEMPLATE_BASE", "https://templates.test.example")

	_, err := resolveConnectPlatform()

	require.Error(t, err)
	assert.True(t, errors.Is(err, errConnectPairHalfSet))
}

func TestResolveConnectPlatform_UsesBothOverridesCanonicalised(t *testing.T) {
	clearConnectEnv(t)
	t.Setenv("FORMAE_CONNECT_ISSUER", "HTTPS://OIDC.Test.Example:443/")
	t.Setenv("FORMAE_CONNECT_TEMPLATE_BASE", "HTTPS://Templates.Test.Example/")

	p, err := resolveConnectPlatform()

	require.NoError(t, err)
	assert.Equal(t, "https://oidc.test.example", p.Issuer)
	assert.Equal(t, "https://templates.test.example", p.TemplateBase)
}

// The login platform pair governs where the bearer goes; this pair governs
// what the AWS-side trust artifacts pin. Different trust domains, different
// overrides: the login pair must not move this one.
func TestResolveConnectPlatform_IgnoresTheLoginPlatformPair(t *testing.T) {
	clearConnectEnv(t)
	t.Setenv("FORMAE_CLOUD_URL", "https://cloud.evil.example")
	t.Setenv("FORMAE_CLOUD_ISSUER", "https://auth.evil.example")

	p, err := resolveConnectPlatform()

	require.NoError(t, err)
	assert.Equal(t, ProductionIssuer, p.Issuer)
}

func TestResolveConnectPlatform_RefusesAnUncanonicalisableOverride(t *testing.T) {
	clearConnectEnv(t)
	t.Setenv("FORMAE_CONNECT_ISSUER", "not a url")
	t.Setenv("FORMAE_CONNECT_TEMPLATE_BASE", "https://templates.test.example")

	_, err := resolveConnectPlatform()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connect issuer")
}
