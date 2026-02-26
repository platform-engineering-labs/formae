// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"context"
	"fmt"
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockFactory(name string, shouldErr bool) Factory {
	return func(_ context.Context, _ *pkgmodel.DatastoreConfig, _ string) (Datastore, error) {
		if shouldErr {
			return nil, fmt.Errorf("factory error for %s", name)
		}
		return &mockDatastore{name: name}, nil
	}
}

func TestRegistry_RegisterAndCreate(t *testing.T) {
	r := NewRegistry()
	r.Register("sqlite", newMockFactory("sqlite", false))

	ds, err := r.Create("sqlite", context.Background(), &pkgmodel.DatastoreConfig{}, "agent-1")
	require.NoError(t, err)
	assert.Equal(t, "sqlite", ds.(*mockDatastore).name)
}

func TestRegistry_CreateUnknownTypeReturnsError(t *testing.T) {
	r := NewRegistry()

	_, err := r.Create("nonexistent", context.Background(), &pkgmodel.DatastoreConfig{}, "agent-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestRegistry_CreatePropagatesFactoryError(t *testing.T) {
	r := NewRegistry()
	r.Register("broken", newMockFactory("broken", true))

	_, err := r.Create("broken", context.Background(), &pkgmodel.DatastoreConfig{}, "agent-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "factory error")
}

func TestRegistry_RegisterMultipleAndRetrieveEach(t *testing.T) {
	r := NewRegistry()
	r.Register("sqlite", newMockFactory("sqlite", false))
	r.Register("postgres", newMockFactory("postgres", false))
	r.Register("auroradataapi", newMockFactory("aurora", false))

	ds, err := r.Create("sqlite", context.Background(), &pkgmodel.DatastoreConfig{}, "agent-1")
	require.NoError(t, err)
	assert.Equal(t, "sqlite", ds.(*mockDatastore).name)

	ds, err = r.Create("postgres", context.Background(), &pkgmodel.DatastoreConfig{}, "agent-1")
	require.NoError(t, err)
	assert.Equal(t, "postgres", ds.(*mockDatastore).name)

	ds, err = r.Create("auroradataapi", context.Background(), &pkgmodel.DatastoreConfig{}, "agent-1")
	require.NoError(t, err)
	assert.Equal(t, "aurora", ds.(*mockDatastore).name)
}
