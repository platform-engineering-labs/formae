// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import (
	"fmt"
	"net/url"
	"path/filepath"

	"resty.dev/v3"
)

func init() {
	Repo.RegisterReader("https", func(uri *url.URL, data *RepoData) RepoReader {
		return &RepoReaderHttps{
			uri:    uri,
			data:   data,
			client: resty.New(),
		}
	})
}

type RepoReaderHttps struct {
	uri  *url.URL
	data *RepoData

	client *resty.Client
}

func (r *RepoReaderHttps) Data() *RepoData {
	return r.data
}

func (r *RepoReaderHttps) Fetch() error {
	resp, err := r.client.R().
		SetResult(r.data).
		SetForceResponseContentType("application/json").
		Get(r.uri.JoinPath(RepoFileName).String())
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
	fetchUri := r.uri.JoinPath(PackagePath, fileName).String()

	resp, err := r.client.R().
		SetOutputFileName(filepath.Join(path, fileName)).
		Get(fetchUri)
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
