//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package bcrypt

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRandomId(t *testing.T) {
	uri, _ := url.Parse("libext:///bcrypt/hashPassword?password=mySecret")
	assert.NotNil(t, BCrypt.Invoke(uri))
	assert.Empty(t, BCrypt.Invoke(uri).Error)
	assert.NotNil(t, BCrypt.Invoke(uri))
}
