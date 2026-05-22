// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// makeResourceUpdate constructs a *resource_update.ResourceUpdate suitable for
// childPointsAt tests. The `properties` map is marshalled into DesiredState.Properties
// as raw JSON — both create and delete operations read property values from
// DesiredState (the destroy path treats DesiredState as the snapshot of the
// resource being torn down). Caller may pass nil for `properties` when no
// payload is required (e.g., bare producer in create-phase tests).
//
// The returned ResourceUpdate has:
//   - A generated KSUID and matching URI
//   - DesiredState populated with type, label, and (optional) properties
//   - An empty Schema; callers override Schema.Identifier / Schema.Parent /
//     Schema.ParentMappings as needed.
//
// Lives in this _test.go file (rather than a dedicated test_helpers file)
// because Go test helpers in the same package are reachable from sibling
// _test.go files — future tests (Tasks 2.3, 2.4, 2.5) can call this directly.
func makeResourceUpdate(t *testing.T, resourceType, label string, properties map[string]any) *resource_update.ResourceUpdate {
	t.Helper()

	ksuid := util.NewID()

	var propsJSON json.RawMessage
	if properties != nil {
		b, err := json.Marshal(properties)
		require.NoError(t, err, "marshal properties for %s/%s", resourceType, label)
		propsJSON = b
	}

	return &resource_update.ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Ksuid:      ksuid,
			Label:      label,
			Type:       resourceType,
			Stack:      "test-stack",
			Properties: propsJSON,
		},
		Operation:  resource_update.OperationDelete,
		StackLabel: "test-stack",
	}
}

