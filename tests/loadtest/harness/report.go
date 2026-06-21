// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Report struct {
	Metadata            ReportMetadata     `json:"metadata"`
	Phases              ReportPhases       `json:"phases"`
	ResourceUtilization ReportResourceUtil `json:"resource_utilization"`
	Errors              []ReportError      `json:"errors"`
	LogWarnings         []string           `json:"log_warnings"`
}

type ReportMetadata struct {
	Timestamp     time.Time `json:"timestamp"`
	Profile       string    `json:"profile"`
	AgentSpec     string    `json:"agent_spec"`
	GitSHA        string    `json:"git_sha"`
	RunID         string    `json:"run_id"`
	WorkflowRunID string    `json:"workflow_run_id,omitempty"`
}

type ReportPhases struct {
	Deploy  PhaseResult        `json:"deploy"`
	Apply   ApplyPhaseResult   `json:"apply"`
	Verify  VerifyPhaseResult  `json:"verify"`
	Soak    SoakPhaseResult    `json:"soak"`
	Destroy DestroyPhaseResult `json:"destroy"`
}

type PhaseResult struct {
	DurationSeconds float64 `json:"duration_seconds"`
}

type ApplyPhaseResult struct {
	DurationSeconds       float64            `json:"duration_seconds"`
	ResourcesTotal        int                `json:"resources_total"`
	ThroughputPerMinute   map[string]float64 `json:"throughput_per_minute"`
	Errors                int                `json:"errors"`
	OrchestrationOverhead float64            `json:"orchestration_overhead"`
}

type VerifyPhaseResult struct {
	DurationSeconds float64 `json:"duration_seconds"`
	QueriesExecuted int     `json:"queries_executed"`
}

type SoakPhaseResult struct {
	DurationSeconds    float64 `json:"duration_seconds"`
	SyncCycles         int     `json:"sync_cycles"`
	DiscoveryCycles    int     `json:"discovery_cycles"`
	ResourceCountDelta int     `json:"resource_count_delta"`
	ActorCountDelta    int     `json:"actor_count_delta"`
}

type DestroyPhaseResult struct {
	DurationSeconds    float64 `json:"duration_seconds"`
	ResourcesDestroyed int     `json:"resources_destroyed"`
	Errors             int     `json:"errors"`
}

type ReportResourceUtil struct {
	AgentPeakCPUPercent float64 `json:"agent_peak_cpu_percent"`
	AgentPeakMemoryMB   float64 `json:"agent_peak_memory_mb"`
	AgentAvgCPUPercent  float64 `json:"agent_avg_cpu_percent"`
	AgentAvgMemoryMB    float64 `json:"agent_avg_memory_mb"`
}

type ReportError struct {
	Phase   string `json:"phase"`
	Message string `json:"message"`
}

func NewReport(cfg *Config) *Report {
	spec := cfg.ProfileSpec()
	return &Report{
		Metadata: ReportMetadata{
			Timestamp: time.Now(),
			Profile:   string(cfg.Profile),
			AgentSpec: fmt.Sprintf("%s vCPU / %d MB", spec.AgentVCPU, spec.AgentMemoryMB),
			GitSHA:    cfg.GitSHA,
			RunID:     cfg.RunID,
		},
		Errors:      []ReportError{},
		LogWarnings: []string{},
	}
}

func (r *Report) WriteToFile(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}
