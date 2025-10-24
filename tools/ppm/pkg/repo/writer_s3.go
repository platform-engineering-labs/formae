// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"ppm/pkg/s3x"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	s3setlock "github.com/mashiike/s3-setlock"
	"github.com/platform-engineering-labs/formae/pkg/ppm"
)

func init() {
	ppm.Repo.RegisterWriter("s3", func(uri *url.URL, data *ppm.RepoData) ppm.RepoWriter {
		return &WriterS3{
			uri:  uri,
			data: data,
		}
	})
}

type WriterS3 struct {
	uri  *url.URL
	data *ppm.RepoData
}

func (w *WriterS3) Init() error {
	s3Client, err := s3x.GetS3Client()
	if err != nil {
		return err
	}

	result, err := s3Client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
		Bucket:  &w.uri.Host,
		MaxKeys: aws.Int32(200),
		Prefix:  aws.String(strings.TrimPrefix(w.uri.Path, "/") + "/"),
	})
	if err != nil {
		return err
	}

	if len(result.Contents) == 0 {
		return nil
	}

	_, err = s3Client.DeleteObjects(context.Background(), &s3.DeleteObjectsInput{
		Bucket: &w.uri.Host,
		Delete: &types.Delete{
			Objects: Map(result.Contents, func(item types.Object) types.ObjectIdentifier {
				return types.ObjectIdentifier{
					Key: item.Key,
				}
			}),
			Quiet: aws.Bool(true),
		},
	})
	if err != nil {
		return err
	}

	return nil
}

func (w *WriterS3) Publish(prune int, pkgPaths ...string) error {
	s3Client, err := s3x.GetS3Client()
	if err != nil {
		return err
	}

	lockUri, err := url.JoinPath(w.uri.String(), "ppm.lock")
	if err != nil {
		return err
	}

	lock, err := s3setlock.New(
		lockUri,
		s3setlock.WithClient(s3Client),
		s3setlock.WithContext(context.Background()),
		s3setlock.WithDelay(true),
		s3setlock.WithLeaseDuration(30*time.Second),
	)
	if err != nil {
		return err
	}

	_, err = lock.LockWithError(context.Background())
	if err != nil {
		return err
	}
	defer lock.Unlock()

	result, err := s3Client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: &w.uri.Host,
		Key:    aws.String(s3x.BucketPath(w.uri.Path, ppm.RepoFileName)),
	})
	if err != nil {
		var noKey *types.NoSuchKey
		if !errors.As(err, &noKey) {
			return err
		}
	}

	if result != nil {
		jsonBytes, err := io.ReadAll(result.Body)
		if err != nil {
			return err
		}

		err = json.Unmarshal(jsonBytes, w.data)
		if err != nil {
			return err
		}
	}

	remove, err := w.data.Update(prune, pkgPaths)
	if err != nil {
		return err
	}

	uploader := manager.NewUploader(s3Client, func(u *manager.Uploader) {
		u.PartSize = 10 * 1024 * 1024
	})

	for _, pkgPath := range pkgPaths {
		fname := filepath.Base(pkgPath)

		if slices.Contains(remove, fname) {
			continue
		}

		uploadFile, err := os.Open(pkgPath)
		if err != nil {
			return err
		}

		_, err = uploader.Upload(context.Background(), &s3.PutObjectInput{
			Bucket: &w.uri.Host,
			Key:    aws.String(s3x.BucketPath(w.uri.Path, ppm.PackagePath, fname)),
			Body:   uploadFile,
		})
		uploadFile.Close()

		if err != nil {
			return err
		} else {
			err = s3.NewObjectExistsWaiter(s3Client).Wait(
				context.Background(),
				&s3.HeadObjectInput{
					Bucket: &w.uri.Host,
					Key:    aws.String(s3x.BucketPath(w.uri.Path, ppm.PackagePath, fname))},
				time.Minute)
			if err != nil {
				return err
			}
		}
	}

	for _, pkgFile := range remove {
		_, err := s3Client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
			Bucket: &w.uri.Host,
			Key:    aws.String(s3x.BucketPath(w.uri.Path, ppm.PackagePath, pkgFile)),
		})
		if err != nil {
			return err
		}
	}

	body, err := w.data.Json()
	if err != nil {
		return err

	}

	_, err = s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: &w.uri.Host,
		Key:    aws.String(s3x.BucketPath(w.uri.Path, ppm.RepoFileName)),
		Body:   bytes.NewReader(body),
	})
	if err != nil {
		return err
	}

	return nil
}

func (w *WriterS3) Rebuild() error {
	return fmt.Errorf("not yet supported for scheme: s3://")
}
