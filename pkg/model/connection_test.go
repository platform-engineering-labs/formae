//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidInstallationID(t *testing.T) {
	accepted := map[string]string{
		"a provisioned installation": "3HzFPXfPDGhwLJJVtaHbmFs6vLa",
		"all digits":                 "000000000000000000000000000",
		"all uppercase":              "ABCDEFGHIJKLMNOPQRSTUVWXYZA",
	}
	for name, id := range accepted {
		t.Run(name, func(t *testing.T) {
			assert.True(t, ValidInstallationID(id), id)
		})
	}

	// The grammar is wider than the 160 bits a KSUID encodes, so a handful of
	// well-formed strings would fail a KSUID parser. Pinned rather than fixed:
	// the edge routes on this same grammar, and a client that refused what the
	// router accepts would be wrong in the direction that costs a user access.
	assert.True(t, ValidInstallationID("zzzzzzzzzzzzzzzzzzzzzzzzzzz"),
		"the check is the routing-key grammar, not a decode")

	rejected := map[string]string{
		// The format this identifier used to have. Rejected rather than also
		// accepted: nothing mints one any more, so a profile carrying one
		// names an installation that cannot exist.
		"a canonical uuid": "3f2b8c14-0000-4000-8000-000000000000",
		"empty":            "",
		"one short":        "3HzFPXfPDGhwLJJVtaHbmFs6vL",
		"one long":         "3HzFPXfPDGhwLJJVtaHbmFs6vLaa",
		"a hyphen":         "3HzFPXfPDGhwLJJVtaHbmFs6v-a",
		"an underscore":    "3HzFPXfPDGhwLJJVtaHbmFs6v_a",
		"trailing space":   "3HzFPXfPDGhwLJJVtaHbmFs6vL ",
		"a newline":        "3HzFPXfPDGhwLJJVtaHbmFs6vLa\n",
	}
	for name, id := range rejected {
		t.Run(name, func(t *testing.T) {
			assert.False(t, ValidInstallationID(id), id)
		})
	}
}

func TestAuthConfigReadsTheActiveArm(t *testing.T) {
	raw := json.RawMessage(`{"type":"oidc"}`)

	assert.Equal(t, raw, CliConfig{Connection: &HostedConnection{Auth: raw}}.AuthConfig())
	assert.Equal(t, raw, CliConfig{Connection: &ClassicConnection{Auth: raw}}.AuthConfig())
	assert.Nil(t, CliConfig{Connection: &ClassicConnection{}}.AuthConfig())
	assert.Nil(t, CliConfig{}.AuthConfig())
}
