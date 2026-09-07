//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The generated profile teaches the canonical form, so a new user's first
// edit does not start from a deprecated setting.
func TestTemplateDocumentsTheConnectionForm(t *testing.T) {
	tmpl := StubTemplate

	assert.Contains(t, tmpl, "connection = new Classic")
	assert.NotContains(t, tmpl, "api {")
}
