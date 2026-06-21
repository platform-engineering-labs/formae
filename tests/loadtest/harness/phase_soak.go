// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package harness

import (
	"context"
	"fmt"
	"time"
)

func (h *Harness) RunSoakPhase(ctx context.Context) error {
	h.logger.Info("starting soak phase", "duration", h.config.SoakDuration)
	start := time.Now()

	baselineStats, err := h.client.Stats()
	if err != nil {
		return fmt.Errorf("get baseline stats: %w", err)
	}
	baselineManagedCount := sumMapValues(baselineStats.ManagedResources)

	syncCycles := 0
	discoveryCycles := 0
	sampleInterval := 30 * time.Second
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()

	soakDeadline := time.Now().Add(h.config.SoakDuration)

	for time.Now().Before(soakDeadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := h.client.ForceSync(); err != nil {
				h.logger.Warn("force sync failed", "error", err)
			} else {
				syncCycles++
			}

			if err := h.client.ForceDiscover(); err != nil {
				h.logger.Warn("force discover failed", "error", err)
			} else {
				discoveryCycles++
			}

			stats, err := h.client.Stats()
			if err != nil {
				h.logger.Warn("stats sample failed", "error", err)
				continue
			}

			currentManaged := sumMapValues(stats.ManagedResources)
			h.logger.Info("soak sample",
				"managed_resources", currentManaged,
				"plugins", len(stats.Plugins),
				"elapsed", time.Since(start).Round(time.Second),
			)

			if currentManaged != baselineManagedCount {
				h.logger.Warn("resource count changed during soak",
					"baseline", baselineManagedCount,
					"current", currentManaged,
				)
			}
		}
	}

	finalStats, err := h.client.Stats()
	if err != nil {
		return fmt.Errorf("get final stats: %w", err)
	}
	finalManagedCount := sumMapValues(finalStats.ManagedResources)

	duration := time.Since(start)
	h.report.Phases.Soak = SoakPhaseResult{
		DurationSeconds:    duration.Seconds(),
		SyncCycles:         syncCycles,
		DiscoveryCycles:    discoveryCycles,
		ResourceCountDelta: finalManagedCount - baselineManagedCount,
		ActorCountDelta:    0, // Populated by metric collector
	}

	h.logger.Info("soak phase complete",
		"duration", duration, "sync_cycles", syncCycles,
		"discovery_cycles", discoveryCycles, "resource_delta", finalManagedCount-baselineManagedCount,
	)

	if finalManagedCount != baselineManagedCount {
		return fmt.Errorf("resource leak detected: baseline=%d, final=%d", baselineManagedCount, finalManagedCount)
	}
	return nil
}
