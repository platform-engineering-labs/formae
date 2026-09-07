// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/orbital/opm/records"
	"github.com/platform-engineering-labs/orbital/ops"
)

func mustVersion(t *testing.T, v string) *ops.Version {
	t.Helper()
	ver := &ops.Version{}
	if err := ver.Parse(v); err != nil {
		t.Fatalf("failed to parse version %q: %v", v, err)
	}
	return ver
}

// pkgAt builds an available package at a core version (orbital drops the
// pre-release; dev-ness is a channel distinction, not part of the version).
func pkgAt(t *testing.T, v string) *records.Package {
	t.Helper()
	return &records.Package{Header: &ops.Header{Name: "formae", Version: mustVersion(t, v)}}
}

func statusWith(pkgs ...*records.Package) *records.Status {
	return &records.Status{Available: pkgs}
}

func TestResolveFormaeCandidate(t *testing.T) {
	min := mustVersion(t, "0.86.0")

	t.Run("prefers highest qualifying stable release", func(t *testing.T) {
		query := func(channel string) (*records.Status, error) {
			switch channel {
			case "stable":
				return statusWith(pkgAt(t, "0.85.2"), pkgAt(t, "0.86.0"), pkgAt(t, "0.87.1")), nil
			case "dev":
				return statusWith(pkgAt(t, "0.88.0")), nil
			default:
				return nil, fmt.Errorf("unexpected channel %q", channel)
			}
		}

		pkg, channel, err := resolveFormaeCandidate(query, min)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if channel != "stable" {
			t.Errorf("channel = %q, want stable", channel)
		}
		if got := pkg.Version.Short(); got != "0.87.1" {
			t.Errorf("version = %q, want 0.87.1", got)
		}
	})

	t.Run("falls back to dev when no stable release meets the minimum", func(t *testing.T) {
		query := func(channel string) (*records.Status, error) {
			switch channel {
			case "stable":
				return statusWith(pkgAt(t, "0.85.1"), pkgAt(t, "0.85.2")), nil
			case "dev":
				return statusWith(pkgAt(t, "0.86.0")), nil
			default:
				return nil, fmt.Errorf("unexpected channel %q", channel)
			}
		}

		pkg, channel, err := resolveFormaeCandidate(query, min)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if channel != "dev" {
			t.Errorf("channel = %q, want dev", channel)
		}
		if got := pkg.Version.Short(); got != "0.86.0" {
			t.Errorf("version = %q, want 0.86.0", got)
		}
	})

	t.Run("picks the latest dev build of the same core version by timestamp", func(t *testing.T) {
		older := pkgAt(t, "0.86.0")
		older.Version.Timestamp = time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)
		newer := pkgAt(t, "0.86.0")
		newer.Version.Timestamp = time.Date(2026, 5, 27, 15, 0, 0, 0, time.UTC)

		query := func(channel string) (*records.Status, error) {
			switch channel {
			case "stable":
				return statusWith(pkgAt(t, "0.85.2")), nil
			case "dev":
				return statusWith(older, newer), nil
			default:
				return nil, fmt.Errorf("unexpected channel %q", channel)
			}
		}

		pkg, channel, err := resolveFormaeCandidate(query, min)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if channel != "dev" {
			t.Errorf("channel = %q, want dev", channel)
		}
		if !pkg.Version.Timestamp.Equal(newer.Version.Timestamp) {
			t.Errorf("timestamp = %v, want the newer build %v", pkg.Version.Timestamp, newer.Version.Timestamp)
		}
	})

	t.Run("errors when neither channel has a qualifying release", func(t *testing.T) {
		query := func(channel string) (*records.Status, error) {
			switch channel {
			case "stable":
				return statusWith(pkgAt(t, "0.85.2")), nil
			case "dev":
				return statusWith(pkgAt(t, "0.85.3")), nil
			default:
				return nil, fmt.Errorf("unexpected channel %q", channel)
			}
		}

		_, _, err := resolveFormaeCandidate(query, min)
		if err == nil {
			t.Fatal("expected an error when no channel has a release >= 0.86.0, got nil")
		}
	})
}

func TestRetryResolve(t *testing.T) {
	pkg := pkgAt(t, "0.89.0")

	t.Run("returns immediately on success without sleeping", func(t *testing.T) {
		var sleeps []time.Duration
		got, channel, err := retryResolve(func() (*records.Package, string, error) {
			return pkg, "dev", nil
		}, 6, func(d time.Duration) { sleeps = append(sleeps, d) })
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != pkg || channel != "dev" {
			t.Errorf("retryResolve() = (%v, %q), want the resolved package on dev", got, channel)
		}
		if len(sleeps) != 0 {
			t.Errorf("slept %d times on immediate success, want 0", len(sleeps))
		}
	})

	t.Run("retries through transient failures until the channel recovers", func(t *testing.T) {
		var sleeps []time.Duration
		calls := 0
		got, channel, err := retryResolve(func() (*records.Package, string, error) {
			calls++
			if calls < 3 {
				return nil, "", errors.New("querying dev channel for formae versions: no available packages for: formae")
			}
			return pkg, "dev", nil
		}, 6, func(d time.Duration) { sleeps = append(sleeps, d) })
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != pkg || channel != "dev" {
			t.Errorf("retryResolve() = (%v, %q), want the resolved package on dev", got, channel)
		}
		if calls != 3 {
			t.Errorf("resolve called %d times, want 3", calls)
		}
		if len(sleeps) != 2 {
			t.Errorf("slept %d times, want 2 (one before each retry)", len(sleeps))
		}
	})

	t.Run("gives up with the last error once attempts are exhausted", func(t *testing.T) {
		var sleeps []time.Duration
		calls := 0
		_, _, err := retryResolve(func() (*records.Package, string, error) {
			calls++
			return nil, "", fmt.Errorf("attempt %d failed", calls)
		}, 4, func(d time.Duration) { sleeps = append(sleeps, d) })
		if err == nil {
			t.Fatal("expected an error after exhausting attempts")
		}
		if calls != 4 {
			t.Errorf("resolve called %d times, want 4", calls)
		}
		if len(sleeps) != 3 {
			t.Errorf("slept %d times, want 3", len(sleeps))
		}
		if !strings.Contains(err.Error(), "attempt 4 failed") {
			t.Errorf("error %q should be the last attempt's error", err)
		}
	})
}
