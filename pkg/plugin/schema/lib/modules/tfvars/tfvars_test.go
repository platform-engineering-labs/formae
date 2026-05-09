// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package tfvars

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTFVars(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.tfvars")
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)
	return path
}

func TestParseTFVarsFile_StringValues(t *testing.T) {
	path := writeTFVars(t, `
region         = "us-east-1"
instance_type  = "t3.micro"
`)
	result, err := ParseTFVarsFile(path)
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", result["region"])
	assert.Equal(t, "t3.micro", result["instance_type"])
}

func TestParseTFVarsFile_NumericValues(t *testing.T) {
	path := writeTFVars(t, `
instance_count = 3
price          = 9.99
`)
	result, err := ParseTFVarsFile(path)
	require.NoError(t, err)
	assert.Equal(t, int64(3), result["instance_count"])
	assert.Equal(t, 9.99, result["price"])
}

func TestParseTFVarsFile_BooleanValues(t *testing.T) {
	path := writeTFVars(t, `
enable_logging  = true
enable_debug    = false
`)
	result, err := ParseTFVarsFile(path)
	require.NoError(t, err)
	assert.Equal(t, true, result["enable_logging"])
	assert.Equal(t, false, result["enable_debug"])
}

func TestParseTFVarsFile_ListValues(t *testing.T) {
	path := writeTFVars(t, `
availability_zones = ["us-east-1a", "us-east-1b", "us-east-1c"]
ports              = [80, 443, 8080]
`)
	result, err := ParseTFVarsFile(path)
	require.NoError(t, err)
	assert.Equal(t, []any{"us-east-1a", "us-east-1b", "us-east-1c"}, result["availability_zones"])
	assert.Equal(t, []any{int64(80), int64(443), int64(8080)}, result["ports"])
}

func TestParseTFVarsFile_MapValues(t *testing.T) {
	path := writeTFVars(t, `
tags = {
  Name        = "web-server"
  Environment = "production"
}
`)
	result, err := ParseTFVarsFile(path)
	require.NoError(t, err)
	tags, ok := result["tags"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "web-server", tags["Name"])
	assert.Equal(t, "production", tags["Environment"])
}

func TestParseTFVarsFile_EmptyList(t *testing.T) {
	path := writeTFVars(t, `
items = []
`)
	result, err := ParseTFVarsFile(path)
	require.NoError(t, err)
	assert.Equal(t, []any{}, result["items"])
}

func TestParseTFVarsFile_MixedTypes(t *testing.T) {
	path := writeTFVars(t, `
region         = "us-east-1"
instance_count = 2
enable_logging = true
price          = 4.5
tags = {
  Name = "test"
}
zones = ["a", "b"]
`)
	result, err := ParseTFVarsFile(path)
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", result["region"])
	assert.Equal(t, int64(2), result["instance_count"])
	assert.Equal(t, true, result["enable_logging"])
	assert.Equal(t, 4.5, result["price"])
	assert.Equal(t, map[string]any{"Name": "test"}, result["tags"])
	assert.Equal(t, []any{"a", "b"}, result["zones"])
}

func TestParseTFVarsFile_FileNotFound(t *testing.T) {
	_, err := ParseTFVarsFile("/nonexistent/path/test.tfvars")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing")
}

func TestParseTFVarsFile_MalformedHCL(t *testing.T) {
	path := writeTFVars(t, `this is not valid hcl {{{`)
	_, err := ParseTFVarsFile(path)
	require.Error(t, err)
}
