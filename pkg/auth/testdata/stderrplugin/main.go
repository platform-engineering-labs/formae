// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Command stderrplugin is a minimal auth plugin binary built by
// client_test.go. It writes a marker line to its own stderr while handling
// Init, so the test can check whether that line reaches the host process.
package main

import (
	"fmt"
	"os"

	"github.com/platform-engineering-labs/formae/pkg/auth"
)

// stderrMarker must match the constant of the same name in client_test.go.
const stderrMarker = "stderrplugin: init reached"

type plugin struct {
	auth.UnimplementedAuthPlugin
}

func (plugin) Init(req *auth.InitRequest, resp *auth.InitResponse) error {
	fmt.Fprintln(os.Stderr, stderrMarker)
	return nil
}

func main() {
	auth.Run(plugin{})
}
