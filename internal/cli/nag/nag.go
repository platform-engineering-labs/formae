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

	// Constant deprecation warning for scanTargets
	fmt.Printf("\n%s %s\n", display.Gold("DEPRECATION:"), "The 'scanTargets' configuration option is deprecated and will be removed in the next release.")
	fmt.Printf("%s\n", "Remove 'scanTargets' from ~/.config/formae/formae.conf.pkl and set 'discoverable' on targets in your Forma files.")
	fmt.Printf("%s %s\n", display.Grey("See:"), display.LightBlue("https://docs.formae.io/en/latest/core-concepts/target/"))
}
