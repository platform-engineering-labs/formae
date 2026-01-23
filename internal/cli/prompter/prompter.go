// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package prompter

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/platform-engineering-labs/formae/internal/cli/display"
)

type Prompter interface {
	Confirm(prompt string, defaultValue bool) (bool, error)
	PressEnterToContinue() error
	PromptString(prompt string) (string, error)
	PromptStringWithDefault(prompt string, defaultValue string) (string, error)
	PromptChoice(prompt string, options []string, defaultIndex int) (string, error)
}

type BasicPrompter struct {
	reader *bufio.Reader
}

func NewBasicPrompter() *BasicPrompter {
	return &BasicPrompter{
		reader: bufio.NewReader(os.Stdin),
	}
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

// PromptString prompts for required string input, looping until non-empty input is provided.
func (p *BasicPrompter) PromptString(prompt string) (string, error) {
	for {
		fmt.Printf("%s: ", prompt)
		input, err := p.reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		input = strings.TrimSpace(input)
		if input != "" {
			return input, nil
		}
		fmt.Printf("  %s is required\n", prompt)
	}
}

// PromptStringWithDefault prompts for string input with a default value.
// Returns the default if input is empty.
func (p *BasicPrompter) PromptStringWithDefault(prompt string, defaultValue string) (string, error) {
	fmt.Printf("%s %s: ", prompt, display.Grey("["+defaultValue+"]"))
	input, err := p.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultValue, nil
	}
	return input, nil
}

// PromptChoice prompts the user to select from a list of options.
// Options are displayed as a numbered list. The defaultIndex specifies which option
// is selected if the user presses Enter without input (0-based).
func (p *BasicPrompter) PromptChoice(prompt string, options []string, defaultIndex int) (string, error) {
	fmt.Printf("%s:\n", prompt)
	for i, opt := range options {
		marker := "  "
		if i == defaultIndex {
			marker = display.Green("> ")
		}
		fmt.Printf("%s%d. %s\n", marker, i+1, opt)
	}
	fmt.Printf("Enter choice %s: ", display.Grey(fmt.Sprintf("[%d]", defaultIndex+1)))

	input, err := p.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	input = strings.TrimSpace(input)

	if input == "" {
		return options[defaultIndex], nil
	}

	var choice int
	if _, err := fmt.Sscanf(input, "%d", &choice); err != nil || choice < 1 || choice > len(options) {
		fmt.Printf("  Invalid choice, using default\n")
		return options[defaultIndex], nil
	}

	return options[choice-1], nil
}
