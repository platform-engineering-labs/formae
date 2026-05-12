// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"testing"

	"github.com/platform-engineering-labs/orbital/opm/records"
	"github.com/platform-engineering-labs/orbital/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func newVersion(t *testing.T, s string) *ops.Version {
	t.Helper()
	v := &ops.Version{}
	require.NoError(t, v.Parse(s))
	return v
}

func newPackage(t *testing.T, name, version string, metadata map[string]map[string]string) *records.Package {
	t.Helper()
	return &records.Package{
		Header: &ops.Header{
			Name:     name,
			Version:  newVersion(t, version),
			Metadata: metadata,
		},
	}
}

func TestIsPluginPackage(t *testing.T) {
	t.Run("nil package", func(t *testing.T) {
		assert.False(t, isPluginPackage(nil))
	})

	t.Run("plugin kind", func(t *testing.T) {
		pkg := newPackage(t, "p", "1.0.0", map[string]map[string]string{
			"display": {"kind": "plugin"},
		})
		assert.True(t, isPluginPackage(pkg))
	})

	t.Run("metapackage kind", func(t *testing.T) {
		pkg := newPackage(t, "p", "1.0.0", map[string]map[string]string{
			"display": {"kind": "metapackage"},
		})
		assert.True(t, isPluginPackage(pkg))
	})

	t.Run("missing display block", func(t *testing.T) {
		pkg := newPackage(t, "p", "1.0.0", nil)
		assert.False(t, isPluginPackage(pkg))
	})

	t.Run("display block without kind", func(t *testing.T) {
		pkg := newPackage(t, "p", "1.0.0", map[string]map[string]string{
			"display": {"category": "cloud"},
		})
		assert.False(t, isPluginPackage(pkg))
	})
}

func TestInstalledVersionOf(t *testing.T) {
	t.Run("returns version of the Installed package", func(t *testing.T) {
		a := newPackage(t, "p", "1.0.0", nil)
		b := newPackage(t, "p", "1.1.0", nil)
		b.Installed = true
		assert.Equal(t, "1.1.0", installedVersionOf([]*records.Package{a, b}))
	})

	t.Run("returns empty when none installed", func(t *testing.T) {
		a := newPackage(t, "p", "1.0.0", nil)
		b := newPackage(t, "p", "1.1.0", nil)
		assert.Equal(t, "", installedVersionOf([]*records.Package{a, b}))
	})
}

func TestUniqueVersions(t *testing.T) {
	t.Run("dedupes preserving input order", func(t *testing.T) {
		pkgs := []*records.Package{
			newPackage(t, "p", "1.2.0", nil),
			newPackage(t, "p", "1.1.0", nil),
			newPackage(t, "p", "1.2.0", nil), // duplicate
		}
		assert.Equal(t, []string{"1.2.0", "1.1.0"}, uniqueVersions(pkgs))
	})

	t.Run("skips packages with nil Header or Version", func(t *testing.T) {
		pkgs := []*records.Package{
			{Header: nil},
			newPackage(t, "p", "1.0.0", nil),
		}
		assert.Equal(t, []string{"1.0.0"}, uniqueVersions(pkgs))
	})
}

func TestMatchesPluginFilter(t *testing.T) {
	p := apimodel.Plugin{
		Name:        "aws",
		Type:        "resource",
		Category:    "cloud",
		Summary:     "AWS provider",
		Description: "Manages AWS resources",
	}

	t.Run("no filters: always matches", func(t *testing.T) {
		assert.True(t, matchesPluginFilter(p, "", "", ""))
	})

	t.Run("category mismatch", func(t *testing.T) {
		assert.False(t, matchesPluginFilter(p, "", "auth", ""))
	})

	t.Run("type mismatch", func(t *testing.T) {
		assert.False(t, matchesPluginFilter(p, "", "", "auth"))
	})

	t.Run("query matches name", func(t *testing.T) {
		assert.True(t, matchesPluginFilter(p, "aws", "", ""))
	})

	t.Run("query matches description case-insensitively", func(t *testing.T) {
		assert.True(t, matchesPluginFilter(p, "MANAGES", "", ""))
	})

	t.Run("query miss", func(t *testing.T) {
		assert.False(t, matchesPluginFilter(p, "azure", "", ""))
	})

	t.Run("all filters AND together", func(t *testing.T) {
		assert.True(t, matchesPluginFilter(p, "aws", "cloud", "resource"))
		assert.False(t, matchesPluginFilter(p, "aws", "cloud", "auth"))
	})
}

func TestPluginFromPackage(t *testing.T) {
	t.Run("populates plugin fields from metadata", func(t *testing.T) {
		pkg := newPackage(t, "aws", "1.2.3", map[string]map[string]string{
			"plugin":  {"type": "resource", "namespace": "AWS"},
			"display": {"kind": "plugin", "category": "cloud"},
		})
		p := pluginFromPackage(pkg)
		assert.Equal(t, "aws", p.Name)
		assert.Equal(t, "resource", p.Type)
		assert.Equal(t, "AWS", p.Namespace)
		assert.Equal(t, "plugin", p.Kind)
		assert.Equal(t, "cloud", p.Category)
		assert.Equal(t, "1.2.3", p.InstalledVersion)
	})
}

func TestVersionString(t *testing.T) {
	t.Run("plain semver", func(t *testing.T) {
		assert.Equal(t, "1.2.3", versionString(newVersion(t, "1.2.3")))
	})

	t.Run("with prerelease", func(t *testing.T) {
		assert.Equal(t, "1.2.3-dev.4", versionString(newVersion(t, "1.2.3-dev.4")))
	})

	t.Run("nil version", func(t *testing.T) {
		assert.Equal(t, "", versionString(nil))
	})
}
