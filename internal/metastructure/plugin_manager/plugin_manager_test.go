//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_manager

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeOrbitalClient struct {
	refreshErr error
}

func (f *fakeOrbitalClient) Refresh() error           { return f.refreshErr }
func (f *fakeOrbitalClient) Install(_ ...string) error { return nil }
func (f *fakeOrbitalClient) Remove(_ ...string) error  { return nil }
func (f *fakeOrbitalClient) Update(_ ...string) error  { return nil }
func (f *fakeOrbitalClient) Ready() bool               { return true }

func TestNewPluginManager(t *testing.T) {
	pm := newForTesting(slog.Default(), &fakeOrbitalClient{})
	require.NotNil(t, pm)
	require.NotNil(t, pm.orb)
}
