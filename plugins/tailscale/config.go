// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

type Config struct {
	Hostname      string
	Tls           bool
	AuthKey       string
	AdvertiseTags []string
}

func ValueOrDefault(value string, def string) string {
	if value == "" {
		return def
	}

	return value
}
