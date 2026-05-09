//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_manager

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/platform-engineering-labs/orbital/opm/records"
	"github.com/platform-engineering-labs/orbital/ops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// fake orbital client
// ---------------------------------------------------------------------------

type fakeOrbitalClient struct {
	refreshErr      error
	installErr      error
	removeErr       error
	updateErr       error
	availableForErr error

	installed []*records.Package
	available map[string]*records.Status

	// capture args
	installedSpecs []string
	removedSpecs   []string
	updatedSpecs   []string
}

func (f *fakeOrbitalClient) Refresh() error           { return f.refreshErr }
func (f *fakeOrbitalClient) Ready() bool               { return true }

func (f *fakeOrbitalClient) Install(packages ...string) error {
	f.installedSpecs = packages
	return f.installErr
}
func (f *fakeOrbitalClient) Remove(packages ...string) error {
	f.removedSpecs = packages
	return f.removeErr
}
func (f *fakeOrbitalClient) Update(packages ...string) error {
	f.updatedSpecs = packages
	return f.updateErr
}
func (f *fakeOrbitalClient) List() ([]*records.Package, error) {
	return f.installed, nil
}
func (f *fakeOrbitalClient) Available() (map[string]*records.Status, error) {
	return f.available, nil
}
func (f *fakeOrbitalClient) AvailableFor(name string) (*records.Status, error) {
	if f.availableForErr != nil {
		return nil, f.availableForErr
	}
	if f.available == nil {
		return nil, nil
	}
	s, ok := f.available[name]
	if !ok {
		return nil, nil
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

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


// ---------------------------------------------------------------------------
// constructor
// ---------------------------------------------------------------------------

func TestNewPluginManager(t *testing.T) {
	pm := newForTesting(slog.Default(), &fakeOrbitalClient{})
	require.NotNil(t, pm)
	require.NotNil(t, pm.listOrb)
	require.NotNil(t, pm.factory)
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

func TestList_Empty(t *testing.T) {
	pm := newForTesting(slog.Default(), &fakeOrbitalClient{})
	plugins, err := pm.List()
	require.NoError(t, err)
	assert.Empty(t, plugins)
}

func TestList_ReturnsInstalledPlugins(t *testing.T) {
	fake := &fakeOrbitalClient{
		installed: []*records.Package{
			newPackage(t, "formae-plugin-aws", "1.2.3", map[string]map[string]string{
				"plugin":  {"type": "resource", "namespace": "aws"},
				"display": {"kind": "plugin", "category": "cloud"},
			}),
			newPackage(t, "formae-plugin-tailscale", "0.5.0", map[string]map[string]string{
				"plugin": {"type": "network", "namespace": "tailscale"},
				"display": {"kind": "plugin"},
			}),
		},
	}
	pm := newForTesting(slog.Default(), fake)

	plugins, err := pm.List()
	require.NoError(t, err)
	require.Len(t, plugins, 2)

	assert.Equal(t, "formae-plugin-aws", plugins[0].Name)
	assert.Equal(t, "1.2.3", plugins[0].InstalledVersion)
	assert.Equal(t, "resource", plugins[0].Type)
	assert.Equal(t, "aws", plugins[0].Namespace)
	assert.Equal(t, "cloud", plugins[0].Category)

	assert.Equal(t, "formae-plugin-tailscale", plugins[1].Name)
	assert.Equal(t, "0.5.0", plugins[1].InstalledVersion)
	assert.Equal(t, "network", plugins[1].Type)
}

// ---------------------------------------------------------------------------
// List metadata filtering
// ---------------------------------------------------------------------------

func TestList_IncludesMetapackages(t *testing.T) {
	// Curated metapackages set display.kind == "metapackage" (and have no
	// "plugin" metadata block since they have no runtime). They should
	// surface in List the same way regular plugins do, with Kind set so
	// the renderer can group them separately.
	fake := &fakeOrbitalClient{
		installed: []*records.Package{
			newPackage(t, "standard", "0.1.0", map[string]map[string]string{
				"display": {"kind": "metapackage", "category": "bundle"},
			}),
		},
	}
	pm := newForTesting(slog.Default(), fake)

	plugins, err := pm.List()
	require.NoError(t, err)
	require.Len(t, plugins, 1)
	assert.Equal(t, "standard", plugins[0].Name)
	assert.Equal(t, "metapackage", plugins[0].Kind)
	assert.Equal(t, "bundle", plugins[0].Category)
	assert.Equal(t, "", plugins[0].Type, "metapackages have no runtime type")
}

func TestList_FiltersOutPackagesWithoutPluginMetadata(t *testing.T) {
	// formae and pkl are binary tooling installed in the orbital tree but
	// carry no "plugin" metadata; only the package with plugin metadata
	// should surface through List.
	fake := &fakeOrbitalClient{
		installed: []*records.Package{
			newPackage(t, "formae", "0.85.0", nil),
			newPackage(t, "pkl", "0.31.0", nil),
			newPackage(t, "formae-plugin-aws", "1.2.3", map[string]map[string]string{
				"plugin": {"type": "resource", "namespace": "aws"},
				"display": {"kind": "plugin"},
			}),
		},
	}
	pm := newForTesting(slog.Default(), fake)

	plugins, err := pm.List()
	require.NoError(t, err)
	require.Len(t, plugins, 1)
	assert.Equal(t, "formae-plugin-aws", plugins[0].Name)
}

// ---------------------------------------------------------------------------
// Available
// ---------------------------------------------------------------------------

func TestAvailable_NoFilter(t *testing.T) {
	fake := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-aws": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-aws", "1.2.3", map[string]map[string]string{
						"plugin":  {"type": "resource", "namespace": "aws"},
						"display": {"kind": "plugin", "category": "cloud"},
					}),
					newPackage(t, "formae-plugin-aws", "1.1.0", nil),
				},
			},
			"formae-plugin-azure": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-azure", "0.1.0", map[string]map[string]string{
						"plugin":  {"type": "resource", "namespace": "azure"},
						"display": {"kind": "plugin", "category": "cloud"},
					}),
				},
			},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	plugins, err := pm.Available(AvailableFilter{})
	require.NoError(t, err)
	require.Len(t, plugins, 2)

	// Should be sorted by name.
	assert.Equal(t, "formae-plugin-aws", plugins[0].Name)
	assert.Equal(t, "formae-plugin-azure", plugins[1].Name)

	// AWS should have two available versions.
	assert.Equal(t, []string{"1.2.3", "1.1.0"}, plugins[0].AvailableVersions)
	// Azure should have one.
	assert.Equal(t, []string{"0.1.0"}, plugins[1].AvailableVersions)
}

