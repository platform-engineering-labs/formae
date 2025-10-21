// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"ppm/pkg/s3x"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/platform-engineering-labs/formae/pkg/ppm"
)

func init() {
	ppm.Repo.RegisterReader("s3", func(uri *url.URL, data *ppm.RepoData) ppm.RepoReader {
		return &ReaderS3{
			uri:  uri,
			data: data,
		}
	})
}

type ReaderS3 struct {
	uri  *url.URL
	data *ppm.RepoData
}

func (r *ReaderS3) Data() *ppm.RepoData {
	return r.data
}

func (r *ReaderS3) Fetch() error {
	s3Client, err := s3x.GetS3Client()
	if err != nil {
		return err
	}

	result, err := s3Client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(r.uri.Host),
		Key:    aws.String(s3x.BucketPath(r.uri.Path, ppm.RepoFileName)),
	})
	if err != nil {
		var noKey *types.NoSuchKey
		if !errors.As(err, &noKey) {
			return err
		}
	}

	if result.Body != nil {
		jsonBytes, err := io.ReadAll(result.Body)
		if err != nil {
			return err
		}

		err = json.Unmarshal(jsonBytes, r.data)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *ReaderS3) FetchPackage(entry *ppm.PkgEntry, path string) error {
	s3Client, err := s3x.GetS3Client()
	if err != nil {
		return err
	}

	downloader := manager.NewDownloader(s3Client, func(d *manager.Downloader) {
		d.PartSize = 10 * 1024 * 1024
	})

	file, err := os.Open(path)
	if err != nil {
		return err
	}

	_, err = downloader.Download(context.Background(), file, &s3.GetObjectInput{
		Bucket: aws.String(r.uri.Host),
		Key:    aws.String(s3x.BucketPath(r.uri.Path, ppm.PackagePath, ppm.Package.FileName(entry.Name, entry.Version, entry.OsArch))),
	})
	if err != nil {
		return err
	}

	return nil
}
