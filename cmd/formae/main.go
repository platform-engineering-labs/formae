// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"github.com/platform-engineering-labs/formae/internal/cli"
	"github.com/platform-engineering-labs/formae/internal/logging"
)

func main() {
	logging.SetupInitialLogging()
	cli.Start()
}
