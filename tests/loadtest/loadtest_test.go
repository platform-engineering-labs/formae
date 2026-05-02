// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build loadtest

package loadtest

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/schema"
	_ "github.com/platform-engineering-labs/formae/internal/schema/all"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/loadtest/harness"
)

func TestLoadTest(t *testing.T) {
	cfg := configFromEnv(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	h, err := harness.New(cfg, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ProfileSpec().MaxDuration)
	defer cancel()

	// Phase 1: Health check
	require.NoError(t, h.WaitForHealth(ctx, 5*time.Minute))

	// Phase 2: Apply
	phaseStart := time.Now()
	forma := loadForma(t, cfg.FormaFilePath)
	require.NoError(t, h.RunApplyPhase(ctx, forma))
	h.CheckLogs(ctx, phaseStart, "apply")

	// Phase 3: Verify
	phaseStart = time.Now()
	require.NoError(t, h.RunVerifyPhase(ctx))
	h.CheckLogs(ctx, phaseStart, "verify")

	// Phase 4: Soak
	phaseStart = time.Now()
	require.NoError(t, h.RunSoakPhase(ctx))
	h.CheckLogs(ctx, phaseStart, "soak")

	// Phase 5: Destroy
	phaseStart = time.Now()
	require.NoError(t, h.RunDestroyPhase(ctx))
	h.CheckLogs(ctx, phaseStart, "destroy")

	// Collect metrics and write report
	h.CollectMetrics(ctx)

	reportPath := os.Getenv("LOADTEST_REPORT_PATH")
	if reportPath == "" {
		reportPath = "loadtest-report.json"
	}
	require.NoError(t, h.WriteReport(reportPath))
	t.Logf("Load test report written to %s", reportPath)
}

func configFromEnv(t *testing.T) *harness.Config {
	t.Helper()

	agentURL := os.Getenv("LOADTEST_AGENT_URL")
	if agentURL == "" {
		t.Skip("LOADTEST_AGENT_URL not set, skipping load test")
	}

	profile := harness.ScaleProfile(os.Getenv("LOADTEST_PROFILE"))
	if profile == "" {
		profile = harness.ProfileSmall
	}

	soakDuration := 15 * time.Minute
	if d := os.Getenv("LOADTEST_SOAK_DURATION"); d != "" {
		parsed, err := time.ParseDuration(d)
		require.NoError(t, err)
		soakDuration = parsed
	}

	spec := harness.Profiles[profile]

	return &harness.Config{
		AgentURL:       agentURL,
		Profile:        profile,
		SoakDuration:   soakDuration,
		ApplyTimeout:   spec.MaxDuration,
		DestroyTimeout: spec.MaxDuration,
		PhaseTimeout:   30 * time.Minute,
		MimirURL:       os.Getenv("LOADTEST_MIMIR_URL"),
		TempoURL:       os.Getenv("LOADTEST_TEMPO_URL"),
		LokiURL:        os.Getenv("LOADTEST_LOKI_URL"),
		FormaFilePath:  os.Getenv("LOADTEST_FORMA_FILE"),
		RunID:          os.Getenv("LOADTEST_RUN_ID"),
		GitSHA:         os.Getenv("GITHUB_SHA"),
	}
}

// loadForma evaluates the forma file at path and returns the parsed Forma.
// If path is empty, the test is skipped with an informative message.
func loadForma(t *testing.T, path string) *model.Forma {
	t.Helper()

	if path == "" {
		t.Skip("LOADTEST_FORMA_FILE not set, skipping load test")
	}

	absPath, err := filepath.Abs(path)
	require.NoError(t, err, "resolve forma file path")

	ext := filepath.Ext(absPath)
	plugin, err := schema.DefaultRegistry.GetByFileExtension(ext)
	require.NoError(t, err, "find schema plugin for extension %q", ext)

	forma, err := plugin.Evaluate(absPath, model.CommandApply, model.FormaApplyModeReconcile, nil)
	require.NoError(t, err, "evaluate forma file %q", absPath)

	return forma
}
