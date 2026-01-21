// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import "net/url"

type RepoConfig struct {
	Uri      *url.URL
	Username string
	Password string
}

type Config struct {
	Repo *RepoConfig
}