func TestAvailable_FilterByCategory(t *testing.T) {
	fake := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-aws": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-aws", "1.0.0", map[string]map[string]string{
						"plugin":  {"type": "resource"},
						"display": {"kind": "plugin", "category": "cloud"},
					}),
				},
			},
			"formae-plugin-tailscale": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-tailscale", "0.3.0", map[string]map[string]string{
						"plugin":  {"type": "network"},
						"display": {"kind": "plugin", "category": "networking"},
					}),
				},
			},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	plugins, err := pm.Available(AvailableFilter{Category: "networking"})
	require.NoError(t, err)
	require.Len(t, plugins, 1)
	assert.Equal(t, "formae-plugin-tailscale", plugins[0].Name)
}

func TestAvailable_FilterByType(t *testing.T) {
	fake := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-aws": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-aws", "1.0.0", map[string]map[string]string{
						"plugin": {"type": "resource"},
						"display": {"kind": "plugin"},
					}),
				},
			},
			"formae-plugin-tailscale": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-tailscale", "0.3.0", map[string]map[string]string{
						"plugin": {"type": "network"},
						"display": {"kind": "plugin"},
					}),
				},
			},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	plugins, err := pm.Available(AvailableFilter{Type: "resource"})
	require.NoError(t, err)
	require.Len(t, plugins, 1)
	assert.Equal(t, "formae-plugin-aws", plugins[0].Name)
}

func TestAvailable_FilterByQuery(t *testing.T) {
	fake := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-aws": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-aws", "1.0.0",
						map[string]map[string]string{"plugin": {"type": "resource"}, "display": {"kind": "plugin"}}),
				},
			},
			"formae-plugin-azure": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-azure", "0.1.0",
						map[string]map[string]string{"plugin": {"type": "resource"}, "display": {"kind": "plugin"}}),
				},
			},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	plugins, err := pm.Available(AvailableFilter{Query: "azure"})
	require.NoError(t, err)
	require.Len(t, plugins, 1)
	assert.Equal(t, "formae-plugin-azure", plugins[0].Name)
}

func TestAvailable_RefreshFailureDoesNotBlock(t *testing.T) {
	fake := &fakeOrbitalClient{
		refreshErr: errors.New("network timeout"),
		available: map[string]*records.Status{
			"formae-plugin-aws": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-aws", "1.0.0",
						map[string]map[string]string{"plugin": {"type": "resource"}, "display": {"kind": "plugin"}}),
				},
			},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	plugins, err := pm.Available(AvailableFilter{})
	require.NoError(t, err)
	require.Len(t, plugins, 1)
}

