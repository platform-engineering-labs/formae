// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import (
	"fmt"
	"path/filepath"

	"resty.dev/v3"
)

func init() {
	Repo.RegisterReader("https", func(config *RepoConfig, data *RepoData) RepoReader {
		return &RepoReaderHttps{
			config: config,
			data:   data,
			client: resty.New(),
		}
	})
}

type RepoReaderHttps struct {
	config *RepoConfig
	data   *RepoData

	client *resty.Client
}

func (r *RepoReaderHttps) Data() *RepoData {
	return r.data
}

func (r *RepoReaderHttps) Fetch() error {
	request := r.client.R().
		SetResult(r.data).
		SetForceResponseContentType("application/json")

	if r.config.Username != "" && r.config.Password != "" {
		request.SetBasicAuth(r.config.Username, r.config.Password)
	}

	resp, err := request.Get(r.config.Uri.JoinPath(RepoFileName).String())
	if err != nil {
		return err
	}

	if resp.IsError() {
		switch resp.StatusCode() {
		case 404:
			return fmt.Errorf("not found: %s", resp.Request.URL)
		case 403:
			return fmt.Errorf("access denied: %s", resp.Request.URL)
		default:
			return fmt.Errorf("server error %d: %s", resp.StatusCode(), resp.Request.URL)
		}
	}

	return nil
}

func (r *RepoReaderHttps) FetchPackage(entry *PkgEntry, path string) error {
	fileName := Package.FileName(entry.Name, entry.Version, entry.OsArch)
	fetchUri := r.config.Uri.JoinPath(PackagePath, fileName).String()

	request := r.client.R().SetOutputFileName(filepath.Join(path, fileName))
	if r.config.Username != "" && r.config.Password != "" {
		request.SetBasicAuth(r.config.Username, r.config.Password)
	}

	resp, err := request.Get(fetchUri)
	if err != nil {
		return err
	}

	if resp.IsError() {
		switch resp.StatusCode() {
		case 404:
			return fmt.Errorf("not found: %s", resp.Request.URL)
		case 403:
			return fmt.Errorf("access denied: %s", resp.Request.URL)
		default:
			return fmt.Errorf("server error %d: %s", resp.StatusCode(), resp.Request.URL)
		}
	}

	return nil
}
