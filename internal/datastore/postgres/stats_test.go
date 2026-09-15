//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package postgres_test

import (
	"encoding/json"
	"testing"

	"github.com/demula/mksuid/v2"
	"github.com/platform-engineering-labs/formae/internal/constants"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// Stats counts each resource once, at its current version, and leaves out a
// resource whose current version is a delete tombstone.
func TestStats_CountsEachResourceOnceAtItsCurrentVersion(t *testing.T) {
	d, cleanup := newTestDatastore(t)
	defer cleanup()
	storeTestTarget(t, d)

	store := func(ksuid, stack, typ string) *pkgmodel.Resource {
		r := &pkgmodel.Resource{
			Ksuid: ksuid, NativeID: "native-" + ksuid, Stack: stack, Label: "res-" + ksuid,
			Type: typ, Target: "test-target", Properties: json.RawMessage(`{}`),
		}
		_, err := d.StoreResource(r, "cmd-"+ksuid)
		require.NoError(t, err)
		return r
	}

	// A: two versions in stack s1; only the current one may count.
	a := store(mksuid.New().String(), "s1", "AWS::S3::Bucket")
	_, err := d.StoreResource(a, "cmd-a2")
	require.NoError(t, err)
	// B: stored, then deleted; its current version is a tombstone.
	b := store(mksuid.New().String(), "s1", "AWS::S3::Bucket")
	_, err = d.DeleteResource(b, "cmd-b-delete")
	require.NoError(t, err)
	// C: unmanaged.
	store(mksuid.New().String(), constants.UnmanagedStack, "AWS::EC2::Instance")
	// D: a second managed stack.
	store(mksuid.New().String(), "s2", "AWS::S3::Bucket")

	got, err := d.Stats()
	require.NoError(t, err)

	require.Equal(t, 2, got.Stacks, "s1 and s2; the tombstoned B does not keep s1 alive on its own")
	require.Equal(t, map[string]int{"AWS": 2}, got.ManagedResources, "A once (not twice) and D")
	require.Equal(t, map[string]int{"AWS": 1}, got.UnmanagedResources)
	require.Equal(t, map[string]int{"AWS::S3::Bucket": 2, "AWS::EC2::Instance": 1}, got.ResourceTypes)
	require.Equal(t, map[string]int{"AWS": 1}, got.Targets)
}
