// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

type AppNotFoundError struct{}

func (e AppNotFoundError) Error() string {
	return "app not found in context"
}
