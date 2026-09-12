// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package aurora

import (
	"context"
	"os"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestStackRetirement(t *testing.T) {
	if os.Getenv("FORMAE_TEST_AURORA_CLUSTER_ARN") == "" {
		t.Skip("local Data API configuration required")
	}
	database := os.Getenv("FORMAE_TEST_AURORA_DATABASE")
	if database == "" {
		database = "formae"
	}
	cfg := &pkgmodel.DatastoreConfig{DatastoreType: pkgmodel.AuroraDataAPIDatastore, AuroraDataAPI: pkgmodel.AuroraDataAPIConfig{ClusterARN: os.Getenv("FORMAE_TEST_AURORA_CLUSTER_ARN"), SecretARN: os.Getenv("FORMAE_TEST_AURORA_SECRET_ARN"), Database: database, Region: os.Getenv("FORMAE_TEST_AURORA_REGION"), Endpoint: os.Getenv("FORMAE_TEST_AURORA_ENDPOINT")}}
	open := func() *DatastoreAuroraDataAPI {
		ds, err := NewDatastoreAuroraDataAPI(context.Background(), cfg, "test")
		require.NoError(t, err)
		return ds.(*DatastoreAuroraDataAPI)
	}
	first := open()
	defer func() { first.Close() }()
	second := open()
	defer second.Close()
	dstest.RunStackRetirement(t, first, second, first.admissionStore())
}
