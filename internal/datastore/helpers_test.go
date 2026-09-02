// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"encoding/json"
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

// A resource whose Properties and ReadOnlyProperties are byte-identical to
// another's but whose OwnedMembers differs must not compare read-write equal:
// a record-only update (see resource_update.ResourceUpdate.RecordOnly)
// carries exactly this shape, and every StoreResource backend's
// identical-resource short-circuit reads readWriteEqual/readOnlyEqual to
// decide whether a write is needed at all. Reporting both true here would
// make ownership-record commits silently vanish at the storage layer.
func TestResourcesAreEqual_OwnedMembersDifference(t *testing.T) {
	base := func(owned pkgmodel.OwnedMembers) *pkgmodel.Resource {
		return &pkgmodel.Resource{
			NativeID:           "native-1",
			Stack:              "default",
			Type:               "FakeAWS::EC2::SecurityGroup",
			Label:              "sg",
			Properties:         json.RawMessage(`{"Tags":["a","b"]}`),
			ReadOnlyProperties: json.RawMessage(`{"Arn":"arn:aws:ec2:sg/test"}`),
			OwnedMembers:       owned,
		}
	}

	before := base(nil)
	after := base(pkgmodel.OwnedMembers{"Tags": {Rule: "Set", Members: []string{`"a"`}}})

	readWriteEqual, readOnlyEqual := ResourcesAreEqual(before, after)
	assert.False(t, readWriteEqual, "an OwnedMembers change must count as a read-write difference")
	assert.True(t, readOnlyEqual, "ReadOnlyProperties are unchanged")
}

// Two resources with no ownership record on either side, and otherwise
// identical, still compare fully equal — the added OwnedMembers check must
// not turn nil-vs-nil (or nil-vs-empty) into a spurious difference.
func TestResourcesAreEqual_NoOwnedMembersOnEitherSide(t *testing.T) {
	r1 := &pkgmodel.Resource{
		NativeID:           "native-1",
		Stack:              "default",
		Type:               "FakeAWS::EC2::SecurityGroup",
		Label:              "sg",
		Properties:         json.RawMessage(`{"Tags":["a","b"]}`),
		ReadOnlyProperties: json.RawMessage(`{"Arn":"arn:aws:ec2:sg/test"}`),
	}
	r2 := &pkgmodel.Resource{
		NativeID:           "native-1",
		Stack:              "default",
		Type:               "FakeAWS::EC2::SecurityGroup",
		Label:              "sg",
		Properties:         json.RawMessage(`{"Tags":["a","b"]}`),
		ReadOnlyProperties: json.RawMessage(`{"Arn":"arn:aws:ec2:sg/test"}`),
		OwnedMembers:       pkgmodel.OwnedMembers{},
	}

	readWriteEqual, readOnlyEqual := ResourcesAreEqual(r1, r2)
	assert.True(t, readWriteEqual)
	assert.True(t, readOnlyEqual)
}
