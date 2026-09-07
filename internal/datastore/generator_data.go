// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"encoding/json"
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// GeneratorData builds the generator_data payload for a generator.
//
// Unlike TTLPolicyData, no field needs to be stripped or reassembled: a
// generator carries no one-of and pkgmodel.ParseGenerator already dispatches
// on the same discriminated Type field the generator marshals itself with, so
// the full generator value round-trips through it directly. Centralized here
// so the four backends agree on the same byte-level format rather than each
// deciding independently.
func GeneratorData(gen pkgmodel.Generator) ([]byte, error) {
	data, err := json.Marshal(gen)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal generator data: %w", err)
	}
	return data, nil
}

// GeneratorFromData rebuilds a generator from a stored generator_data
// payload.
func GeneratorFromData(data []byte) (pkgmodel.Generator, error) {
	gen, err := pkgmodel.ParseGenerator(data)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal generator data: %w", err)
	}
	return gen, nil
}
