// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package harness

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/platform-engineering-labs/formae/internal/api"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Harness wraps the formae API client and provides load-test phase helpers.
type Harness struct {
	config *Config
	client *api.Client
	logger *slog.Logger
	report *Report
}

// New validates cfg and returns a ready-to-use Harness.
func New(cfg *Config, logger *slog.Logger) (*Harness, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	apiCfg := pkgmodel.APIConfig{URL: cfg.AgentURL}
	client := api.NewClient(apiCfg, nil, http.DefaultClient)

	return &Harness{
		config: cfg,
		client: client,
		logger: logger,
		report: NewReport(cfg),
	}, nil
}

// WaitForHealth blocks until the agent responds healthy or the timeout elapses.
// WaitOnAvailable polls internally, so we race it against a deadline in a goroutine.
func (h *Harness) WaitForHealth(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan struct{})
	go func() {
		h.client.WaitOnAvailable()
		close(done)
	}()

	select {
	case <-done:
		h.logger.Info("agent is healthy")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("agent not healthy after %s", timeout)
	}
}

// WriteReport serialises the accumulated report to the given file path.
func (h *Harness) WriteReport(path string) error {
	return h.report.WriteToFile(path)
}

// CheckLogs queries Loki for unexpected errors and warnings emitted since the given timestamp.
// Results are appended to the report. If LokiURL is not set this is a no-op.
func (h *Harness) CheckLogs(ctx context.Context, since time.Time, phase string) {
	if h.config.LokiURL == "" {
		h.logger.Warn("Loki URL not set, skipping log check")
		return
	}
	checker := NewLogChecker(h.config.LokiURL)
	now := time.Now()

	errors, err := checker.CheckForErrors(ctx, since, now)
	if err != nil {
		h.logger.Warn("log check failed", "phase", phase, "error", err)
		return
	}
	for _, e := range errors {
		h.report.Errors = append(h.report.Errors, ReportError{Phase: phase, Message: e})
	}

	warnings, err := checker.CheckForWarnings(ctx, since, now)
	if err != nil {
		h.logger.Warn("warning check failed", "phase", phase, "error", err)
		return
	}
	h.report.LogWarnings = append(h.report.LogWarnings, warnings...)

	if len(errors) > 0 {
		h.logger.Error("unexpected errors in logs", "phase", phase, "count", len(errors))
	}
}

// CollectMetrics queries Mimir for agent resource utilization over the entire test run
// and stores the result in the report. If MimirURL is not set this is a no-op.
func (h *Harness) CollectMetrics(ctx context.Context) {
	if h.config.MimirURL == "" {
		h.logger.Warn("Mimir URL not set, skipping metric collection")
		return
	}
	collector := NewMetricCollector(h.config.MimirURL)
	util, err := collector.CollectResourceUtilization(ctx, h.report.Metadata.Timestamp, time.Now())
	if err != nil {
		h.logger.Warn("metric collection failed", "error", err)
		return
	}
	h.report.ResourceUtilization = *util
}
