// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package repo

func Map[T any, M any](a []T, f func(T) M) []M {
	n := make([]M, len(a))
	for i, e := range a {
		n[i] = f(e)
	}
	return n
}
