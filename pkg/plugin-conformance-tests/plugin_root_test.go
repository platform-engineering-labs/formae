// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package conformance

import (
	"os"
	"path/filepath"
	"testing"
)

// An explicit isolated root must determine which actual manifests are
// discovered; falling back to developer-home plugins violates isolation.
func TestPluginDiscoveryUsesIsolatedRoot(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "isolation-probe", "v0.0.1")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "isolation-probe"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "formae-plugin.pkl"), []byte(`name = "isolation-probe"
version = "0.0.1"
namespace = "ISOLATION"
minFormaeVersion = "0.89.0"
output { renderer = new JsonRenderer {} }
`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORMAE_TEST_PLUGIN_DIR", root)
	h := &TestHarness{t: t}
	if err := h.setupPluginDiscovery(); err != nil {
		t.Fatal(err)
	}
	if len(h.externalResourcePlugins) != 1 || h.externalResourcePlugins[0].Namespace != "ISOLATION" {
		t.Fatalf("isolated root not honored: found %d plugins", len(h.externalResourcePlugins))
	}
}
