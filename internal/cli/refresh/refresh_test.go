// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package refresh

import (
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
)

// orbital's Refresh() logs per-repository fetch/validation failures at ERROR
// level but always returns nil. refreshErrorCounter must count exactly those
// ERROR records (not INFO/WARN) so refresh can report a failure instead of a
// false "done.".
func TestRefreshErrorCounter_CountsOnlyErrorRecords(t *testing.T) {
	var n atomic.Int64
	log := slog.New(refreshErrorCounter{Handler: slog.NewTextHandler(io.Discard, nil), count: &n})

	log.Info("refreshing pel#stable")
	log.Warn("no metadata: community#stable")
	log.Error("refresh failed: pel#stable")
	log.Error("metadata validation failed: community#stable")

	if got := n.Load(); got != 2 {
		t.Errorf("expected 2 ERROR records counted, got %d", got)
	}
}
