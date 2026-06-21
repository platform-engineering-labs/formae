// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package harness

import (
	"context"
	"fmt"
	"time"
)

func (h *Harness) RunVerifyPhase(ctx context.Context) error {
	h.logger.Info("starting verify phase")
	start := time.Now()
	queriesExecuted := 0

	spec := h.config.ProfileSpec()

	stats, err := h.client.Stats()
	if err != nil {
		return fmt.Errorf("get stats: %w", err)
	}

	totalManaged := sumMapValues(stats.ManagedResources)
	if totalManaged != spec.TotalResources {
		return fmt.Errorf("expected %d managed resources, got %d", spec.TotalResources, totalManaged)
	}
	queriesExecuted++

	for namespace, expected := range map[string]int{"AWS": spec.AWSResources, "Azure": spec.AzureResources, "GCP": spec.GCPResources} {
		if expected == 0 {
			continue
		}
		actual := stats.ManagedResources[namespace]
		if actual != expected {
			return fmt.Errorf("expected %d %s resources, got %d", expected, namespace, actual)
		}
		queriesExecuted++
	}

	totalErrors := sumMapValues(stats.ResourceErrors)
	if totalErrors > 0 {
		return fmt.Errorf("found %d resource errors after apply", totalErrors)
	}
	queriesExecuted++

	// Exercise query API at scale
	queryPatterns := []string{"managed:true", "stack:default"}
	for _, query := range queryPatterns {
		_, err := h.client.ExtractResources(query)
		if err != nil {
			h.logger.Warn("query failed", "query", query, "error", err)
		}
		queriesExecuted++
	}

	stacks, err := h.client.ListStacks()
	if err != nil {
		return fmt.Errorf("list stacks: %w", err)
	}
	h.logger.Info("stacks found", "count", len(stacks))
	queriesExecuted++

	duration := time.Since(start)
	h.report.Phases.Verify = VerifyPhaseResult{
		DurationSeconds: duration.Seconds(),
		QueriesExecuted: queriesExecuted,
	}

	h.logger.Info("verify phase complete", "duration", duration, "queries", queriesExecuted)
	return nil
}

func sumMapValues(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}
