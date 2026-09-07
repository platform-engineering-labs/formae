// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"testing"

	"github.com/stretchr/testify/assert"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func ru(target, resourceTargetLabel, ksuid string) ResourceUpdate {
	return ResourceUpdate{
		DesiredState:   pkgmodel.Resource{Target: target, Ksuid: ksuid},
		ResourceTarget: pkgmodel.Target{Label: resourceTargetLabel},
	}
}

func TestReferencedTargetLabels_DistinctFirstSeenOrder(t *testing.T) {
	rus := []ResourceUpdate{
		ru("a", "b", "k1"),
		ru("a", "", "k2"),  // dup "a", empty resource-target skipped
		ru("c", "b", "k3"), // "c" new, "b" already seen
		ru("", "d", "k4"),  // empty desired-target skipped, "d" new
	}
	assert.Equal(t, []string{"a", "b", "c", "d"}, ReferencedTargetLabels(rus))
}

func TestReferencedTargetLabels_Empty(t *testing.T) {
	assert.Nil(t, ReferencedTargetLabels(nil))
}

func TestSourceTargetByKsuid_MapsNonEmptyKsuids(t *testing.T) {
	rus := []ResourceUpdate{
		ru("target-a", "x", "k1"),
		ru("target-b", "x", "k2"),
		ru("target-c", "x", ""), // empty ksuid skipped
	}
	got := SourceTargetByKsuid(rus)
	assert.Equal(t, map[string]string{"k1": "target-a", "k2": "target-b"}, got)
}
