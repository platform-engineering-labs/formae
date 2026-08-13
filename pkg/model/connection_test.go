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

func TestAuthConfigReadsTheActiveArm(t *testing.T) {
	raw := json.RawMessage(`{"type":"oidc"}`)

	assert.Equal(t, raw, CliConfig{Connection: &HostedConnection{Auth: raw}}.AuthConfig())
	assert.Equal(t, raw, CliConfig{Connection: &ClassicConnection{Auth: raw}}.AuthConfig())
	assert.Nil(t, CliConfig{Connection: &ClassicConnection{}}.AuthConfig())
	assert.Nil(t, CliConfig{}.AuthConfig())
}