func TestAvailable_EmptyStatus(t *testing.T) {
	fake := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-empty": {Available: nil},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	plugins, err := pm.Available(AvailableFilter{})
	require.NoError(t, err)
	assert.Empty(t, plugins)
}

func TestAvailable_FiltersOutPackagesWithoutPluginMetadata(t *testing.T) {
	// formae has no plugin metadata; sftp does. Only sftp surfaces.
	fake := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae": {
				Available: []*records.Package{
					newPackage(t, "formae", "0.85.0", nil),
				},
			},
			"formae-plugin-sftp": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-sftp", "0.1.0", map[string]map[string]string{
						"plugin": {"type": "resource", "namespace": "sftp"},
						"display": {"kind": "plugin"},
					}),
				},
			},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	plugins, err := pm.Available(AvailableFilter{})
	require.NoError(t, err)
	require.Len(t, plugins, 1)
	assert.Equal(t, "formae-plugin-sftp", plugins[0].Name)
}

func TestAvailable_InstalledVersionFromInstalledFlag(t *testing.T) {
	// Two candidates; v1.1.0 is the one orbital marks Installed.
	// InstalledVersion must reflect that — NOT just Available[0].Version.
	pkgInstalled := newPackage(t, "formae-plugin-aws", "1.1.0",
		map[string]map[string]string{"plugin": {"type": "resource"}, "display": {"kind": "plugin"}})
	pkgInstalled.Installed = true
	pkgNewer := newPackage(t, "formae-plugin-aws", "1.2.0",
		map[string]map[string]string{"plugin": {"type": "resource"}, "display": {"kind": "plugin"}})

	fake := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-aws": {
				Available: []*records.Package{pkgNewer, pkgInstalled},
			},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	plugins, err := pm.Available(AvailableFilter{})
	require.NoError(t, err)
	require.Len(t, plugins, 1)
	assert.Equal(t, "1.1.0", plugins[0].InstalledVersion)
}

func TestAvailable_NoInstalledVersionWhenNoneInstalled(t *testing.T) {
	// Nothing in Available has Installed=true → InstalledVersion is empty.
	fake := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-sftp": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-sftp", "0.1.0",
						map[string]map[string]string{"plugin": {"type": "resource"}, "display": {"kind": "plugin"}}),
				},
			},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	plugins, err := pm.Available(AvailableFilter{})
	require.NoError(t, err)
	require.Len(t, plugins, 1)
	assert.Equal(t, "", plugins[0].InstalledVersion)
}

// ---------------------------------------------------------------------------
// Channel routing
// ---------------------------------------------------------------------------

// channelFakes builds a factory that hands out a different fake per channel.
func channelFakes(byChannel map[string]*fakeOrbitalClient) orbitalFactory {
	return func(channel string) (orbitalClient, error) {
		if c, ok := byChannel[channel]; ok {
			return c, nil
		}
		return &fakeOrbitalClient{}, nil
	}
}

func TestAvailable_DefaultsToStableChannel(t *testing.T) {
	stable := &fakeOrbitalClient{available: map[string]*records.Status{}}
	dev := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-sftp": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-sftp", "0.1.0-dev.1",
						map[string]map[string]string{"plugin": {"type": "resource"}, "display": {"kind": "plugin"}}),
				},
			},
		},
	}
	pm := newForTestingWithFactory(slog.Default(), &fakeOrbitalClient{},
		channelFakes(map[string]*fakeOrbitalClient{
			"stable": stable,
			"dev":    dev,
		}))

	plugins, err := pm.Available(AvailableFilter{})
	require.NoError(t, err)
	assert.Empty(t, plugins, "default search must hit stable, which is empty")
}

func TestAvailable_ChannelOverrideReturnsDevPlugin(t *testing.T) {
	stable := &fakeOrbitalClient{available: map[string]*records.Status{}}
	dev := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-sftp": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-sftp", "0.1.0-dev.1",
						map[string]map[string]string{"plugin": {"type": "resource"}, "display": {"kind": "plugin"}}),
				},
			},
		},
	}
	pm := newForTestingWithFactory(slog.Default(), &fakeOrbitalClient{},
		channelFakes(map[string]*fakeOrbitalClient{
			"stable": stable,
			"dev":    dev,
		}))

	plugins, err := pm.Available(AvailableFilter{Channel: "dev"})
	require.NoError(t, err)
	require.Len(t, plugins, 1)
	assert.Equal(t, "formae-plugin-sftp", plugins[0].Name)
}

