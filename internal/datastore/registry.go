// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"context"
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Factory is a constructor function that creates a Datastore implementation.
type Factory func(ctx context.Context, cfg *pkgmodel.DatastoreConfig, agentID string) (Datastore, error)

// Registry stores datastore factories indexed by type name.
type Registry struct {
	factories map[string]Factory
}

// DefaultRegistry is the package-level registry used by default.
var DefaultRegistry = NewRegistry()

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]Factory),
	}
}

// Register adds a datastore factory under the given type name.
func (r *Registry) Register(name string, factory Factory) {
	r.factories[name] = factory
}

// Create looks up the factory registered under the given type name and calls it
// to construct a new Datastore.
func (r *Registry) Create(name string, ctx context.Context, cfg *pkgmodel.DatastoreConfig, agentID string) (Datastore, error) {
	factory, ok := r.factories[name]
	if !ok {
		return nil, fmt.Errorf("no datastore registered with type %q", name)
	}
	return factory(ctx, cfg, agentID)
}
