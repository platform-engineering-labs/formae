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