func TestInfo_ChannelOverride(t *testing.T) {
	stable := &fakeOrbitalClient{available: map[string]*records.Status{}}
	dev := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-sftp": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-sftp", "0.1.0-dev.1",
						map[string]map[string]string{"plugin": {"type": "resource"}, "display": {"kind": "plugin"}}),
				},
			},
		},
	}
	pm := newForTestingWithFactory(slog.Default(), &fakeOrbitalClient{},
		channelFakes(map[string]*fakeOrbitalClient{
			"stable": stable,
			"dev":    dev,
		}))

	pStable, err := pm.Info("formae-plugin-sftp", "")
	require.NoError(t, err)
	assert.Nil(t, pStable, "info on default stable channel should not find dev-only plugin")

	pDev, err := pm.Info("formae-plugin-sftp", "dev")
	require.NoError(t, err)
	require.NotNil(t, pDev)
	assert.Equal(t, "formae-plugin-sftp", pDev.Name)
}

func TestInstall_ChannelRoutesToCorrectClient(t *testing.T) {
	stable := &fakeOrbitalClient{}
	dev := &fakeOrbitalClient{}
	pm := newForTestingWithFactory(slog.Default(), &fakeOrbitalClient{},
		channelFakes(map[string]*fakeOrbitalClient{
			"stable": stable,
			"dev":    dev,
		}))

	_, err := pm.Install(InstallRequest{
		Packages: []PackageRef{{Name: "formae-plugin-sftp", Version: "0.1.0-dev.1"}},
		Channel:  "dev",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"formae-plugin-sftp@0.1.0-dev.1"}, dev.installedSpecs,
		"install with --channel dev must route to the dev client")
	assert.Empty(t, stable.installedSpecs, "stable client should not see this install")
}

// ---------------------------------------------------------------------------
// Info
// ---------------------------------------------------------------------------

func TestInfo_Found(t *testing.T) {
	fake := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-aws": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-aws", "1.2.0", map[string]map[string]string{
						"plugin": {"type": "resource", "namespace": "aws"},
						"display": {"kind": "plugin"},
					}),
					newPackage(t, "formae-plugin-aws", "1.1.0", nil),
				},
			},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	p, err := pm.Info("formae-plugin-aws", "")
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, "formae-plugin-aws", p.Name)
	assert.Equal(t, "resource", p.Type)
	assert.Equal(t, []string{"1.2.0", "1.1.0"}, p.AvailableVersions)
}

func TestInfo_NotFound(t *testing.T) {
	fake := &fakeOrbitalClient{available: map[string]*records.Status{}}
	pm := newForTesting(slog.Default(), fake)

	p, err := pm.Info("nonexistent", "")
	require.NoError(t, err)
	assert.Nil(t, p)
}

// TestInfo_NotFoundFromOrbitalError verifies that orbital's "no available
// packages for: X" error (returned when X is in none of the configured
// channel's repos) is translated to a not-found nil result rather than
// bubbling up as a 500.
func TestInfo_NotFoundFromOrbitalError(t *testing.T) {
	fake := &fakeOrbitalClient{
		availableForErr: errors.New("no available packages for: ghost"),
	}
	pm := newForTesting(slog.Default(), fake)

	p, err := pm.Info("ghost", "dev")
	require.NoError(t, err)
	assert.Nil(t, p)
}

func TestInfo_NotFoundWhenNoPluginMetadata(t *testing.T) {
	// `formae` has no "plugin" metadata; Info treats it as not-a-plugin.
	fake := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae": {
				Available: []*records.Package{
					newPackage(t, "formae", "0.85.0", nil),
				},
			},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	p, err := pm.Info("formae", "")
	require.NoError(t, err)
	assert.Nil(t, p)
}

// ---------------------------------------------------------------------------
// Install
// ---------------------------------------------------------------------------

func TestInstall_Success(t *testing.T) {
	fake := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-aws": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-aws", "1.2.0", map[string]map[string]string{
						"plugin": {"type": "resource"},
						"display": {"kind": "plugin"},
					}),
				},
			},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	resp, err := pm.Install(InstallRequest{
		Packages: []PackageRef{
			{Name: "formae-plugin-aws", Version: "1.2.0"},
		},
	})
	require.NoError(t, err)
	require.Len(t, resp.Operations, 1)
	assert.Equal(t, "formae-plugin-aws", resp.Operations[0].Name)
	assert.Equal(t, "install", resp.Operations[0].Action)
	assert.Equal(t, "1.2.0", resp.Operations[0].Version)
	assert.Equal(t, "resource", resp.Operations[0].Type)
	assert.True(t, resp.RequiresRestart)

	// Verify the spec passed to orbital.
	assert.Equal(t, []string{"formae-plugin-aws@1.2.0"}, fake.installedSpecs)
}

