// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package props

import (
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

func TestHasIdentityTags(t *testing.T) {
	resource := &model.Resource{
		Schema: model.Schema{
			Tags: TagsField,
		},
	}

	resourceKnown := &model.Resource{
		Schema: model.Schema{
			Tags: "HostedZoneTags",
		},
	}

	matchDefault := `
	{
      "Tags": [
      	{
		   "Key": "FormaeResourceLabel",
           "Value": "some-label"
         },
         {
			"Key": "FormaeStackLabel",
           "Value": "some-stack"
          }
      ]
	}`

	matchKnown := `
	{
      "HostedZoneTags": [
      	{
		   "Key": "FormaeResourceLabel",
           "Value": "some-label"
         },
         {
			"Key": "FormaeStackLabel",
           "Value": "some-stack"
          }
      ]
	}`

	assert.True(t, HasIdentityTags(resource, matchDefault, "some-label", "some-stack"))
	assert.True(t, HasIdentityTags(resourceKnown, matchKnown, "some-label", "some-stack"))

	assert.False(t, HasIdentityTags(resource, matchDefault, "formae-label", "some-snarf"))
	assert.False(t, HasIdentityTags(resourceKnown, matchKnown, "some-taco", "some-stack"))
}
