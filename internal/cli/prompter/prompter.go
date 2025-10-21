// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package prompter

import (
	"fmt"
	"syscall"

	"github.com/platform-engineering-labs/formae/internal/cli/display"
)

type Prompter interface {
	Confirm(prompt string, defaultValue bool) (bool, error)
	PressEnterToContinue() error
}

type BasicPrompter struct{}

func NewBasicPrompter() *BasicPrompter {
	return &BasicPrompter{}
}

func (p *BasicPrompter) Confirm(prompt string, defaultValue bool) bool {
	fmt.Printf("%s (Y): ", prompt)
	var response string
	_, err := fmt.Scanln(&response)
	return err == nil && response == "Y"
}

func (p *BasicPrompter) PressEnterToContinue(prompt string) error {
	if prompt != "" {
		fmt.Print(prompt)
	}
	fmt.Print(display.Grey("\n\nPress Enter to continue..."))
	b := make([]byte, 1)
	_, err := syscall.Read(int(syscall.Stdin), b)
	if err != nil {
		return err
	}
	fmt.Println() // Add a newline after the user presses a key

	return nil
}
