// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cmd

import "fmt"

// FlagError indicates an error processing command-line flags or other arguments.
// When a FlagError is returned, the CLI will display the command's usage information
// in addition to the error message, helping users understand correct command syntax.
//
// Use FlagError for validation errors in validate*Options functions.
// Do not use FlagError for runtime errors (API errors, network errors, etc.)
// as those should not trigger usage display.
type FlagError struct {
	Err error
}

func (e *FlagError) Error() string {
	return e.Err.Error()
}

func (e *FlagError) Unwrap() error {
	return e.Err
}

// FlagErrorf creates a new FlagError with a formatted message.
func FlagErrorf(format string, args ...interface{}) error {
	return &FlagError{Err: fmt.Errorf(format, args...)}
}

// FlagErrorWrap wraps an existing error as a FlagError.
func FlagErrorWrap(err error) error {
	if err == nil {
		return nil
	}
	return &FlagError{Err: err}
}
