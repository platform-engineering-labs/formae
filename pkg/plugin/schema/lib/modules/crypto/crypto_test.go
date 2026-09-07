// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package crypto

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKeyPair(t *testing.T) {
	uri, _ := url.Parse("libext:///crypto/keyPair?encoding=pkcs1&bits=2048&params=256")
	assert.NotNil(t, Crypto.Invoke(uri))
	assert.Empty(t, Crypto.Invoke(uri).Error)
	assert.NotNil(t, Crypto.Invoke(uri).Body)
}
