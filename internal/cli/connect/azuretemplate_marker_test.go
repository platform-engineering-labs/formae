// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The marker the cloud plugins' discovery filters key on. Spelled identically
// on AWS, Azure and GCP, and hyphenated because a GCP label key cannot hold a
// colon. Pinned here because it is a cross-repo contract: this template writes
// it and the azure plugin reads it, and the two drifting apart is silent. The
// filter simply matches nothing, which is indistinguishable from the resource
// not being there.
const ownershipMarkerKey = "formae-owned"

// tagsOf walks the template to the managed identity the nested deployment
// creates and returns its tags.
func identityTags(t *testing.T) map[string]any {
	t.Helper()

	var doc struct {
		Resources []struct {
			Type       string `json:"type"`
			Properties struct {
				Template struct {
					Resources []struct {
						Type string         `json:"type"`
						Tags map[string]any `json:"tags"`
					} `json:"resources"`
				} `json:"template"`
			} `json:"properties"`
		} `json:"resources"`
	}
	require.NoError(t, json.Unmarshal(azureTemplateJSON, &doc))

	for _, outer := range doc.Resources {
		for _, inner := range outer.Properties.Template.Resources {
			if inner.Type == "Microsoft.ManagedIdentity/userAssignedIdentities" {
				return inner.Tags
			}
		}
	}
	t.Fatal("the template declares no user-assigned identity")
	return nil
}

func TestAzureTemplateMarksTheIdentityAsFormaeOwned(t *testing.T) {
	tags := identityTags(t)

	assert.Equal(t, "true", tags[ownershipMarkerKey],
		"the identity must carry the marker discovery filters on, or connect artifacts stay importable")
}

// The marker was spelled with a colon before the three clouds were unified on
// one key. A template still writing the old spelling produces artifacts no
// filter matches.
func TestAzureTemplateDoesNotWriteTheSupersededMarker(t *testing.T) {
	tags := identityTags(t)

	assert.NotContains(t, tags, "formae:owned")
}
