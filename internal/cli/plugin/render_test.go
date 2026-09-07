// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"os"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func TestMain(m *testing.M) {
	tuitest.PinRendering()
	os.Exit(m.Run())
}

// ── fixtures ────────────────────────────────────────────────────────────────

func fixturePluginListAllKinds() []apimodel.Plugin {
	return []apimodel.Plugin{
		{Name: "aws", Type: "resource", InstalledVersion: "2.1.0"},
		{Name: "azure", Type: "resource", InstalledVersion: "1.4.2"},
		{Name: "tailscale", Type: "resource", InstalledVersion: "0.9.0", ManagedBy: "homelab-bundle"},
		{Name: "aws-sso", Type: "auth", InstalledVersion: "1.1.0"},
		{Name: "homelab-bundle", Kind: "metapackage", InstalledVersion: "3.0.1"},
	}
}

func fixturePluginListEmpty() []apimodel.Plugin {
	return []apimodel.Plugin{}
}

func fixturePluginSearchResults() []apimodel.Plugin {
	return []apimodel.Plugin{
		{Name: "cloudflare", Summary: "Cloudflare DNS and network resources", InstalledVersion: "1.2.3"},
		{Name: "route53", Summary: "Amazon Route53 DNS management"},
		{Name: "gandi", Summary: "Gandi domain and DNS records"},
	}
}

func fixturePluginSearchEmpty() []apimodel.Plugin {
	return []apimodel.Plugin{}
}

func fixturePluginInfoFull() *apimodel.Plugin {
	return &apimodel.Plugin{
		Name:              "cloudflare",
		Type:              "resource",
		Namespace:         "CLOUDFLARE",
		Category:          "network",
		Summary:           "Cloudflare DNS and network resources",
		Publisher:         "Acme Corp",
		License:           "Apache-2.0",
		Channel:           "stable",
		InstalledVersion:  "1.2.3",
		AvailableVersions: []string{"1.2.3", "1.2.2", "1.1.0"},
		ManagedBy:         "homelab-bundle",
	}
}

func fixturePluginInfoBundle() *apimodel.Plugin {
	return &apimodel.Plugin{
		Name:              "homelab-bundle",
		Kind:              "metapackage",
		InstalledVersion:  "3.0.1",
		Summary:           "Curated homelab stack",
		Description:       "A full homelab bundle including tailscale, aws, and more",
		AvailableVersions: []string{"3.0.1", "3.0.0"},
	}
}

func fixturePluginInfoNotInstalled() *apimodel.Plugin {
	return &apimodel.Plugin{
		Name:              "route53",
		Type:              "resource",
		Namespace:         "AWS",
		Category:          "dns",
		Summary:           "Amazon Route53 DNS management",
		Publisher:         "Amazon Web Services",
		License:           "Apache-2.0",
		AvailableVersions: []string{"2.0.0", "1.9.0"},
	}
}

// ── golden tests ─────────────────────────────────────────────────────────────

func TestRenderPluginList_Golden(t *testing.T) {
	th := theme.New("formae")
	out := renderPluginList(th, fixturePluginListAllKinds())
	tuitest.RequireGolden(t, []byte(out))
}

func TestRenderPluginList_EmptyGolden(t *testing.T) {
	th := theme.New("formae")
	out := renderPluginList(th, fixturePluginListEmpty())
	tuitest.RequireGolden(t, []byte(out))
}

func TestRenderPluginSearch_Golden(t *testing.T) {
	th := theme.New("formae")
	out := renderPluginSearch(th, fixturePluginSearchResults(), SearchRenderOpts{Query: "dns"})
	tuitest.RequireGolden(t, []byte(out))
}

func TestRenderPluginSearch_CategoryEchoGolden(t *testing.T) {
	th := theme.New("formae")
	out := renderPluginSearch(th, fixturePluginSearchResults(), SearchRenderOpts{
		Query:    "dns",
		Category: "network",
		Type:     "resource",
	})
	tuitest.RequireGolden(t, []byte(out))
}

func TestRenderPluginSearch_EmptyGolden(t *testing.T) {
	th := theme.New("formae")
	out := renderPluginSearch(th, fixturePluginSearchEmpty(), SearchRenderOpts{Query: "dns"})
	tuitest.RequireGolden(t, []byte(out))
}

func TestRenderPluginInfo_FullGolden(t *testing.T) {
	th := theme.New("formae")
	out := renderPluginInfo(th, fixturePluginInfoFull())
	tuitest.RequireGolden(t, []byte(out))
}

func TestRenderPluginInfo_BundleGolden(t *testing.T) {
	th := theme.New("formae")
	out := renderPluginInfo(th, fixturePluginInfoBundle())
	tuitest.RequireGolden(t, []byte(out))
}

func TestRenderPluginInfo_NotInstalledGolden(t *testing.T) {
	th := theme.New("formae")
	out := renderPluginInfo(th, fixturePluginInfoNotInstalled())
	tuitest.RequireGolden(t, []byte(out))
}
