// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateApplyModeCLI(t *testing.T) {
	for _, tc := range []struct{ name, setting, want string }{
		{"default", "", "patch"}, {"patch", "patch", "patch"}, {"reconcile", "reconcile", "reconcile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FORMAE_TEST_UPDATE_MODE", tc.setting)
			argsFile := filepath.Join(t.TempDir(), "args")
			h := &TestHarness{t: t, configFile: "/tmp/test-config.pkl", formaeBinary: stubFormaeBinary(t, argsFile, `printf '%s' '{"CommandId":"update-1"}'`)}
			got, err := applyUpdate(h, "/tmp/update.pkl")
			if err != nil || got != "update-1" {
				t.Fatalf("applyUpdate=(%q,%v)", got, err)
			}
			raw, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), "--mode\n"+tc.want+"\n") {
				t.Fatalf("wrong CLI mode: %s", raw)
			}
		})
	}
}

func TestUpdateApplyModeInvalidNeverCallsCLI(t *testing.T) {
	for _, mode := range []string{"typo", "RECONCILE", " patch ", "destroy"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FORMAE_TEST_UPDATE_MODE", mode)
			argsFile := filepath.Join(t.TempDir(), "args")
			h := &TestHarness{t: t, formaeBinary: stubFormaeBinary(t, argsFile, "exit 0")}
			_, err := applyUpdate(h, "/tmp/update.pkl")
			if err == nil || !strings.Contains(err.Error(), "FORMAE_TEST_UPDATE_MODE") {
				t.Fatalf("expected setting error, got %v", err)
			}
			if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
				t.Fatalf("CLI invoked for invalid setting: %v", err)
			}
		})
	}
}

func TestInvalidUpdateModeBeforeSetup(t *testing.T) {
	if os.Getenv("TEST_INVALID_UPDATE_MODE_CHILD") == "1" {
		RunCRUDTests(t)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestInvalidUpdateModeBeforeSetup$", "-test.v")
	// An empty directory would fail fixture discovery if validation ran too late.
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "TEST_INVALID_UPDATE_MODE_CHILD=1", "FORMAE_TEST_UPDATE_MODE=invalid", "FORMAE_TEST_TYPE=crud")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("invalid configuration succeeded: %s", output)
	}
	if !strings.Contains(string(output), "FORMAE_TEST_UPDATE_MODE") || strings.Contains(string(output), "failed to discover") {
		t.Fatalf("validation did not precede setup: %s", output)
	}
}