func TestChildPointsAt_Destroy_SingleMapping_Match(t *testing.T) {
	producer := makeResourceUpdate(t, "AWS::EFS::FileSystem", "grafanaEfs", map[string]any{
		"FileSystemId": "fs-abc123",
	})
	producer.DesiredState.Schema.Identifier = "FileSystemId"

	child := makeResourceUpdate(t, "AWS::EFS::MountTarget", "mtA", map[string]any{
		"FileSystemId": "fs-abc123",
	})
	child.DesiredState.Schema.Parent = "AWS::EFS::FileSystem"
	child.DesiredState.Schema.ParentMappings = []pkgmodel.ParentMapping{
		{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
	}

	ok := childPointsAt(child, producer, resource_update.OperationDelete, nil)
	require.True(t, ok)
}

func TestChildPointsAt_Destroy_SingleMapping_NoMatch(t *testing.T) {
	producer := makeResourceUpdate(t, "AWS::EFS::FileSystem", "grafanaEfs", map[string]any{
		"FileSystemId": "fs-abc123",
	})
	producer.DesiredState.Schema.Identifier = "FileSystemId"

	child := makeResourceUpdate(t, "AWS::EFS::MountTarget", "mtX", map[string]any{
		"FileSystemId": "fs-different", // points at a different FileSystem
	})
	child.DesiredState.Schema.Parent = "AWS::EFS::FileSystem"
	child.DesiredState.Schema.ParentMappings = []pkgmodel.ParentMapping{
		{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
	}

	ok := childPointsAt(child, producer, resource_update.OperationDelete, nil)
	require.False(t, ok)
}

// TestChildPointsAt_EmptyMappings_ReturnsFalse locks in the invariant promised
// by the doc comment: when ParentMappings is empty, instance membership cannot
// be proven and the function conservatively returns false.
func TestChildPointsAt_EmptyMappings_ReturnsFalse(t *testing.T) {
	producer := makeResourceUpdate(t, "AWS::EFS::FileSystem", "fs", map[string]any{"FileSystemId": "fs-1"})
	child := makeResourceUpdate(t, "AWS::EFS::MountTarget", "mt", map[string]any{"FileSystemId": "fs-1"})
	// ParentMappings is left nil/empty
	require.False(t, childPointsAt(child, producer, resource_update.OperationDelete, nil))
}

// TestChildPointsAt_UnknownPhase_ReturnsFalse locks in the default-branch
// behavior: any phase that is neither OperationCreate nor OperationDelete must
// return false. OperationType is a string, so the zero value "" is a safe
// out-of-band value distinct from all real operations.
func TestChildPointsAt_UnknownPhase_ReturnsFalse(t *testing.T) {
	producer := makeResourceUpdate(t, "AWS::EFS::FileSystem", "fs", map[string]any{"FileSystemId": "fs-1"})
	child := makeResourceUpdate(t, "AWS::EFS::MountTarget", "mt", map[string]any{"FileSystemId": "fs-1"})
	child.DesiredState.Schema.Parent = "AWS::EFS::FileSystem"
	child.DesiredState.Schema.ParentMappings = []pkgmodel.ParentMapping{
		{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
	}
	var unknownPhase resource_update.OperationType // zero value, distinct from Create/Delete
	require.False(t, childPointsAt(child, producer, unknownPhase, nil))
}

// TestChildPointsAt_Destroy_MissingChildProperty_ReturnsFalse locks in the
// invariant that destroySideMatches returns false when the child has no value
// under its ChildProperty key — there is nothing to compare against.
func TestChildPointsAt_Destroy_MissingChildProperty_ReturnsFalse(t *testing.T) {
	producer := makeResourceUpdate(t, "AWS::EFS::FileSystem", "fs", map[string]any{"FileSystemId": "fs-1"})
	child := makeResourceUpdate(t, "AWS::EFS::MountTarget", "mt", map[string]any{}) // no FileSystemId
	child.DesiredState.Schema.Parent = "AWS::EFS::FileSystem"
	child.DesiredState.Schema.ParentMappings = []pkgmodel.ParentMapping{
		{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
	}
	require.False(t, childPointsAt(child, producer, resource_update.OperationDelete, nil))
}

// makeResolvable constructs the on-the-wire Resolvable shape that
// translatePropertiesJSON emits and the engine consumes:
//
//	{"$ref": "formae://<KSUID>#/<PropertyPath>", "$value": ""}
//
// Matches the URI format produced by pkgmodel.NewFormaeURI (`formae://ksuid#`
// with `/<path>` appended for property fragments). Used by create-phase tests
// that need a Resolvable value embedded in a child resource's Properties.
func makeResolvable(uri pkgmodel.FormaeURI, propPath string) map[string]any {
	ref := pkgmodel.NewFormaeURI(uri.KSUID(), propPath)
	return map[string]any{
		"$ref":   string(ref),
		"$value": "",
	}
}

// TestChildPointsAt_Create_SingleMapping_Match exercises the create-phase
// happy path: the child's parent-reference property holds a Resolvable
// pointing at the producer, and baseResources resolves the K-side URI back
// to a resource whose Type matches Schema.Parent. Expect a match.
func TestChildPointsAt_Create_SingleMapping_Match(t *testing.T) {
	producer := makeResourceUpdate(t, "AWS::EFS::FileSystem", "grafanaEfs", nil)
	producerURI := producer.URI()

	child := makeResourceUpdate(t, "AWS::EFS::MountTarget", "mtA", map[string]any{
		"FileSystemId": makeResolvable(producerURI, "FileSystemId"),
	})
	child.DesiredState.Schema.Parent = "AWS::EFS::FileSystem"
	child.DesiredState.Schema.ParentMappings = []pkgmodel.ParentMapping{
		{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
	}

	baseRes := map[pkgmodel.FormaeURI]*resource_update.ResourceUpdate{
		producerURI.Stripped(): producer,
	}

	ok := childPointsAt(child, producer, resource_update.OperationCreate, baseRes)
	require.True(t, ok)
}

// TestChildPointsAt_Create_SingleMapping_NoMatch exercises the create-phase
// negative case: the child's parent-reference property points at a different
// producer of the same Schema.Parent type. baseResources knows about both,
// but only one matches. Expect no match against the wrong producer.
func TestChildPointsAt_Create_SingleMapping_NoMatch(t *testing.T) {
	producer := makeResourceUpdate(t, "AWS::EFS::FileSystem", "grafanaEfs", nil)
	other := makeResourceUpdate(t, "AWS::EFS::FileSystem", "lokiEfs", nil)

	child := makeResourceUpdate(t, "AWS::EFS::MountTarget", "mtX", map[string]any{
		"FileSystemId": makeResolvable(other.URI(), "FileSystemId"),
	})
	child.DesiredState.Schema.Parent = "AWS::EFS::FileSystem"
	child.DesiredState.Schema.ParentMappings = []pkgmodel.ParentMapping{
		{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
	}

	baseRes := map[pkgmodel.FormaeURI]*resource_update.ResourceUpdate{
		producer.URI().Stripped(): producer,
		other.URI().Stripped():    other,
	}

	ok := childPointsAt(child, producer, resource_update.OperationCreate, baseRes)
	require.False(t, ok)
}

// TestChildPointsAt_Create_Composite_TaskSetShape exercises the create-phase
// composite-mapping case in its canonical shape: an ECS TaskSet whose parent
// Service is keyed by (ServiceName, Cluster). The type discriminator inside
// createSideMatches must classify the "Service" mapping as a parent reference
// (Resolvable target type == Schema.Parent) and the "Cluster" mapping as a
// sibling reference (target type == AWS::ECS::Cluster), then verify both halves
// — parent URI equality plus shared third-party URI on the Cluster side —
// before declaring a match. Two services sharing a ServiceName but pointing at
// different clusters demonstrate that the Cluster discriminator does its job.
func TestChildPointsAt_Create_Composite_TaskSetShape(t *testing.T) {
	cluster1 := makeResourceUpdate(t, "AWS::ECS::Cluster", "c1", nil)
	cluster2 := makeResourceUpdate(t, "AWS::ECS::Cluster", "c2", nil)

	svc1 := makeResourceUpdate(t, "AWS::ECS::Service", "S1", map[string]any{
		"ServiceName": "foo",
		"Cluster":     makeResolvable(cluster1.URI(), "ClusterName"),
	})
	svc1.DesiredState.Schema.Identifier = "Ref"

	svc2 := makeResourceUpdate(t, "AWS::ECS::Service", "S2", map[string]any{
		"ServiceName": "foo",
		"Cluster":     makeResolvable(cluster2.URI(), "ClusterName"),
	})
	svc2.DesiredState.Schema.Identifier = "Ref"

	ts1 := makeResourceUpdate(t, "AWS::ECS::TaskSet", "TS1", map[string]any{
		"Service": makeResolvable(svc1.URI(), "ServiceName"),
		"Cluster": makeResolvable(cluster1.URI(), "ClusterName"),
	})
	ts1.DesiredState.Schema.Parent = "AWS::ECS::Service"
	ts1.DesiredState.Schema.ParentMappings = []pkgmodel.ParentMapping{
		{ParentProperty: "ServiceName", ChildProperty: "Service"},
		{ParentProperty: "Cluster", ChildProperty: "Cluster"},
	}

	baseRes := map[pkgmodel.FormaeURI]*resource_update.ResourceUpdate{
		cluster1.URI().Stripped(): cluster1,
		cluster2.URI().Stripped(): cluster2,
		svc1.URI().Stripped():     svc1,
		svc2.URI().Stripped():     svc2,
	}

	// Matches svc1: Service mapping resolves to svc1's URI (parent reference),
	// Cluster mapping resolves to cluster1 on both sides (sibling reference).
	require.True(t, childPointsAt(ts1, svc1, resource_update.OperationCreate, baseRes))
	// Does NOT match svc2: Service mapping would point at svc1, not svc2 — the
	// parent-reference half fails before the Cluster check is even reached.
	require.False(t, childPointsAt(ts1, svc2, resource_update.OperationCreate, baseRes))
}

// TestChildPointsAt_Create_LiteralChildProperty_ReturnsFalse locks in the
// extractResolvableURI early-return: when the child's parent-reference property
// holds a literal rather than a Resolvable object, createSideMatches must
// decline rather than treat the literal as a URI. Removing the IsObject() guard
// in extractResolvableURI would silently regress without this test.
func TestChildPointsAt_Create_LiteralChildProperty_ReturnsFalse(t *testing.T) {
	producer := makeResourceUpdate(t, "AWS::EFS::FileSystem", "fs", nil)
	child := makeResourceUpdate(t, "AWS::EFS::MountTarget", "mt", map[string]any{
		"FileSystemId": "fs-literal-abc", // literal string, not a Resolvable
	})
	child.DesiredState.Schema.Parent = "AWS::EFS::FileSystem"
	child.DesiredState.Schema.ParentMappings = []pkgmodel.ParentMapping{
		{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
	}
	baseRes := map[pkgmodel.FormaeURI]*resource_update.ResourceUpdate{
		producer.URI().Stripped(): producer,
	}
	require.False(t, childPointsAt(child, producer, resource_update.OperationCreate, baseRes))
}

// TestChildPointsAt_Destroy_Composite_MatchesOnlyCorrectInstance exercises the
// AND-of-mappings semantics of composite parent mappings. A TaskSet under
// ECS::Service is identified by (ServiceName, Cluster). Two services share the
// same ServiceName but live in different clusters; the TaskSet must match only
// its own parent, distinguished by Cluster.
func TestChildPointsAt_Destroy_Composite_MatchesOnlyCorrectInstance(t *testing.T) {
	// Two Services with same ServiceName but different Cluster
	svc1 := makeResourceUpdate(t, "AWS::ECS::Service", "S1", map[string]any{
		"Ref":         "arn:aws:ecs:.../foo/c1",
		"ServiceName": "foo",
		"Cluster":     "cluster-1",
	})
	svc1.DesiredState.Schema.Identifier = "Ref"

	svc2 := makeResourceUpdate(t, "AWS::ECS::Service", "S2", map[string]any{
		"Ref":         "arn:aws:ecs:.../foo/c2",
		"ServiceName": "foo",
		"Cluster":     "cluster-2",
	})
	svc2.DesiredState.Schema.Identifier = "Ref"

	// TaskSet under svc1
	ts1 := makeResourceUpdate(t, "AWS::ECS::TaskSet", "TS1", map[string]any{
		"Service": "foo",
		"Cluster": "cluster-1",
	})
	ts1.DesiredState.Schema.Parent = "AWS::ECS::Service"
	ts1.DesiredState.Schema.ParentMappings = []pkgmodel.ParentMapping{
		{ParentProperty: "ServiceName", ChildProperty: "Service"},
		{ParentProperty: "Cluster", ChildProperty: "Cluster"},
	}

	// Matches svc1 (own parent)
	require.True(t, childPointsAt(ts1, svc1, resource_update.OperationDelete, nil))
	// Does NOT match svc2 (same ServiceName, different Cluster)
	require.False(t, childPointsAt(ts1, svc2, resource_update.OperationDelete, nil))
}
