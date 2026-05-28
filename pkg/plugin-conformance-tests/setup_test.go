// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"fmt"
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
