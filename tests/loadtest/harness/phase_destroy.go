// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package harness

import (
	"context"
	"fmt"
	"time"
)

func (h *Harness) RunDestroyPhase(ctx context.Context) error {
	h.logger.Info("starting destroy phase")
	start := time.Now()

	stacks, err := h.client.ListStacks()
	if err != nil {
		return fmt.Errorf("list stacks: %w", err)
	}

	errorCount := 0
	resourcesDestroyed := 0

	for _, stack := range stacks {
		query := fmt.Sprintf("stack:%s", stack.Label)
		resp, err := h.client.DestroyByQuery(query, false, h.config.RunID)
		if err != nil {
			h.logger.Error("destroy failed for stack", "stack", stack.Label, "error", err)
			errorCount++
			continue
		}

		h.logger.Info("destroy submitted", "stack", stack.Label, "commandID", resp.CommandID)

		if err := h.waitForCommandDone(ctx, resp.CommandID, h.config.DestroyTimeout); err != nil {
			h.logger.Error("destroy did not complete", "stack", stack.Label, "error", err)
			errorCount++
			continue
		}

		status, err := h.client.GetFormaCommandsStatus(fmt.Sprintf("id:%s", resp.CommandID), h.config.RunID, 1)
		if err == nil && len(status.Commands) > 0 {
			for _, ru := range status.Commands[0].ResourceUpdates {
				if ru.State == "Success" {
					resourcesDestroyed++
				} else if ru.State == "Failed" {
					errorCount++
				}
			}
		}
	}

	stats, err := h.client.Stats()
	if err != nil {
		return fmt.Errorf("get final stats: %w", err)
	}
	remaining := sumMapValues(stats.ManagedResources)
	if remaining > 0 {
		h.logger.Error("resources remain after destroy", "count", remaining)
		errorCount += remaining
	}

	duration := time.Since(start)
	h.report.Phases.Destroy = DestroyPhaseResult{
		DurationSeconds:    duration.Seconds(),
		ResourcesDestroyed: resourcesDestroyed,
		Errors:             errorCount,
	}

	h.logger.Info("destroy phase complete", "duration", duration, "destroyed", resourcesDestroyed, "errors", errorCount)

	if errorCount > 0 {
		return fmt.Errorf("destroy phase completed with %d errors", errorCount)
	}
	return nil
}
