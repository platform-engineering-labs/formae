// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package update

import (
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/orbital/opm/records"
	"github.com/platform-engineering-labs/orbital/ops"
)

func pkg(major, minor, patch uint64, installed bool) *records.Package {
	return &records.Package{
		Installed: installed,
		Header: &ops.Header{
			Version: &ops.Version{
				Major:     major,
				Minor:     minor,
				Patch:     patch,
				Timestamp: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}
}

// A cold index with a single, not-yet-installed version must still list that
// version. orbital's AvailableForSimple drops the candidate at index 0, so a
// cold index rendered an empty list; formatAvailableVersions must not.
func TestFormatAvailableVersions_ColdSingleCandidateNotDropped(t *testing.T) {
	out := formatAvailableVersions([]*records.Package{pkg(0, 88, 0, false)})

	if !strings.Contains(out, "0.88.0") {
		t.Errorf("expected the only available version 0.88.0 to be listed, got:\n%s", out)
	}
	if strings.Contains(out, "installed:") {
		t.Errorf("expected no installed line when nothing is installed, got:\n%s", out)
	}
}

// With multiple not-installed candidates and nothing installed, every distinct
// version must appear (AvailableForSimple would omit the first).
func TestFormatAvailableVersions_MultipleCandidatesAllListed(t *testing.T) {
	out := formatAvailableVersions([]*records.Package{
		pkg(0, 88, 0, false),
		pkg(0, 87, 1, false),
	})

	for _, want := range []string{"0.88.0", "0.87.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected version %s to be listed, got:\n%s", want, out)
		}
	}
}

// The installed version is surfaced on its own line and de-duplicated in the
// available list.
func TestFormatAvailableVersions_InstalledSurfacedAndDeduped(t *testing.T) {
	out := formatAvailableVersions([]*records.Package{
		pkg(0, 87, 1, true),
		pkg(0, 87, 1, false), // same version present in both tree and repo
		pkg(0, 88, 0, false),
	})

	if !strings.Contains(out, "installed: 0.87.1") {
		t.Errorf("expected installed line for 0.87.1, got:\n%s", out)
	}
	if !strings.Contains(out, "0.88.0") {
		t.Errorf("expected available version 0.88.0, got:\n%s", out)
	}
	if n := strings.Count(out, "0.87.1"); n != 2 { // installed line + one deduped entry
		t.Errorf("expected 0.87.1 exactly twice (installed line + one list entry), got %d in:\n%s", n, out)
	}
}
