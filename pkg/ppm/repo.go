// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import (
	"fmt"
	"net/url"
)

var repoReaderRegistry = make(map[string]func(uri *url.URL, data *RepoData) RepoReader)
var repoWriterRegistry = make(map[string]func(uri *url.URL, data *RepoData) RepoWriter)

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
	uri, err := url.Parse(config.Uri)
	if err != nil {
		return nil, err
	}

	if repoReaderRegistry[uri.Scheme] != nil {
		return repoReaderRegistry[uri.Scheme](uri, &RepoData{}), nil
	}

	return nil, fmt.Errorf("unsupported repository scheme: %s", uri.Scheme)
}

func (repo) GetWriter(config *RepoConfig) (RepoWriter, error) {
	uri, err := url.Parse(config.Uri)
	if err != nil {
		return nil, err
	}

	if repoWriterRegistry[uri.Scheme] != nil {
		return repoWriterRegistry[uri.Scheme](uri, &RepoData{}), nil
	}

	return nil, fmt.Errorf("unsupported repository scheme: %s", uri.Scheme)
}

func (repo) RegisterReader(scheme string, creator func(uri *url.URL, data *RepoData) RepoReader) {
	repoReaderRegistry[scheme] = creator
}

func (repo) RegisterWriter(scheme string, creator func(uri *url.URL, data *RepoData) RepoWriter) {
	repoWriterRegistry[scheme] = creator
}
