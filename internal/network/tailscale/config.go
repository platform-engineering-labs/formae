// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package tailscale

// Config holds the Tailscale network plugin configuration.
type Config struct {
	Hostname      string
	Tls           bool
	AuthKey       string
	AdvertiseTags []string
}

func valueOrDefault(value string, def string) string {
	if value == "" {
		return def
	}

	return value
}
