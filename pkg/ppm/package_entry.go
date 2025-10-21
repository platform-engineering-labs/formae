// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import (
	"slices"

	"github.com/masterminds/semver"
)

type PkgEntries []*PkgEntry

type PkgEntry struct {
	Name    string
	OsArch  *OSArch
	Version *semver.Version
	Sha256  string
}

func (p *PkgEntry) Bucket() string {
	return p.Name + "@" + p.OsArch.String()
}

func (slice PkgEntries) Len() int {
	return len(slice)
}

func (slice PkgEntries) Less(i, j int) bool {
	if slice[i].Name < slice[j].Name {
		return true
	}
	if slice[i].Name > slice[j].Name {
		return false
	}

	if slice[i].OsArch.String() < slice[j].OsArch.String() {
		return true
	}
	if slice[i].OsArch.String() > slice[j].OsArch.String() {
		return false
	}

	return slice[i].Version.GreaterThan(slice[j].Version)
}

func (slice PkgEntries) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

func (slice PkgEntries) Names() []string {
	var names []string

	for _, entry := range slice {
		if !slices.Contains(names, entry.Name) {
			names = append(names, entry.Name)
		}
	}

	return names
}

func (slice PkgEntries) Platforms() []*OSArch {
	var platforms []*OSArch

	for _, entry := range slice {
		if !slices.ContainsFunc(platforms, func(p *OSArch) bool {
			return p.String() == entry.OsArch.String()
		}) {
			platforms = append(platforms, entry.OsArch)
		}
	}

	return platforms
}
