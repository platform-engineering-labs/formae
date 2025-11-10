// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package nag

import (
	"fmt"
	"math/rand"

	"github.com/platform-engineering-labs/formae/internal/cli/display"
)

func MaybePrintNags(nags []string) {
	randomNumber := rand.Intn(101) // Random number between 0 and 100
	if randomNumber%2 == 0 {
		if len(nags) > 0 {
			fmt.Printf("\n")
			for _, nag := range nags {
				fmt.Printf("%s %s\n", display.Gold("Tip:"), nag)
			}
		}
	}
}
