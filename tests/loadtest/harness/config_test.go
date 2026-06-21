// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package harness

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_Validate(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		c := &Config{AgentURL: "http://localhost:49684", Profile: ProfileMedium}
		require.NoError(t, c.Validate())
	})
	t.Run("missing agent URL", func(t *testing.T) {
		c := &Config{Profile: ProfileMedium}
		assert.ErrorContains(t, c.Validate(), "agent URL is required")
	})
	t.Run("unknown profile", func(t *testing.T) {
		c := &Config{AgentURL: "http://localhost:49684", Profile: "huge"}
		assert.ErrorContains(t, c.Validate(), "unknown profile")
	})
}

func TestProfileSpec(t *testing.T) {
	c := &Config{AgentURL: "http://localhost:49684", Profile: ProfileSmall}
	spec := c.ProfileSpec()
	assert.Equal(t, 500, spec.TotalResources)
	assert.Equal(t, 200, spec.AWSResources)
	assert.Equal(t, 150, spec.AzureResources)
	assert.Equal(t, 150, spec.GCPResources)
	assert.Equal(t, 30*time.Minute, spec.MaxDuration)
}
