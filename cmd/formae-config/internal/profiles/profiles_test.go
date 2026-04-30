// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package profiles_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/platform-engineering-labs/formae/cmd/formae-config/internal/profiles"
)

func TestValidateName(t *testing.T) {
	good := []string{"default", "local-dev", "load_test", "prod", "a", "AB-12_cd"}
	for _, n := range good {
		if err := profiles.ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{"", "with space", "has/slash", ".dotfile", "name.pkl", "with$"}
	for _, n := range bad {
		if err := profiles.ValidateName(n); !errors.Is(err, profiles.ErrInvalidName) {
			t.Errorf("ValidateName(%q) = %v, want ErrInvalidName", n, err)
		}
	}
}

func TestStorePaths(t *testing.T) {
	s := profiles.New("/root")
	if got, want := s.ConfigPath(), filepath.Join("/root", "formae.conf.pkl"); got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}
	if got, want := s.ProfilePath("local-dev"), filepath.Join("/root", "profiles", "local-dev.pkl"); got != want {
		t.Errorf("ProfilePath = %q, want %q", got, want)
	}
}
