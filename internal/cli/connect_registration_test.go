// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cli

import "testing"

// The connect command is registered on the root beside login/logout, in the
// Auth group the usage template renders it under.
func TestConnectIsRegisteredAsAnAuthCommand(t *testing.T) {
	for _, c := range rootCmd.Commands() {
		if c.Name() == "connect" {
			if c.Annotations["type"] != "Auth" {
				t.Fatalf("connect is registered with type %q, want Auth", c.Annotations["type"])
			}
			return
		}
	}
	t.Fatal("connect is not registered on the root command")
}
