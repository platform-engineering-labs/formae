// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package drift

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func coOwnedNsSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Fields: []string{"Name", "labels"},
		Hints:  map[string]pkgmodel.FieldHint{"labels": {CoOwned: &pkgmodel.CoOwnership{}}},
	}
}

// A modification whose only movement is a co-actor's never-owned member is
// dropped from the unabsorbed set, so a reconcile that also edits the
// resource (its edit lives in the plan, not here) is not rejected.
func TestRetainConfrontable_DropsToleratedCoOwnedMovement(t *testing.T) {
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "s", Type: "K8S::Core::Namespace", Label: "ns",
		Properties: json.RawMessage(`{"Name":"n","labels":{"app":"web","version":"2"}}`),
		Schema:     coOwnedNsSchema(),
	}}}
	mod := datastore.ResourceModification{
		Stack: "s", Type: "K8S::Core::Namespace", Label: "ns", Operation: "update", Ksuid: "k1",
		OldProperties: json.RawMessage(`{"Name":"n","labels":{"app":"web"}}`),
		Properties:    json.RawMessage(`{"Name":"n","labels":{"app":"web","team":"platform"}}`),
	}
	records := map[string]pkgmodel.OwnedMembers{"k1": {"labels": {Rule: "Mapping", Members: []string{"app"}}}}

	got := RetainConfrontable([]datastore.ResourceModification{mod}, records, forma)
	assert.Empty(t, got, "tolerated co-owned movement must not survive as unabsorbed drift")
}

// A modification with a declared member's out-of-band change stays: real
// drift on managed content still rejects.
func TestRetainConfrontable_KeepsDeclaredMemberDrift(t *testing.T) {
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "s", Type: "K8S::Core::Namespace", Label: "ns",
		Properties: json.RawMessage(`{"Name":"n","labels":{"app":"web"}}`),
		Schema:     coOwnedNsSchema(),
	}}}
	mod := datastore.ResourceModification{
		Stack: "s", Type: "K8S::Core::Namespace", Label: "ns", Operation: "update", Ksuid: "k1",
		OldProperties: json.RawMessage(`{"Name":"n","labels":{"app":"web"}}`),
		Properties:    json.RawMessage(`{"Name":"n","labels":{"app":"hacked"}}`),
	}
	records := map[string]pkgmodel.OwnedMembers{"k1": {"labels": {Rule: "Mapping", Members: []string{"app"}}}}

	got := RetainConfrontable([]datastore.ResourceModification{mod}, records, forma)
	assert.Len(t, got, 1, "a declared member's drift must remain confrontable")
}

// A modification on a resource not in the forma (an orphan) cannot be
// classified against a declaration and is kept — the conservative default.
func TestRetainConfrontable_KeepsNotInFormaModification(t *testing.T) {
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "s", Type: "K8S::Core::Namespace", Label: "other", Schema: coOwnedNsSchema(),
	}}}
	mod := datastore.ResourceModification{
		Stack: "s", Type: "K8S::Core::Namespace", Label: "orphan", Operation: "update", Ksuid: "k1",
		OldProperties: json.RawMessage(`{"Name":"n","labels":{"app":"web"}}`),
		Properties:    json.RawMessage(`{"Name":"n","labels":{"app":"web","team":"x"}}`),
	}
	records := map[string]pkgmodel.OwnedMembers{"k1": {"labels": {Rule: "Mapping", Members: []string{"app"}}}}

	got := RetainConfrontable([]datastore.ResourceModification{mod}, records, forma)
	assert.Len(t, got, 1, "a modification with no matching declaration is kept")
}

// A witnessed provider-default move on a resource in the forma stays
// confrontable even though nothing co-owned moved.
func TestRetainConfrontable_KeepsWitnessedProviderDefaultMove(t *testing.T) {
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "s", Type: "AWS::KMS::Key", Label: "key",
		Properties: json.RawMessage(`{"Name":"n"}`),
		Schema:     kmsSchemaForDrift(),
	}}}
	mod := datastore.ResourceModification{
		Stack: "s", Type: "AWS::KMS::Key", Label: "key", Operation: "update", Ksuid: "k1",
		OldProperties: json.RawMessage(`{"Name":"n","EnableKeyRotation":false}`),
		Properties:    json.RawMessage(`{"Name":"n","EnableKeyRotation":true}`),
	}
	got := RetainConfrontable([]datastore.ResourceModification{mod}, nil, forma)
	assert.Len(t, got, 1, "a provider-default move on a declared resource stays confrontable")
}
