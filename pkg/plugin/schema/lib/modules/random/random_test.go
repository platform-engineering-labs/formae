// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit
// +build unit

package random

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRandomId(t *testing.T) {
	uri, _ := url.Parse("libext:///random/id?length=10")
	assert.NotNil(t, Random.Invoke(uri))
	assert.Empty(t, Random.Invoke(uri).Error)
	assert.NotNil(t, Random.Invoke(uri))
}

func TestRandomPassword(t *testing.T) {
	uri, _ := url.Parse("libext:///random/password?length=10&useSpecial=true")
	assert.NotNil(t, Random.Invoke(uri))
	assert.Empty(t, Random.Invoke(uri).Error)
	assert.NotNil(t, Random.Invoke(uri).Body)
}
