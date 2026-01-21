// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import (
	"fmt"
)

func init() {
	Repo.RegisterWriter("https", func(config *RepoConfig, data *RepoData) RepoWriter {
		return &RepoWriterHttps{
			config: config,
			data:   data,
		}
	})
}

type RepoWriterHttps struct {
	config *RepoConfig

	data *RepoData
}

func (r *RepoWriterHttps) Init() error {
	return fmt.Errorf("not yet supported for scheme: https://")
}

func (r *RepoWriterHttps) Publish(prune int, paths ...string) error {
	return fmt.Errorf("not yet supported for scheme: https://")
}

func (r *RepoWriterHttps) Rebuild() error {
	return fmt.Errorf("not yet supported for scheme: https://")
}
