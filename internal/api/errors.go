// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

import "errors"

type AppNotFoundError struct{}

func (e AppNotFoundError) Error() string {
	return "app not found in context"
}

// AuthenticationError is returned when the agent rejects the CLI's credentials.
type AuthenticationError struct{}

func (e AuthenticationError) Error() string {
	return "authentication failed — check your cli.auth configuration"
}

// ErrEndpointNotFound is returned by client methods when the agent does not
// support the requested endpoint (e.g. an older agent that predates the
// summary or by-ksuid routes). Callers can test for this with errors.Is to
// decide whether to fall back to an older API call.
var ErrEndpointNotFound = errors.New("endpoint not found on agent")
