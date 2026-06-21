// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReport_WriteToFile(t *testing.T) {
	cfg := &Config{
		AgentURL: "http://localhost:49684",
		Profile:  ProfileSmall,
		RunID:    "test-run-123",
		GitSHA:   "abc123",
	}
	report := NewReport(cfg)
	report.Phases.Apply = ApplyPhaseResult{
		DurationSeconds:     120.5,
		ResourcesTotal:      500,
		ThroughputPerMinute: map[string]float64{"aws": 45.0, "azure": 33.0, "gcp": 33.0},
		Errors:              0,
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	require.NoError(t, report.WriteToFile(path))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var loaded Report
	require.NoError(t, json.Unmarshal(data, &loaded))

	assert.Equal(t, "small", loaded.Metadata.Profile)
	assert.Equal(t, "test-run-123", loaded.Metadata.RunID)
	assert.Equal(t, 500, loaded.Phases.Apply.ResourcesTotal)
	assert.InDelta(t, 45.0, loaded.Phases.Apply.ThroughputPerMinute["aws"], 0.01)
}
