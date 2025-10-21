// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package arn

import "strings"

func IdFrom(arn string) string {
	frags := strings.Split(arn, "/")
	if len(frags) == 2 {
		return frags[len(frags)-1]
	}

	frags = strings.Split(arn, ":")
	return frags[len(frags)-1]
}
