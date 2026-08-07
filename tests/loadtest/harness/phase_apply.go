// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package harness

import (
	"context"
	"fmt"
	"time"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func (h *Harness) RunApplyPhase(ctx context.Context, forma *pkgmodel.Forma) error {
	h.logger.Info("starting apply phase")
	start := time.Now()

	resp, err := h.client.ApplyForma(forma, pkgmodel.FormaApplyModeReconcile, false, h.config.RunID, false)
	if err != nil {
		return fmt.Errorf("submit apply: %w", err)
	}
	h.logger.Info("apply command submitted", "commandID", resp.CommandID)

	if err := h.waitForCommandDone(ctx, resp.CommandID, h.config.ApplyTimeout); err != nil {
		return fmt.Errorf("apply command failed: %w", err)
	}

	duration := time.Since(start)
	spec := h.config.ProfileSpec()

	status, err := h.client.GetFormaCommandsStatus(fmt.Sprintf("id:%s", resp.CommandID), h.config.RunID, 1)
	if err != nil {
		return fmt.Errorf("get command status: %w", err)
	}

	errorCount := 0
	if len(status.Commands) > 0 {
		for _, ru := range status.Commands[0].ResourceUpdates {
			if ru.State == "Failed" {
				errorCount++
			}
		}
	}

	minutes := duration.Minutes()
	aggregateRPS := h.calculateAggregateRPS()
	theoreticalMin := float64(spec.TotalResources) / aggregateRPS
	overhead := 0.0
	if theoreticalMin > 0 {
		overhead = (duration.Seconds() - theoreticalMin) / theoreticalMin
	}

	h.report.Phases.Apply = ApplyPhaseResult{
		DurationSeconds:       duration.Seconds(),
		ResourcesTotal:        spec.TotalResources,
		ThroughputPerMinute:   h.calculateThroughput(status, minutes),
		Errors:                errorCount,
		OrchestrationOverhead: overhead,
	}

	h.logger.Info("apply phase complete", "duration", duration, "resources", spec.TotalResources, "errors", errorCount, "overhead", overhead)
	return nil
}

func (h *Harness) waitForCommandDone(ctx context.Context, commandID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		status, err := h.client.GetFormaCommandsStatus(fmt.Sprintf("id:%s", commandID), h.config.RunID, 1)
		if err != nil {
			h.logger.Warn("failed to get command status", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if len(status.Commands) == 0 {
			time.Sleep(2 * time.Second)
			continue
		}

		switch status.Commands[0].State {
		case "Success":
			return nil
		case "Failed":
			return fmt.Errorf("command %s failed", commandID)
		case "Canceled":
			return fmt.Errorf("command %s was canceled", commandID)
		}

		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("command %s timed out after %s", commandID, timeout)
}

func (h *Harness) calculateAggregateRPS() float64 {
	stats, err := h.client.Stats()
	if err != nil {
		return 2.0 // fallback: assume single plugin at 2 RPS
	}
	total := 0.0
	for _, p := range stats.Plugins {
		total += float64(p.MaxRequestsPerSecond)
	}
	if total == 0 {
		return 2.0
	}
	return total
}

func (h *Harness) calculateThroughput(status *apimodel.ListCommandStatusResponse, minutes float64) map[string]float64 {
	if minutes <= 0 || status == nil || len(status.Commands) == 0 {
		return map[string]float64{}
	}

	perProvider := map[string]int{}
	for _, ru := range status.Commands[0].ResourceUpdates {
		if ru.State == "Success" {
			// Extract provider from resource type (e.g., "AWS::S3::Bucket" -> "AWS")
			provider := extractProvider(ru.ResourceType)
			perProvider[provider]++
		}
	}

	result := map[string]float64{}
	for provider, count := range perProvider {
		result[provider] = float64(count) / minutes
	}
	return result
}

func extractProvider(resourceType string) string {
	for i, c := range resourceType {
		if c == ':' {
			return resourceType[:i]
		}
	}
	return resourceType
}
