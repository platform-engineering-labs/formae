// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

type AppNotFoundError struct{}

func (e AppNotFoundError) Error() string {
	return "app not found in context"
}

// AuthenticationError is returned when the agent rejects the CLI's credentials.
type AuthenticationError struct{}

func (e AuthenticationError) Error() string {
	return "authentication failed — check your cli.auth configuration"
}
