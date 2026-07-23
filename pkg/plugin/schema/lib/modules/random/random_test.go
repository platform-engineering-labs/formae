// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package random

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRandomId(t *testing.T) {
	uri, _ := url.Parse("libext:///random/id?length=10")
	assert.NotNil(t, Random.Invoke(uri))
	assert.Empty(t, Random.Invoke(uri).Error)
	assert.NotNil(t, Random.Invoke(uri))
}

func TestRandomIdValidRange(t *testing.T) {
	for _, length := range []int{1, 2, 18} {
		uri, _ := url.Parse(fmt.Sprintf("libext:///random/id?length=%d", length))

		result := Random.Invoke(uri)
		require.NotNil(t, result)
		require.Empty(t, result.Error, "length %d should not error", length)
		require.NotNil(t, result.Body)

		var parsed struct {
			ID int64 `json:"id"`
		}
		require.NoError(t, json.Unmarshal(result.Body, &parsed))

		var mn int64 = 1
		for i := 1; i < length; i++ {
			mn *= 10
		}
		mx := mn*10 - 1

		assert.GreaterOrEqual(t, parsed.ID, mn, "length %d: id below range", length)
		assert.LessOrEqual(t, parsed.ID, mx, "length %d: id above range", length)
	}
}

func TestRandomIdErrorForOverLength(t *testing.T) {
	for _, length := range []int{19, 24, 100} {
		uri, _ := url.Parse(fmt.Sprintf("libext:///random/id?length=%d", length))

		result := Random.Invoke(uri)
		require.NotNil(t, result)
		assert.NotEmpty(t, result.Error, "length %d should return a clean error, not panic", length)
	}
}

func TestRandomPassword(t *testing.T) {
	uri, _ := url.Parse("libext:///random/password?length=10&useSpecial=true")
	assert.NotNil(t, Random.Invoke(uri))
	assert.Empty(t, Random.Invoke(uri).Error)
	assert.NotNil(t, Random.Invoke(uri).Body)
}
