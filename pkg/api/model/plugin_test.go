//go:build unit

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPluginJSONRoundTrip(t *testing.T) {
	p := Plugin{
		Name:              "aws",
		Type:              "resource",
		Namespace:         "AWS",
		Category:          "cloud",
		Summary:           "AWS plugin",
		InstalledVersion:  "1.4.2",
		AvailableVersions: []string{"1.4.2", "1.3.1"},
		ManagedBy:         "standard",
	}
	data, err := json.Marshal(p)
	require.NoError(t, err)
	var back Plugin
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, p, back)
}

func TestInstallPluginsRequestJSONRoundTrip(t *testing.T) {
	req := InstallPluginsRequest{
		Packages: []PackageRef{
			{Name: "aws", Version: "1.4.2"},
			{Name: "azure"},
		},
	}
	data, err := json.Marshal(req)
	require.NoError(t, err)
	var back InstallPluginsRequest
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, req, back)
}

func TestInstallPluginsResponseJSONRoundTrip(t *testing.T) {
	resp := InstallPluginsResponse{
		Operations: []PluginOperation{
			{Name: "aws", Version: "1.4.2", Action: "install"},
		},
		RequiresRestart: true,
		Warnings:        []string{"Restart the agent"},
	}
	data, err := json.Marshal(resp)
	require.NoError(t, err)
	var back InstallPluginsResponse
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, resp, back)
}
