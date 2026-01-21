// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import (
	"fmt"
)

var repoReaderRegistry = make(map[string]func(config *RepoConfig, data *RepoData) RepoReader)
var repoWriterRegistry = make(map[string]func(config *RepoConfig, data *RepoData) RepoWriter)

var Repo = repo{}

type repo struct{}

type RepoReader interface {
	Data() *RepoData
	Fetch() error
	FetchPackage(entry *PkgEntry, path string) error
}

type RepoWriter interface {
	Init() error
	Rebuild() error
	Publish(prune int, pkgPaths ...string) error
}

func (repo) GetReader(config *RepoConfig) (RepoReader, error) {
	if repoReaderRegistry[config.Uri.Scheme] != nil {
		return repoReaderRegistry[config.Uri.Scheme](config, &RepoData{}), nil
	}

	return nil, fmt.Errorf("unsupported repository scheme: %s", config.Uri.Scheme)
}

func (repo) GetWriter(config *RepoConfig) (RepoWriter, error) {
	if repoWriterRegistry[config.Uri.Scheme] != nil {
		return repoWriterRegistry[config.Uri.Scheme](config, &RepoData{}), nil
	}

	return nil, fmt.Errorf("unsupported repository scheme: %s", config.Uri.Scheme)
}

func (repo) RegisterReader(scheme string, creator func(config *RepoConfig, data *RepoData) RepoReader) {
	repoReaderRegistry[scheme] = creator
}

func (repo) RegisterWriter(scheme string, creator func(config *RepoConfig, data *RepoData) RepoWriter) {
	repoWriterRegistry[scheme] = creator
}
