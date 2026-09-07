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

// AuthorizationDeniedError is returned when a forced-refresh retry still
// fails authentication: the CLI obtained a fresh credential from the auth
// plugin and the agent rejected it anyway. Unlike AuthenticationError, this
// is not a local configuration problem — the identity is valid but the
// agent has refused it — so the message does not point the user at their
// cli.auth configuration.
type AuthorizationDeniedError struct{}

func (e AuthorizationDeniedError) Error() string {
	return "the agent denied access for this installation; check with your org admin"
}
