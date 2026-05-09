// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"testing"

	"github.com/masterminds/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMinFormaeVersion_IsValidSemver(t *testing.T) {
	v, err := semver.NewVersion(MinFormaeVersion)
	require.NoError(t, err, "MinFormaeVersion should be a valid semver string")
	assert.Equal(t, "0.84.0", v.String())
}

func TestSDKVersion_IsValidSemver(t *testing.T) {
	v, err := semver.NewVersion(SDKVersion)
	require.NoError(t, err, "SDKVersion should be a valid semver string")
	assert.Equal(t, "0.2.1", v.String())
}

func TestCheckAgentCompatibility_CompatibleAgent(t *testing.T) {
	// Agent version exactly meets minimum → no error
	err := CheckAgentCompatibility(MinFormaeVersion)
	assert.NoError(t, err)
}

func TestCheckAgentCompatibility_NewerAgent(t *testing.T) {
	// Agent version exceeds minimum → no error
	err := CheckAgentCompatibility("99.0.0")
	assert.NoError(t, err)
}

func TestCheckAgentCompatibility_OlderAgent(t *testing.T) {
	// Agent version below minimum → returns error
	err := CheckAgentCompatibility("0.1.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "0.1.0")
	assert.Contains(t, err.Error(), MinFormaeVersion)
}

func TestCheckAgentCompatibility_EmptyVersion(t *testing.T) {
	// Empty version (env var not set) → no error (graceful skip)
	err := CheckAgentCompatibility("")
	assert.NoError(t, err)
}

func TestCheckAgentCompatibility_UnparseableVersion(t *testing.T) {
	// Unparseable version → no error (graceful skip, don't crash)
	err := CheckAgentCompatibility("not-a-version")
	assert.NoError(t, err)
}
