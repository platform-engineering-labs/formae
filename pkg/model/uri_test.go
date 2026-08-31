// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCollectReferencedKSUIDs(t *testing.T) {
	t.Run("empty bytes returns empty slice", func(t *testing.T) {
		got := CollectReferencedKSUIDs([]byte{})
		assert.Equal(t, []string{}, got)
	})

	t.Run("nil bytes returns empty slice", func(t *testing.T) {
		got := CollectReferencedKSUIDs(nil)
		assert.Equal(t, []string{}, got)
	})

	t.Run("invalid JSON returns empty slice", func(t *testing.T) {
		got := CollectReferencedKSUIDs([]byte(`not json`))
		assert.Equal(t, []string{}, got)
	})

	t.Run("no refs in data returns empty slice", func(t *testing.T) {
		got := CollectReferencedKSUIDs([]byte(`{"Key": "Value", "Nested": {"A": 1}}`))
		assert.Equal(t, []string{}, got)
	})

	t.Run("single resolved ref at top level", func(t *testing.T) {
		data := []byte(`{
			"SubnetId": {"$ref": "formae://2abc123def456ghi7jk8lmno9p0#/SubnetId", "$value": "subnet-abc"}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{"2abc123def456ghi7jk8lmno9p0"}, got)
	})

	t.Run("ref nested inside an object", func(t *testing.T) {
		data := []byte(`{
			"NetworkConfig": {
				"VpcId": {"$ref": "formae://ksuid1111111111111111111111#/VpcId", "$value": "vpc-1"}
			}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{"ksuid1111111111111111111111"}, got)
	})

	t.Run("ref nested inside an array", func(t *testing.T) {
		data := []byte(`{
			"Subnets": [
				{"$ref": "formae://ksuidAAAAAAAAAAAAAAAAAAAAA#/Subnets/0", "$value": "subnet-a"},
				{"$ref": "formae://ksuidBBBBBBBBBBBBBBBBBBBBBBB#/Subnets/1", "$value": "subnet-b"}
			]
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{"ksuidAAAAAAAAAAAAAAAAAAAAA", "ksuidBBBBBBBBBBBBBBBBBBBBBBB"}, got)
	})

	t.Run("multiple distinct refs deduped and sorted", func(t *testing.T) {
		data := []byte(`{
			"A": {"$ref": "formae://zzz#/A", "$value": "a"},
			"B": {"$ref": "formae://aaa#/B", "$value": "b"}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{"aaa", "zzz"}, got)
	})

	t.Run("duplicate refs from multiple fields deduped to one", func(t *testing.T) {
		data := []byte(`{
			"Field1": {"$ref": "formae://sameKSUID#/Prop1", "$value": "v1"},
			"Field2": {"$ref": "formae://sameKSUID#/Prop2", "$value": "v2"}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{"sameKSUID"}, got)
	})

	t.Run("ref nested inside $value of another ref", func(t *testing.T) {
		// A resolved ref whose $value contains another ref — the collector must
		// descend into all object children, not stop at the $ref object.
		data := []byte(`{
			"Outer": {
				"$ref": "formae://outerKSUID#/Outer",
				"$value": {
					"Inner": {"$ref": "formae://innerKSUID#/Inner", "$value": "inner-val"}
				}
			}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{"innerKSUID", "outerKSUID"}, got)
	})

	t.Run("unresolved source-time resolvable ($res: true, no $ref) excluded", func(t *testing.T) {
		data := []byte(`{
			"SubnetId": {"$res": true, "$label": "my-vpc", "$type": "AWS::EC2::VPC", "$stack": "default", "$property": "SubnetId"}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{}, got)
	})

	t.Run("$ref with non-formae scheme excluded", func(t *testing.T) {
		data := []byte(`{
			"Schema": {"$ref": "#/definitions/Foo"},
			"Link": {"$ref": "http://example.com/schema"}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{}, got)
	})

	t.Run("malformed formae $ref (no hash separator) excluded", func(t *testing.T) {
		data := []byte(`{
			"Field": {"$ref": "formae://"}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{}, got)
	})

	t.Run("partial formae URI missing hash excluded", func(t *testing.T) {
		data := []byte(`{
			"Field": {"$ref": "formae://someKSUID"}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{}, got)
	})

	t.Run("$ref with non-string value (number) excluded", func(t *testing.T) {
		data := []byte(`{
			"Field": {"$ref": 42}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{}, got)
	})

	t.Run("mixed included and excluded in one document", func(t *testing.T) {
		data := []byte(`{
			"ValidRef": {"$ref": "formae://validKSUID#/Prop", "$value": "val"},
			"SchemaRef": {"$ref": "#/definitions/Foo"},
			"Resolvable": {"$res": true, "$label": "x", "$type": "T", "$stack": "s", "$property": "p"},
			"MalformedRef": {"$ref": "formae://"},
			"Nested": {
				"DeepRef": {"$ref": "formae://deepKSUID#/Deep", "$value": "deep-val"}
			}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{"deepKSUID", "validKSUID"}, got)
	})

	t.Run("deeply nested array of objects with refs", func(t *testing.T) {
		data := []byte(`{
			"Config": {
				"Rules": [
					{"Action": {"$ref": "formae://rule1KSUID#/Action", "$value": "allow"}},
					{"Action": {"$ref": "formae://rule2KSUID#/Action", "$value": "deny"}}
				]
			}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{"rule1KSUID", "rule2KSUID"}, got)
	})

	t.Run("returns non-nil empty slice not nil", func(t *testing.T) {
		got := CollectReferencedKSUIDs([]byte(`{}`))
		assert.NotNil(t, got)
		assert.Equal(t, []string{}, got)
	})
}

func TestCollectReferencedKSUIDs_GenEnvelopes(t *testing.T) {
	t.Run("translated $gen envelope contributes its generator KSUID", func(t *testing.T) {
		data := []byte(`{
			"MasterUserPassword": {
				"$gen": true,
				"$generator": "2genKSUID000000000000000000",
				"$output": "value",
				"$visibility": "Opaque",
				"$value": "sha256:abc",
				"$hashed": true,
				"$resolvedFrom": "sha256:abc"
			}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{"2genKSUID000000000000000000"}, got)
	})

	t.Run("a document carrying both a $ref and a $gen yields both KSUIDs", func(t *testing.T) {
		data := []byte(`{
			"VpcId": {"$ref": "formae://2refKSUID000000000000000000#/VpcId", "$value": "vpc-1"},
			"Password": {"$gen": true, "$generator": "2genKSUID000000000000000000", "$output": "value", "$value": "sha256:abc"}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{"2genKSUID000000000000000000", "2refKSUID000000000000000000"}, got)
	})

	t.Run("$gen nested inside an array element is collected", func(t *testing.T) {
		data := []byte(`{
			"Users": [
				{"Name": "alice", "Password": {"$gen": true, "$generator": "2genAAA00000000000000000000", "$output": "value"}},
				{"Name": "bob", "Password": {"$gen": true, "$generator": "2genBBB00000000000000000000", "$output": "value"}}
			]
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{"2genAAA00000000000000000000", "2genBBB00000000000000000000"}, got)
	})

	t.Run("untranslated $gen envelope contributes nothing", func(t *testing.T) {
		data := []byte(`{
			"Password": {"$gen": true, "$label": "db-password", "$stack": "default", "$output": "value", "$visibility": "Opaque"}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{}, got)
	})

	t.Run("the same generator bound to two properties is deduped", func(t *testing.T) {
		data := []byte(`{
			"A": {"$gen": true, "$generator": "2sameGen0000000000000000000", "$output": "value"},
			"B": {"$gen": true, "$generator": "2sameGen0000000000000000000", "$output": "value"}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{"2sameGen0000000000000000000"}, got)
	})

	t.Run("multiple generator KSUIDs are returned sorted", func(t *testing.T) {
		data := []byte(`{
			"A": {"$gen": true, "$generator": "zzzGen", "$output": "value"},
			"B": {"$gen": true, "$generator": "aaaGen", "$output": "value"}
		}`)
		got := CollectReferencedKSUIDs(data)
		assert.Equal(t, []string{"aaaGen", "zzzGen"}, got)
	})
}
