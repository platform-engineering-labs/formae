// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit
// +build unit

package lib

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLibraryExtensionCall(t *testing.T) {
	uri, _ := url.Parse("libext:///random/id?length=10")
	assert.NotNil(t, Call(uri))
	assert.Empty(t, Call(uri).Error)
	assert.NotNil(t, Call(uri).Body)
}
