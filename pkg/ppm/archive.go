// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/mholt/archives"
)

// extractArchive extracts a tar.gz archive from r into destDir.
func extractArchive(r io.ReadSeeker, destDir string) error {
	format := archives.CompressedArchive{
		Compression: archives.Gz{},
		Extraction:  archives.Tar{},
	}

	return format.Extract(context.Background(), r, func(_ context.Context, f archives.FileInfo) error {
		dstPath := filepath.Join(destDir, f.NameInArchive)

		if f.IsDir() {
			return os.MkdirAll(dstPath, f.Mode())
		}

		dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		src, err := f.Open()
		if err != nil {
			return err
		}

		_, err = io.Copy(dst, src)
		return err
	})
}

// extractArchiveFromFile opens a .tgz file and extracts it to destDir.
func extractArchiveFromFile(tgzPath, destDir string) error {
	f, err := os.Open(tgzPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	return extractArchive(f, destDir)
}
