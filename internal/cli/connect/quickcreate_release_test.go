// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build releasecheck

package connect

import "testing"

// The release gate for the template coordinates: a build may not ship while
// any of them still holds the placeholder. Runs under the releasecheck tag —
// the release pipeline, not the unit suite — so development proceeds while
// the publication is pending, and a release cannot.
func TestTemplateCoordinatesArePinned(t *testing.T) {
	for name, value := range map[string]string{
		"providerTemplateVersionID": providerTemplateVersionID,
		"providerTemplateSHA256":    providerTemplateSHA256,
		"roleTemplateVersionID":     roleTemplateVersionID,
		"roleTemplateSHA256":        roleTemplateSHA256,
	} {
		if value == "PINNED_AT_PUBLICATION" {
			t.Errorf("%s still holds the placeholder; pin it from the infrastructure repo's publication before releasing", name)
		}
	}
}
