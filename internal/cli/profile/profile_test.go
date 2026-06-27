// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package profile_test

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/profile"
)

func TestProfileCmd_HasExpectedSubcommands(t *testing.T) {
	want := map[string]bool{
		"list": false, "current": false, "use": false, "save": false,
		"create": false, "edit": false, "delete": false, "diff": false,
	}
	for _, c := range profile.ProfileCmd().Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("ProfileCmd() missing subcommand %q", name)
		}
	}
}
