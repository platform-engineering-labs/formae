// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A reference expressed in the resolver's dotted-nesting convention
// ("Config.Endpoint") into a property whose whole top-level path a generated
// update removes ("/Config") dangles just as much as a reference to the
// top-level property itself, and must be rejected.
func TestValidateReferencesAgainstRemovals_DottedNestedReferenceIntoRemovedPath_Fails(t *testing.T) {
	producerKsuid := util.NewID()

	updates := []ResourceUpdate{
		{
			Operation: OperationUpdate,
			DesiredState: pkgmodel.Resource{
				Label:         "producer",
				Ksuid:         producerKsuid,
				PatchDocument: json.RawMessage(`[{"op":"remove","path":"/Config"}]`),
			},
		},
	}
	forma := &pkgmodel.Forma{
		Resources: []pkgmodel.Resource{
			{
				Label: "consumer",
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Ref": {"$ref": "formae://%s#/Config.Endpoint", "$value": "e1"}}`,
					producerKsuid)),
			},
		},
	}

	err := validateReferencesAgainstRemovals(updates, forma)
	require.Error(t, err)

	var refErr ReferenceToRemovedPropertyError
	require.True(t, errors.As(err, &refErr), "expected ReferenceToRemovedPropertyError, got: %v", err)
	assert.Equal(t, "consumer", refErr.ConsumerLabel)
	assert.Equal(t, "producer", refErr.SourceLabel)
	assert.Equal(t, "Config.Endpoint", refErr.PropertyPath)
}

// A dotted array-index reference ("Subnets.0") into a property whose whole
// top-level path a generated update removes ("/Subnets") dangles the same
// way, and must be rejected.
func TestValidateReferencesAgainstRemovals_DottedArrayIndexReferenceIntoRemovedPath_Fails(t *testing.T) {
	producerKsuid := util.NewID()

	updates := []ResourceUpdate{
		{
			Operation: OperationUpdate,
			DesiredState: pkgmodel.Resource{
				Label:         "producer",
				Ksuid:         producerKsuid,
				PatchDocument: json.RawMessage(`[{"op":"remove","path":"/Subnets"}]`),
			},
		},
	}
	forma := &pkgmodel.Forma{
		Resources: []pkgmodel.Resource{
			{
				Label: "consumer",
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Ref": {"$ref": "formae://%s#/Subnets.0", "$value": "subnet-a"}}`,
					producerKsuid)),
			},
		},
	}

	err := validateReferencesAgainstRemovals(updates, forma)
	require.Error(t, err)

	var refErr ReferenceToRemovedPropertyError
	require.True(t, errors.As(err, &refErr), "expected ReferenceToRemovedPropertyError, got: %v", err)
	assert.Equal(t, "Subnets.0", refErr.PropertyPath)
}

// Removing one member of a collection (a "remove" op scoped to a single
// element, e.g. "/Tags/1") does not dangle a reference to the collection as a
// whole ("Tags"): the collection itself still exists after the removal.
func TestValidateReferencesAgainstRemovals_MemberRemovalDoesNotDangleCollectionReference(t *testing.T) {
	producerKsuid := util.NewID()

	updates := []ResourceUpdate{
		{
			Operation: OperationUpdate,
			DesiredState: pkgmodel.Resource{
				Label:         "producer",
				Ksuid:         producerKsuid,
				PatchDocument: json.RawMessage(`[{"op":"remove","path":"/Tags/1"}]`),
			},
		},
	}
	forma := &pkgmodel.Forma{
		Resources: []pkgmodel.Resource{
			{
				Label: "consumer",
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Ref": {"$ref": "formae://%s#/Tags", "$value": "[{\"Key\":\"team\",\"Value\":\"x\"}]"}}`,
					producerKsuid)),
			},
		},
	}

	err := validateReferencesAgainstRemovals(updates, forma)
	assert.NoError(t, err, "a member removal must not be mistaken for removal of the collection it belongs to")
}
