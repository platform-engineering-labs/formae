// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ptr

func Of[T any](v T) *T {
	return &v
}
