// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/masterminds/semver"
	"github.com/mholt/archives"
)

const Hist = "hist"

type Manager struct {
	repo          RepoReader
	osArch        *OSArch
	installPrefix string
}

func NewManager(config *Config, installPrefix string) (*Manager, error) {
	repo, err := Repo.GetReader(config.Repo)
	if err != nil {
		return nil, err
	}

	return &Manager{
		repo:          repo,
		osArch:        CurrentOsArch(),
		installPrefix: installPrefix,
	}, nil
}

func (m *Manager) Clean() {
	histPath := filepath.Join(m.installPrefix, Hist)
	os.RemoveAll(histPath + string(os.PathSeparator))
	os.MkdirAll(histPath, 0755)
}

func (m *Manager) HasEntry(name string, version *semver.Version) (*PkgEntry, error) {
	err := m.repo.Fetch()
	if err != nil {
		return nil, err
	}

	entry := m.repo.Data().GetEntry(name, version, m.osArch)
	if entry != nil {
		return entry, nil
	}

	return nil, fmt.Errorf("entry not found")
}

func (m *Manager) HasUpdate(name string, version *semver.Version) (*PkgEntry, error) {
	list, err := m.AvailableVersions(name, false)
	if err != nil {
		return nil, err
	}

	if len(list) == 0 {
		return nil, nil
	}

	if list[0].Version.GreaterThan(version) {
		return list[0], nil
	}

	return nil, nil
}

func (m *Manager) Install(entry *PkgEntry) error {
	err := os.MkdirAll(m.installPrefix, 0755)
	if err != nil {
		return err
	}

	tmpPath, err := os.MkdirTemp("", "ppm-")
	if err != nil {
		return err
	}

	err = m.repo.FetchPackage(entry, tmpPath)
	if err != nil {
		return err
	}

	pkg, err := os.Open(filepath.Join(tmpPath, Package.FileName(entry.Name, entry.Version, m.osArch)))
	if err != nil {
		return err
	}
	defer pkg.Close()
	defer os.Remove(pkg.Name())

	err = Package.Verify(pkg, entry.Sha256)
	if err != nil {
		return err
	}
	pkg.Seek(0, io.SeekStart)

	os.Chmod(filepath.Join(m.installPrefix, Hist), 0755)
	os.MkdirAll(filepath.Join(m.installPrefix, Hist), 0755)

	isInstalled, err := exists(filepath.Join(m.installPrefix, entry.Name))
	if err != nil {
		return err
	}

	if isInstalled {
		err = os.Rename(filepath.Join(m.installPrefix, entry.Name), filepath.Join(m.installPrefix, "hist", entry.Name+"-"+fmt.Sprintf("%d", time.Now().Unix())))
		if err != nil {
			return err
		}
	}

	format := archives.CompressedArchive{
		Compression: archives.Gz{},
		Extraction:  archives.Tar{},
	}

	err = format.Extract(context.Background(), pkg, func(ctx context.Context, f archives.FileInfo) error {
		dstPath := filepath.Join(m.installPrefix, f.NameInArchive)

		if f.IsDir() {
			err := os.MkdirAll(dstPath, f.Mode())

			return err
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
	if err != nil {
		return err
	}

	return nil
}

func (m *Manager) AvailableVersions(name string, prerelease bool) ([]*PkgEntry, error) {
	err := m.repo.Fetch()
	if err != nil {
		return nil, err
	}

	versions := m.repo.Data().List(name, m.osArch)
	var available []*PkgEntry
	for i, pkg := range m.repo.Data().List(name, m.osArch) {
		if prerelease == false && pkg.Version.Prerelease() != "" {
			continue
		}

		available = append(available, versions[i])
	}
	if len(available) != 0 {
		return available, nil
	}

	return nil, nil
}

func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}
