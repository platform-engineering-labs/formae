// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"os"
)

func main() {
	err := RootCmd().Execute()
	if err != nil {
		os.Exit(1)
	}
}
