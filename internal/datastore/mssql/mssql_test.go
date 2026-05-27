// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package mssql_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	_ "github.com/platform-engineering-labs/formae/internal/datastore/mssql"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// TestRegistryRegistration confirms the mssql package wires its factory
// into datastore.DefaultRegistry on import. Until the persistence layer
// lands, the factory itself returns a sentinel "not yet implemented" error —
// distinct from the registry's "no datastore registered" error.
func TestRegistryRegistration(t *testing.T) {
	cfg := &pkgmodel.DatastoreConfig{DatastoreType: pkgmodel.MSSQLDatastore}

	ds, err := datastore.DefaultRegistry.Create(pkgmodel.MSSQLDatastore, context.Background(), cfg, "test")

	if err == nil {
		t.Fatalf("expected an error from the not-yet-implemented factory, got datastore %T", ds)
	}
	if errors.Is(err, errors.New("no datastore registered")) || strings.Contains(err.Error(), "no datastore registered") {
		t.Fatalf("factory was not registered: %v", err)
	}
	if !strings.Contains(err.Error(), "mssql") {
		t.Fatalf("expected error from mssql factory, got: %v", err)
	}
}
