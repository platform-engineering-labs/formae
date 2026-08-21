//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/pkg/model"
)

func TestAuroraDataAPIConfigRoundTrips(t *testing.T) {
	config, err := PKL{}.FormaeConfig("./testdata/config/datastore_aurora.pkl")
	require.NoError(t, err)

	assert.Equal(t, model.AuroraDataAPIDatastore, config.Agent.Datastore.DatastoreType)

	aurora := config.Agent.Datastore.AuroraDataAPI
	assert.Equal(t, "arn:aws:rds:us-east-1:123456789012:cluster:formae", aurora.ClusterARN)
	assert.Equal(t, "arn:aws:secretsmanager:us-east-1:123456789012:secret:formae", aurora.SecretARN)
	assert.Equal(t, "formae", aurora.Database)
	assert.Equal(t, "us-east-1", aurora.Region)
	assert.Equal(t, "http://localhost:8080", aurora.Endpoint)
}

// A config with no auroraDataAPI block leaves the endpoint empty, which is what
// makes the AWS SDK resolve its default endpoint for the region.
func TestAuroraDataAPIEndpointDefaultsToEmpty(t *testing.T) {
	config, err := PKL{}.FormaeConfig("./testdata/config/test_config.pkl")
	require.NoError(t, err)

	assert.Empty(t, config.Agent.Datastore.AuroraDataAPI.Endpoint)
}
