//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPropertyLines_ScalarsSortedAndUnquoted(t *testing.T) {
	raw := []byte(`{"QueueName":"my-queue","DelaySeconds":0,"Enabled":true}`)
	got := PropertyLines(raw, 1)
	// Keys sorted alphabetically; strings unquoted (inventory-detail style).
	assert.Equal(t, []string{
		" DelaySeconds: 0",
		" Enabled: true",
		" QueueName: my-queue",
	}, got)
}

func TestPropertyLines_TagArrayCollapsesKeyValue(t *testing.T) {
	raw := []byte(`{"Tags":[{"Key":"Name","Value":"web"},{"Key":"Env","Value":"prod"}]}`)
	got := PropertyLines(raw, 1)
	assert.Equal(t, []string{
		" Tags:",
		"   - Name: web",
		"   - Env: prod",
	}, got)
}

func TestPropertyLines_NestedObjectRecurses(t *testing.T) {
	raw := []byte(`{"Config":{"B":2,"A":1}}`)
	got := PropertyLines(raw, 1)
	assert.Equal(t, []string{
		" Config:",
		"   A: 1",
		"   B: 2",
	}, got)
}

func TestPropertyLines_SimplifiesSpecialValues(t *testing.T) {
	// $value wrapper unwraps to its inner value.
	assert.Equal(t, []string{" Name: web-vpc"},
		PropertyLines([]byte(`{"Name":{"$strategy":"SetOnce","$value":"web-vpc"}}`), 1))

	// $ref renders "<value>  → <label.property>".
	assert.Equal(t, []string{" VpcId: vpc-0b5  → lifeline-vpc.VpcId"},
		PropertyLines([]byte(`{"VpcId":{"$res":true,"$value":"vpc-0b5","$label":"lifeline-vpc","$property":"VpcId"}}`), 1))

	// opaque values are masked.
	assert.Equal(t, []string{" Secret: " + propOpaqueMask},
		PropertyLines([]byte(`{"Secret":{"$visibility":"Opaque","$value":"hunter2"}}`), 1))
}

func TestPropertyLines_EmptyIsNil(t *testing.T) {
	assert.Nil(t, PropertyLines(nil, 1))
	assert.Nil(t, PropertyLines([]byte(``), 1))
}
