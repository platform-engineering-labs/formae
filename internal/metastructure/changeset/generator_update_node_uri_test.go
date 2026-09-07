// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package changeset

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A GeneratorUpdate's NodeURI must never collide with a resource operation
// URI (createOperationURI's "<ksuid>/<propertyPath>/<operation>", no scheme)
// or a target operation URI (target_update's "target://<label>/<operation>"),
// since all three share the single ExecutionDAG.Nodes keyspace. This test
// builds all three through their real, in-package construction paths — not
// hand-rolled string copies — so it fails if any of the three formats ever
// changes in a way that could reopen a collision.
func TestGeneratorUpdateNodeURIDoesNotCollideWithResourceOrTargetOperationURI(t *testing.T) {
	resourceKsuid := util.NewID()

	resourceOps := []types.OperationType{
		types.OperationCreate, types.OperationUpdate, types.OperationDelete,
		types.OperationRead, types.OperationReplace,
	}
	var resourceOpURIs []pkgmodel.FormaeURI
	for _, op := range resourceOps {
		ru := resource_update.ResourceUpdate{
			DesiredState: pkgmodel.Resource{Ksuid: resourceKsuid},
			Operation:    op,
		}
		resourceOpURIs = append(resourceOpURIs, createOperationURI(ru.URI(), ru.Operation))
	}

	targetOps := []types.OperationType{
		target_update.TargetOperationCreate, target_update.TargetOperationUpdate,
		target_update.TargetOperationDelete, target_update.TargetOperationResolve,
	}
	var targetOpURIs []pkgmodel.FormaeURI
	for _, op := range targetOps {
		tu := target_update.TargetUpdate{
			Target:    pkgmodel.Target{Label: "db-password"},
			Operation: op,
		}
		targetOpURIs = append(targetOpURIs, tu.NodeURI())
	}

	generatorOps := []types.OperationType{
		types.OperationCreate, types.OperationUpdate, types.OperationDelete,
	}
	var generatorOpURIs []pkgmodel.FormaeURI
	for _, op := range generatorOps {
		gu := generator_update.GeneratorUpdate{
			Generator:  &pkgmodel.PasswordGenerator{Label: "db-password"},
			StackLabel: "default",
			Operation:  op,
		}
		generatorOpURIs = append(generatorOpURIs, gu.NodeURI())
	}

	for _, gURI := range generatorOpURIs {
		for _, rURI := range resourceOpURIs {
			assert.NotEqual(t, rURI, gURI, "generator node URI collided with a resource operation URI")
		}
		for _, tURI := range targetOpURIs {
			assert.NotEqual(t, tURI, gURI, "generator node URI collided with a target operation URI")
		}
	}
}
