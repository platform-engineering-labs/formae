// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/masterminds/semver"
	"github.com/mholt/archives"
)

var Package = pkg{}

type pkg struct{}

func (pkg) Build(name string, version *semver.Version, osArch *OSArch, srcPath string, outPath string) (string, error) {
	srcPath, err := filepath.Abs(srcPath)
	if err != nil {
		return "", err
	}

	outPath, err = filepath.Abs(outPath)
	if err != nil {
		return "", err
	}

	_ = os.MkdirAll(outPath, 0750)

	contentMap := map[string]string{
		srcPath: "",
	}

	files, err := archives.FilesFromDisk(context.Background(), nil, contentMap)
	if err != nil {
		return "", err
	}

	pkgName := Package.FileName(name, version, osArch)

	out, err := os.Create(filepath.Join(outPath, pkgName))
	if err != nil {
		return "", err
	}
	defer out.Close()

	format := archives.CompressedArchive{
		Compression: archives.Gz{},
		Archival:    archives.Tar{},
	}

	err = format.Archive(context.Background(), out, files)
	if err != nil {
		return "", err
	}

	return pkgName, nil
}

func (pkg) Verify(pkg *os.File, checkSum string) error {
	hash := sha256.New()
	if _, err := io.Copy(hash, pkg); err != nil {
		return err
	}

	if hex.EncodeToString(hash.Sum(nil)) != checkSum {
		return fmt.Errorf("package failed checksum validation")
	}
	return nil
}

func (pkg) FileName(name string, version *semver.Version, osArch *OSArch) string {
	return fmt.Sprintf("%s@%s_%s.tgz", name, version.String(), osArch.String())
}

func (pkg) EntryFromFilePath(filePath string) (entry *PkgEntry, err error) {
	entry = &PkgEntry{}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, err
	}
	entry.Sha256 = hex.EncodeToString(hash.Sum(nil))

	fileName := filepath.Base(filePath)
	parts := strings.Split(fileName, "@")
	entry.Name = parts[0]

	versionParts := strings.Split(parts[1], "_")
	entry.Version, err = semver.NewVersion(versionParts[0])
	if err != nil {
		return nil, err
	}

	oaParts := strings.Split(versionParts[1], ".")
	oaComps := strings.Split(oaParts[0], "-")
	entry.OsArch = &OSArch{oaComps[0], oaComps[1]}

	return entry, nil
}
