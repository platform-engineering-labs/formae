// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

func TestFindGenObjectsFromProperties(t *testing.T) {
	t.Run("finds a top-level $gen object", func(t *testing.T) {
		props := json.RawMessage(`{
			"Password": {"$gen": true, "$label": "db-password", "$stack": "default", "$output": "value", "$visibility": "Opaque"}
		}`)
		got := FindGenObjectsFromProperties(props)
		assert.Equal(t, []GenObject{
			{Path: "Password", Label: "db-password", Stack: "default", Output: "value"},
		}, got)
	})

	t.Run("finds a $gen object nested inside an object", func(t *testing.T) {
		props := json.RawMessage(`{
			"Config": {
				"Credentials": {"$gen": true, "$label": "api-key", "$stack": "secrets", "$output": "value"}
			}
		}`)
		got := FindGenObjectsFromProperties(props)
		assert.Equal(t, []GenObject{
			{Path: "Config.Credentials", Label: "api-key", Stack: "secrets", Output: "value"},
		}, got)
	})

	t.Run("finds $gen objects inside an array, addressed by index", func(t *testing.T) {
		props := json.RawMessage(`{
			"Secrets": [
				{"$gen": true, "$label": "one", "$stack": "default", "$output": "value"},
				{"$gen": true, "$label": "two", "$stack": "default", "$output": "value"}
			]
		}`)
		got := FindGenObjectsFromProperties(props)
		assert.Equal(t, []GenObject{
			{Path: "Secrets.0", Label: "one", Stack: "default", Output: "value"},
			{Path: "Secrets.1", Label: "two", Stack: "default", Output: "value"},
		}, got)
	})

	t.Run("stops descending at a $gen node", func(t *testing.T) {
		// A $gen envelope's own fields ($label, $stack, $output) must never be
		// walked into as if they were further nested structure — the walk
		// returns as soon as it recognizes the node, exactly like the
		// resolvable walk it mirrors.
		props := json.RawMessage(`{
			"Password": {
				"$gen": true,
				"$label": "db-password",
				"$stack": "default",
				"$output": "value",
				"$nested": {"$gen": true, "$label": "should-not-be-found", "$stack": "default", "$output": "value"}
			}
		}`)
		got := FindGenObjectsFromProperties(props)
		assert.Equal(t, []GenObject{
			{Path: "Password", Label: "db-password", Stack: "default", Output: "value"},
		}, got)
	})

	t.Run("ignores a $res object", func(t *testing.T) {
		props := json.RawMessage(`{
			"VpcId": {"$res": true, "$label": "my-vpc", "$type": "AWS::EC2::VPC", "$stack": "default", "$property": "VpcId"}
		}`)
		got := FindGenObjectsFromProperties(props)
		assert.Empty(t, got)
	})

	t.Run("mixed $res and $gen: only the $gen object is found", func(t *testing.T) {
		props := json.RawMessage(`{
			"VpcId": {"$res": true, "$label": "my-vpc", "$type": "AWS::EC2::VPC", "$stack": "default", "$property": "VpcId"},
			"Password": {"$gen": true, "$label": "db-password", "$stack": "default", "$output": "value"}
		}`)
		got := FindGenObjectsFromProperties(props)
		assert.Equal(t, []GenObject{
			{Path: "Password", Label: "db-password", Stack: "default", Output: "value"},
		}, got)
	})

	t.Run("no $gen objects returns empty, not nil-panicking", func(t *testing.T) {
		props := json.RawMessage(`{"Plain": "value", "Nested": {"A": 1}}`)
		got := FindGenObjectsFromProperties(props)
		assert.Empty(t, got)
	})
}

func TestIsGenObject(t *testing.T) {
	t.Run("true for an authored $gen envelope", func(t *testing.T) {
		v := gjson.Parse(`{"$gen": true, "$label": "x", "$stack": "default", "$output": "value"}`)
		assert.True(t, IsGenObject(v))
	})

	t.Run("true for a translated $gen envelope", func(t *testing.T) {
		v := gjson.Parse(`{"$gen": true, "$generator": "2abc123def456ghi7jk8lmno9p0", "$output": "value", "$visibility": "Opaque"}`)
		assert.True(t, IsGenObject(v))
	})

	t.Run("false when $gen is absent", func(t *testing.T) {
		v := gjson.Parse(`{"$res": true, "$label": "x"}`)
		assert.False(t, IsGenObject(v))
	})

	t.Run("false when $gen is false", func(t *testing.T) {
		v := gjson.Parse(`{"$gen": false}`)
		assert.False(t, IsGenObject(v))
	})

	t.Run("false for a non-object value", func(t *testing.T) {
		v := gjson.Parse(`"just a string"`)
		assert.False(t, IsGenObject(v))
	})
}

func TestGenGeneratorKSUID(t *testing.T) {
	t.Run("empty for an authored (not yet translated) $gen envelope", func(t *testing.T) {
		v := gjson.Parse(`{"$gen": true, "$label": "x", "$stack": "default", "$output": "value"}`)
		assert.Equal(t, "", GenGeneratorKSUID(v))
	})

	t.Run("returns the KSUID from a translated $gen envelope", func(t *testing.T) {
		v := gjson.Parse(`{"$gen": true, "$generator": "2abc123def456ghi7jk8lmno9p0", "$output": "value", "$visibility": "Opaque"}`)
		assert.Equal(t, "2abc123def456ghi7jk8lmno9p0", GenGeneratorKSUID(v))
	})

	t.Run("empty for a non-$gen object", func(t *testing.T) {
		v := gjson.Parse(`{"$res": true, "$generator": "should-not-be-read"}`)
		assert.Equal(t, "", GenGeneratorKSUID(v))
	})
}

func TestKnownGeneratorOutputs(t *testing.T) {
	t.Run("value is the only known output today", func(t *testing.T) {
		assert.True(t, KnownGeneratorOutputs["value"])
		assert.False(t, KnownGeneratorOutputs["nosuchoutput"])
	})
}
