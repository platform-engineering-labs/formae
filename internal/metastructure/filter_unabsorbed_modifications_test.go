// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// RFC-0041 reconcile-with-drift-and-rename: a modification recorded against
// the OLD label must be considered absorbed when the forma renames that
// resource (declares `alias = <old>`). Without alias-awareness the modification
// shows up as unabsorbed and reconcile rejects the apply.
func TestFilterUnabsorbedModifications_RenameAbsorbsOldLabelModification(t *testing.T) {
	mods := []datastore.ResourceModification{
		{Stack: "prod", Type: "AWS::EC2::Instance", Label: "web-server", Operation: "update"},
	}
	forma := &pkgmodel.Forma{
		Resources: []pkgmodel.Resource{
			{Stack: "prod", Type: "AWS::EC2::Instance", Label: "app-server", Alias: "web-server"},
		},
	}
	fa := &forma_command.FormaCommand{}

	got := filterUnabsorbedModifications(mods, forma, fa)
	assert.Empty(t, got, "modification under old label must be absorbed by alias")
}

// Sanity: a modification with no matching forma resource (no label, no alias)
// remains unabsorbed and the reconcile guard still fires.
func TestFilterUnabsorbedModifications_ForeignModificationStillUnabsorbed(t *testing.T) {
	mods := []datastore.ResourceModification{
		{Stack: "prod", Type: "AWS::EC2::Instance", Label: "orphan", Operation: "update"},
	}
	forma := &pkgmodel.Forma{
		Resources: []pkgmodel.Resource{
			{Stack: "prod", Type: "AWS::EC2::Instance", Label: "app-server", Alias: "web-server"},
		},
	}
	fa := &forma_command.FormaCommand{}

	got := filterUnabsorbedModifications(mods, forma, fa)
	assert.Len(t, got, 1, "unrelated modification must remain unabsorbed")
}

// A modification keyed by the NEW label paired with a ResourceUpdate against
// that same key remains unabsorbed — the user is explicitly applying the
// drift, which is the existing pre-RFC-0041 behaviour. Verifies the alias
// path doesn't accidentally swallow modifications that have a real update.
func TestFilterUnabsorbedModifications_ModificationWithPendingUpdateIsUnabsorbed(t *testing.T) {
	mods := []datastore.ResourceModification{
		{Stack: "prod", Type: "AWS::EC2::Instance", Label: "app-server", Operation: "update"},
	}
	forma := &pkgmodel.Forma{
		Resources: []pkgmodel.Resource{
			{Stack: "prod", Type: "AWS::EC2::Instance", Label: "app-server"},
		},
	}
	fa := &forma_command.FormaCommand{
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				StackLabel:   "prod",
				DesiredState: pkgmodel.Resource{Type: "AWS::EC2::Instance", Label: "app-server"},
			},
		},
	}

	got := filterUnabsorbedModifications(mods, forma, fa)
	assert.Len(t, got, 1, "a modification with a pending update is not absorbed")
}

// Cross-type guard: same label on a different type is NOT absorbed by an
// unrelated alias hit.
func TestFilterUnabsorbedModifications_AliasMatchRequiresTypeMatch(t *testing.T) {
	mods := []datastore.ResourceModification{
		{Stack: "prod", Type: "AWS::S3::Bucket", Label: "web-server", Operation: "update"},
	}
	forma := &pkgmodel.Forma{
		Resources: []pkgmodel.Resource{
			{Stack: "prod", Type: "AWS::EC2::Instance", Label: "app-server", Alias: "web-server"},
		},
	}
	fa := &forma_command.FormaCommand{}

	got := filterUnabsorbedModifications(mods, forma, fa)
	assert.Len(t, got, 1, "alias match must require matching Type")
}
