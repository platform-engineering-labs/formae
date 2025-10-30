// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/testing/unit"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

func TestResourceUpdater_Initialization(t *testing.T) {
	sender := gen.PID{Node: "test", ID: 100}

	ds := newMockDatastore()
	env := map[gen.Env]any{
		"RetryConfig": pkgmodel.RetryConfig{
			StatusCheckInterval: 1 * time.Second,
		},
		"DiscoveryConfig": pkgmodel.DiscoveryConfig{},
		"Datastore":       ds,
	}

	updater, err := unit.Spawn(t, newResourceUpdater,
		unit.WithArgs(sender),
		unit.WithEnv(env))

	assert.NoError(t, err, "Failed to spawn resource updater")
	assert.NotNil(t, updater, "Resource updater should not be nil")
	assert.False(t, updater.IsTerminated(), "Resource updater should not be terminated after spawning")
}
