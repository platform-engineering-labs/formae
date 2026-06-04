// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// RFC-0041 pre-flight: two forma resources cannot claim the same existing
// managed row. resourceA matches via current label, resourceB matches via
// alias. Without rejection, the reconcile loop emits two updates against the
// same existing row and the final label is nondeterministic.
func TestValidateAliasUsage_DuplicateClaim_LabelAndAlias(t *testing.T) {
	ds, _ := GetDeps(t)

	ksuid := util.NewID()
	_, err := ds.StoreStack(&pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "prod"}},
		Resources: []pkgmodel.Resource{{
			Ksuid:      ksuid,
			Label:      "web-server",
			Type:       "AWS::EC2::Instance",
			Stack:      "prod",
			Target:     "test-target",
			NativeID:   "i-0abc",
			Properties: json.RawMessage(`{}`),
			Managed:    true,
		}},
	}, "setup")
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "prod"}},
		Resources: []pkgmodel.Resource{
			{Label: "web-server", Type: "AWS::EC2::Instance", Stack: "prod", Target: "test-target", Properties: json.RawMessage(`{}`)},
			{Label: "app-server", Alias: "web-server", Type: "AWS::EC2::Instance", Stack: "prod", Target: "test-target", Properties: json.RawMessage(`{}`)},
		},
	}

	err = validateAliasUsage(forma, ds)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "web-server")
	assert.Contains(t, err.Error(), "app-server")
	assert.Contains(t, err.Error(), "via alias")
	assert.Contains(t, err.Error(), "via label")
}

// RFC-0041 pre-flight: an alias that matches nothing — neither an existing
// managed row in the resource's stack nor an unmanaged row — is rejected.
// Creating-and-renaming a fresh resource is almost always a stale alias from
// a prior refactor.
func TestValidateAliasUsage_DeadAlias_NoMatch(t *testing.T) {
	ds, _ := GetDeps(t)

	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "prod"}},
		Resources: []pkgmodel.Resource{{
			Label:      "app-server",
			Alias:      "never-existed",
			Type:       "AWS::EC2::Instance",
			Stack:      "prod",
			Target:     "test-target",
			Properties: json.RawMessage(`{}`),
		}},
	}

	err := validateAliasUsage(forma, ds)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "app-server")
	assert.Contains(t, err.Error(), "never-existed")
}

// Alias matching an unmanaged row of the same type is valid (bring-under-
// management + rename in one apply).
func TestValidateAliasUsage_AliasMatchesUnmanaged_OK(t *testing.T) {
	ds, _ := GetDeps(t)

	_, err := ds.StoreStack(&pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: constants.UnmanagedStack}},
		Resources: []pkgmodel.Resource{{
			Ksuid:      util.NewID(),
			Label:      "discovered-i-123",
			Type:       "AWS::EC2::Instance",
			Stack:      constants.UnmanagedStack,
			Target:     "test-target",
			NativeID:   "i-123",
			Properties: json.RawMessage(`{}`),
		}},
	}, "setup")
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "prod"}},
		Resources: []pkgmodel.Resource{{
			Label:      "app-server",
			Alias:      "discovered-i-123",
			Type:       "AWS::EC2::Instance",
			Stack:      "prod",
			Target:     "test-target",
			Properties: json.RawMessage(`{}`),
		}},
	}

	assert.NoError(t, validateAliasUsage(forma, ds))
}

// Alias matching a managed row in the same stack (the canonical pure rename
// case) is valid.
func TestValidateAliasUsage_AliasMatchesManaged_OK(t *testing.T) {
	ds, _ := GetDeps(t)

	_, err := ds.StoreStack(&pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "prod"}},
		Resources: []pkgmodel.Resource{{
			Ksuid:      util.NewID(),
			Label:      "web-server",
			Type:       "AWS::EC2::Instance",
			Stack:      "prod",
			Target:     "test-target",
			NativeID:   "i-0abc",
			Properties: json.RawMessage(`{}`),
			Managed:    true,
		}},
	}, "setup")
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "prod"}},
		Resources: []pkgmodel.Resource{{
			Label:      "app-server",
			Alias:      "web-server",
			Type:       "AWS::EC2::Instance",
			Stack:      "prod",
			Target:     "test-target",
			Properties: json.RawMessage(`{}`),
		}},
	}

	assert.NoError(t, validateAliasUsage(forma, ds))
}

// Alias equal to label is nonsensical — reject.
func TestValidateAliasUsage_AliasEqualsLabel_Rejected(t *testing.T) {
	ds, _ := GetDeps(t)

	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "prod"}},
		Resources: []pkgmodel.Resource{{
			Label:      "web-server",
			Alias:      "web-server",
			Type:       "AWS::EC2::Instance",
			Stack:      "prod",
			Target:     "test-target",
			Properties: json.RawMessage(`{}`),
		}},
	}

	err := validateAliasUsage(forma, ds)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "alias") && strings.Contains(err.Error(), "label"))
}

// Forma without aliases must pass cleanly even when existing resources are
// present — validation is alias-specific.
func TestValidateAliasUsage_NoAliases_NoError(t *testing.T) {
	ds, _ := GetDeps(t)

	_, err := ds.StoreStack(&pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "prod"}},
		Resources: []pkgmodel.Resource{{
			Ksuid: util.NewID(), Label: "web-server", Type: "AWS::EC2::Instance",
			Stack: "prod", Target: "test-target", NativeID: "i-0abc",
			Properties: json.RawMessage(`{}`), Managed: true,
		}},
	}, "setup")
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "prod"}},
		Resources: []pkgmodel.Resource{{
			Label: "web-server", Type: "AWS::EC2::Instance",
			Stack: "prod", Target: "test-target", Properties: json.RawMessage(`{}`),
		}},
	}

	assert.NoError(t, validateAliasUsage(forma, ds))
}
