// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import (
	"encoding/json"
	"slices"
	"sort"

	"github.com/masterminds/semver"
)

// TODO(discount-elf) Add repository signing, current impl is good enough for now

const PruneCount = 10
const PackagePath = "pkgs"
const RepoFileName = "repo.json"

type RepoData struct {
	index map[string][]*PkgEntry

	Version  int
	Packages []*PkgEntry
}

func (r *RepoData) Json() ([]byte, error) {
	sort.Sort(PkgEntries(r.Packages))
	return json.Marshal(r)
}

func (r *RepoData) GetEntry(name string, version *semver.Version, osArch *OSArch) *PkgEntry {
	for _, pkg := range r.Packages {
		if pkg.Version.Equal(version) && osArch.String() == pkg.OsArch.String() && pkg.Name == name {
			return pkg
		}
	}

	return nil
}

func (r *RepoData) List(name string, osArch *OSArch) []*PkgEntry {
	list := r.Packages

	if name != "" {
		var filteredList []*PkgEntry

		for _, pkg := range list {
			if pkg.Name == name {
				filteredList = append(filteredList, pkg)
			}
		}

		list = filteredList
	}

	if osArch != nil {
		var filteredList []*PkgEntry

		for _, pkg := range list {
			if pkg.OsArch.String() == osArch.String() {
				filteredList = append(filteredList, pkg)
			}
		}

		list = filteredList
	}

	return list
}

func (r *RepoData) Update(prune int, pkgPaths []string) ([]string, error) {
	for _, filePath := range pkgPaths {
		entry, err := Package.EntryFromFilePath(filePath)
		if err != nil {
			return nil, err
		}

		r.addPkg(entry)
	}

	return r.prune(prune), nil
}

func (r *RepoData) prune(count int) []string {
	var prune []string

	sort.Sort(PkgEntries(r.Packages))
	r.buildIndex()

	for bucket := range r.index {
		for len(r.index[bucket]) > count {
			var p *PkgEntry

			p, r.index[bucket] = r.index[bucket][len(r.index[bucket])-1], r.index[bucket][:len(r.index[bucket])-1]
			prune = append(prune, Package.FileName(p.Name, p.Version, p.OsArch))
		}
	}

	r.Packages = []*PkgEntry{}
	for bucket := range r.index {
		r.Packages = append(r.Packages, r.index[bucket]...)
	}

	return prune
}

func (r *RepoData) addPkg(entry *PkgEntry) {
	if slices.ContainsFunc(r.Packages, func(p *PkgEntry) bool {
		return p.Version.Equal(entry.Version) && p.OsArch.String() == entry.OsArch.String()
	}) {
		for i, pkg := range r.Packages {
			if entry.Version.Equal(pkg.Version) && entry.OsArch.String() == pkg.OsArch.String() {
				r.Packages[i].Sha256 = entry.Sha256
			}
		}
	} else {
		r.Packages = append(r.Packages, entry)
	}
}

func (r *RepoData) buildIndex() {
	r.index = make(map[string][]*PkgEntry)

	for _, pkg := range r.Packages {
		r.index[pkg.Bucket()] = append(r.index[pkg.Bucket()], pkg)
	}
}
