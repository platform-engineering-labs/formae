// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package props

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatch(t *testing.T) {
	oaProperties := json.RawMessage(`{"BucketName": "test-bucket", "Tags": [{"Key": "env", "Value": "prod"}]}`)
	rProperties := `{"BucketName": "test-bucket", "Tags": [{"Key": "env", "Value": "prod"}]}`

	match, err := Match(oaProperties, rProperties)
	assert.NoError(t, err)
	assert.True(t, match)

	// Test non-matching properties
	rPropertiesDiff := `{"BucketName": "different-bucket"}`
	match, err = Match(oaProperties, rPropertiesDiff)
	assert.NoError(t, err)
	assert.False(t, match)
}
