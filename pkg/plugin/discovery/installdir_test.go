// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package discovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSystemPluginDir(t *testing.T) {
	assert.Equal(t, "/opt/pel/formae/plugins", SystemPluginDir("/opt/pel/bin/formae"))
}

func TestSystemPluginDir_NestedBin(t *testing.T) {
	assert.Equal(t, "/usr/local/pel/formae/plugins", SystemPluginDir("/usr/local/pel/bin/formae"))
}