func TestInstall_NoVersion(t *testing.T) {
	fake := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-aws": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-aws", "2.0.0", nil),
				},
			},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	resp, err := pm.Install(InstallRequest{
		Packages: []PackageRef{{Name: "formae-plugin-aws"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"formae-plugin-aws"}, fake.installedSpecs)
	assert.Equal(t, "2.0.0", resp.Operations[0].Version)
}

func TestInstall_Error(t *testing.T) {
	fake := &fakeOrbitalClient{installErr: errors.New("conflict")}
	pm := newForTesting(slog.Default(), fake)

	_, err := pm.Install(InstallRequest{
		Packages: []PackageRef{{Name: "bad"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "installing packages")
}

// ---------------------------------------------------------------------------
// Uninstall
// ---------------------------------------------------------------------------

func TestUninstall_Success(t *testing.T) {
	fake := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-aws": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-aws", "1.0.0", map[string]map[string]string{
						"plugin": {"type": "resource"},
						"display": {"kind": "plugin"},
					}),
				},
			},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	resp, err := pm.Uninstall(UninstallRequest{
		Packages: []PackageRef{{Name: "formae-plugin-aws"}},
	})
	require.NoError(t, err)
	require.Len(t, resp.Operations, 1)
	assert.Equal(t, "remove", resp.Operations[0].Action)
	assert.Equal(t, []string{"formae-plugin-aws"}, fake.removedSpecs)
}

func TestUninstall_Error(t *testing.T) {
	fake := &fakeOrbitalClient{removeErr: errors.New("in use")}
	pm := newForTesting(slog.Default(), fake)

	_, err := pm.Uninstall(UninstallRequest{
		Packages: []PackageRef{{Name: "locked"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing packages")
}

// ---------------------------------------------------------------------------
// Upgrade
// ---------------------------------------------------------------------------

func TestUpgrade_Success(t *testing.T) {
	fake := &fakeOrbitalClient{
		available: map[string]*records.Status{
			"formae-plugin-aws": {
				Available: []*records.Package{
					newPackage(t, "formae-plugin-aws", "2.0.0", map[string]map[string]string{
						"plugin": {"type": "resource"},
						"display": {"kind": "plugin"},
					}),
				},
			},
		},
	}
	pm := newForTesting(slog.Default(), fake)

	resp, err := pm.Upgrade(UpgradeRequest{
		Packages: []PackageRef{{Name: "formae-plugin-aws", Version: "2.0.0"}},
	})
	require.NoError(t, err)
	require.Len(t, resp.Operations, 1)
	assert.Equal(t, "install", resp.Operations[0].Action)
	assert.Equal(t, "2.0.0", resp.Operations[0].Version)
	assert.Equal(t, []string{"formae-plugin-aws@2.0.0"}, fake.updatedSpecs)
}

func TestUpgrade_Error(t *testing.T) {
	fake := &fakeOrbitalClient{updateErr: errors.New("no update")}
	pm := newForTesting(slog.Default(), fake)

	_, err := pm.Upgrade(UpgradeRequest{
		Packages: []PackageRef{{Name: "x"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upgrading packages")
}

// ---------------------------------------------------------------------------
// pluginFromPackage
// ---------------------------------------------------------------------------

func TestPluginFromPackage_NilHeader(t *testing.T) {
	pkg := &records.Package{Header: nil}
	p := pluginFromPackage(pkg)
	assert.Equal(t, "", p.Name)
	assert.Equal(t, "", p.InstalledVersion)
}

func TestPluginFromPackage_NilVersion(t *testing.T) {
	pkg := &records.Package{
		Header: &ops.Header{
			Name:    "test",
			Version: nil,
		},
	}
	p := pluginFromPackage(pkg)
	assert.Equal(t, "test", p.Name)
	assert.Equal(t, "", p.InstalledVersion)
}

// ---------------------------------------------------------------------------
// matchesFilter
// ---------------------------------------------------------------------------

func TestMatchesFilter_AllEmpty(t *testing.T) {
	assert.True(t, matchesFilter(Plugin{Name: "x"}, AvailableFilter{}))
}

func TestMatchesFilter_QueryCaseInsensitive(t *testing.T) {
	p := Plugin{Name: "formae-plugin-AWS", Summary: "Amazon Web Services"}
	assert.True(t, matchesFilter(p, AvailableFilter{Query: "amazon"}))
	assert.True(t, matchesFilter(p, AvailableFilter{Query: "AWS"}))
	assert.False(t, matchesFilter(p, AvailableFilter{Query: "gcp"}))
}
