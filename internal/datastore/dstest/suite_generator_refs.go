// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/demula/mksuid/v2"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// genBoundProperties returns a persisted resource's properties document with
// one property bound to the given generator through a translated $gen
// envelope — the shape a resource carries after translation and resolution,
// with the opaque value stored as a digest rather than plaintext.
func genBoundProperties(property, generatorKsuid string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(
		`{"%s":{"$gen":true,"$generator":"%s","$output":"value","$visibility":"Opaque","$value":"sha256:digest","$hashed":true,"$resolvedFrom":"sha256:digest"}}`,
		property, generatorKsuid,
	))
}

// createGeneratorRefsTarget creates the target every resource in this suite is
// stored against.
func createGeneratorRefsTarget(t *testing.T, td TestDatastore) {
	t.Helper()
	_, err := td.CreateTarget(&pkgmodel.Target{
		Label:     "test-target",
		Namespace: "AWS",
		Config:    json.RawMessage(`{}`),
	})
	require.NoError(t, err)
}

// RunFindResourcesReferencingGenerator verifies the basic lookup: a resource
// whose property is bound to the generator is returned.
func RunFindResourcesReferencingGenerator(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesReferencingGenerator", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck
		createGeneratorRefsTarget(t, td)

		generatorKsuid := mksuid.New().String()
		boundKsuid := mksuid.New().String()
		_, err := ds.StoreResource(&pkgmodel.Resource{
			Ksuid: boundKsuid, NativeID: "bound-native", Stack: "app", Label: "database",
			Type: "AWS::RDS::DBInstance", Target: "test-target",
			Properties: genBoundProperties("MasterUserPassword", generatorKsuid),
		}, "cmd-1")
		require.NoError(t, err)

		bound, err := ds.FindResourcesReferencingGenerator(generatorKsuid)
		require.NoError(t, err)
		require.Len(t, bound, 1)
		assert.Equal(t, boundKsuid, bound[0].Ksuid)
		assert.Equal(t, "database", bound[0].Label)
	})
}

// RunFindResourcesReferencingGeneratorOtherGeneratorExcluded verifies that a
// resource bound to a different generator is not returned.
func RunFindResourcesReferencingGeneratorOtherGeneratorExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesReferencingGenerator_OtherGeneratorExcluded", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck
		createGeneratorRefsTarget(t, td)

		wantedGenerator := mksuid.New().String()
		otherGenerator := mksuid.New().String()

		boundKsuid := mksuid.New().String()
		_, err := ds.StoreResource(&pkgmodel.Resource{
			Ksuid: boundKsuid, NativeID: "wanted-native", Stack: "app", Label: "wanted",
			Type: "AWS::RDS::DBInstance", Target: "test-target",
			Properties: genBoundProperties("MasterUserPassword", wantedGenerator),
		}, "cmd-1")
		require.NoError(t, err)

		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: mksuid.New().String(), NativeID: "other-native", Stack: "app", Label: "other",
			Type: "AWS::RDS::DBInstance", Target: "test-target",
			Properties: genBoundProperties("MasterUserPassword", otherGenerator),
		}, "cmd-1")
		require.NoError(t, err)

		bound, err := ds.FindResourcesReferencingGenerator(wantedGenerator)
		require.NoError(t, err)
		require.Len(t, bound, 1, "only the resource bound to the queried generator may be returned")
		assert.Equal(t, boundKsuid, bound[0].Ksuid)
	})
}

// RunFindResourcesReferencingGeneratorUnboundResourceExcluded verifies that a
// resource carrying no $gen envelope at all is not returned.
func RunFindResourcesReferencingGeneratorUnboundResourceExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesReferencingGenerator_UnboundResourceExcluded", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck
		createGeneratorRefsTarget(t, td)

		generatorKsuid := mksuid.New().String()
		_, err := ds.StoreResource(&pkgmodel.Resource{
			Ksuid: mksuid.New().String(), NativeID: "plain-native", Stack: "app", Label: "plain-bucket",
			Type: "AWS::S3::Bucket", Target: "test-target",
			Properties: json.RawMessage(`{"BucketName":"plain"}`),
		}, "cmd-1")
		require.NoError(t, err)

		bound, err := ds.FindResourcesReferencingGenerator(generatorKsuid)
		require.NoError(t, err)
		assert.Empty(t, bound)
	})
}

// RunFindResourcesReferencingGeneratorAcrossStacks verifies that every
// destination of one generator is returned regardless of which stack it lives
// on. A generator is bound by KSUID, so a consumer in another stack is as much
// a destination as one in the generator's own stack, and the whole reverse
// index exists to find it.
func RunFindResourcesReferencingGeneratorAcrossStacks(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesReferencingGenerator_AcrossStacks", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck
		createGeneratorRefsTarget(t, td)

		generatorKsuid := mksuid.New().String()

		firstKsuid := mksuid.New().String()
		_, err := ds.StoreResource(&pkgmodel.Resource{
			Ksuid: firstKsuid, NativeID: "stack-a-native", Stack: "stack-a", Label: "database",
			Type: "AWS::RDS::DBInstance", Target: "test-target",
			Properties: genBoundProperties("MasterUserPassword", generatorKsuid),
		}, "cmd-1")
		require.NoError(t, err)

		secondKsuid := mksuid.New().String()
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: secondKsuid, NativeID: "stack-b-native", Stack: "stack-b", Label: "worker",
			Type: "AWS::ECS::Service", Target: "test-target",
			Properties: genBoundProperties("DatabasePassword", generatorKsuid),
		}, "cmd-1")
		require.NoError(t, err)

		bound, err := ds.FindResourcesReferencingGenerator(generatorKsuid)
		require.NoError(t, err)
		require.Len(t, bound, 2, "a generator's destinations span stacks")

		got := []string{bound[0].Ksuid, bound[1].Ksuid}
		assert.ElementsMatch(t, []string{firstKsuid, secondKsuid}, got)
	})
}

// RunFindResourcesReferencingGeneratorDeletedExcluded verifies that a deleted
// destination is not returned: a torn-down resource no longer binds anything.
func RunFindResourcesReferencingGeneratorDeletedExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesReferencingGenerator_DeletedExcluded", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck
		createGeneratorRefsTarget(t, td)

		generatorKsuid := mksuid.New().String()
		destination := &pkgmodel.Resource{
			Ksuid: mksuid.New().String(), NativeID: "deleted-native", Stack: "app", Label: "database",
			Type: "AWS::RDS::DBInstance", Target: "test-target",
			Properties: genBoundProperties("MasterUserPassword", generatorKsuid),
		}
		_, err := ds.StoreResource(destination, "cmd-1")
		require.NoError(t, err)

		_, err = ds.DeleteResource(destination, "cmd-2")
		require.NoError(t, err)

		bound, err := ds.FindResourcesReferencingGenerator(generatorKsuid)
		require.NoError(t, err)
		assert.Empty(t, bound, "a deleted destination is not a destination")
	})
}

// RunFindResourcesReferencingGeneratorLatestVersionOnly verifies the
// latest-version window: a resource written twice is returned exactly once, at
// its current version, not once per stored version.
func RunFindResourcesReferencingGeneratorLatestVersionOnly(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesReferencingGenerator_LatestVersionOnly", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck
		createGeneratorRefsTarget(t, td)

		generatorKsuid := mksuid.New().String()
		destinationKsuid := mksuid.New().String()

		firstProps := json.RawMessage(fmt.Sprintf(
			`{"Engine":"postgres","MasterUserPassword":{"$gen":true,"$generator":"%s","$output":"value","$visibility":"Opaque","$value":"sha256:first","$hashed":true,"$resolvedFrom":"sha256:first"}}`,
			generatorKsuid,
		))
		_, err := ds.StoreResource(&pkgmodel.Resource{
			Ksuid: destinationKsuid, NativeID: "versioned-native", Stack: "app", Label: "database",
			Type: "AWS::RDS::DBInstance", Target: "test-target", Properties: firstProps,
		}, "cmd-1")
		require.NoError(t, err)

		secondProps := json.RawMessage(fmt.Sprintf(
			`{"Engine":"mysql","MasterUserPassword":{"$gen":true,"$generator":"%s","$output":"value","$visibility":"Opaque","$value":"sha256:second","$hashed":true,"$resolvedFrom":"sha256:second"}}`,
			generatorKsuid,
		))
		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: destinationKsuid, NativeID: "versioned-native", Stack: "app", Label: "database",
			Type: "AWS::RDS::DBInstance", Target: "test-target", Properties: secondProps,
		}, "cmd-2")
		require.NoError(t, err)

		bound, err := ds.FindResourcesReferencingGenerator(generatorKsuid)
		require.NoError(t, err)
		require.Len(t, bound, 1, "a superseded version must not be returned alongside the live one")
		assert.Equal(t, destinationKsuid, bound[0].Ksuid)
		assert.JSONEq(t, string(secondProps), string(bound[0].Properties),
			"the returned version must be the live one, not the superseded one")
	})
}

// RunFindResourcesReferencingGeneratorUnknownGenerator verifies that querying
// a generator KSUID nothing is bound to returns an empty result rather than an
// error.
func RunFindResourcesReferencingGeneratorUnknownGenerator(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesReferencingGenerator_UnknownGenerator", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck
		createGeneratorRefsTarget(t, td)

		_, err := ds.StoreResource(&pkgmodel.Resource{
			Ksuid: mksuid.New().String(), NativeID: "unknown-gen-native", Stack: "app", Label: "database",
			Type: "AWS::RDS::DBInstance", Target: "test-target",
			Properties: genBoundProperties("MasterUserPassword", mksuid.New().String()),
		}, "cmd-1")
		require.NoError(t, err)

		bound, err := ds.FindResourcesReferencingGenerator(mksuid.New().String())
		require.NoError(t, err)
		assert.Empty(t, bound)
	})
}

// RunFindResourcesReferencingGeneratorNestedInArray verifies that a $gen
// envelope inside an array element is found, not only one at the top level of
// the properties document.
func RunFindResourcesReferencingGeneratorNestedInArray(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesReferencingGenerator_NestedInArray", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck
		createGeneratorRefsTarget(t, td)

		generatorKsuid := mksuid.New().String()
		destinationKsuid := mksuid.New().String()
		props := json.RawMessage(fmt.Sprintf(
			`{"Environment":[{"Name":"DB_HOST","Value":"db.internal"},{"Name":"DB_PASSWORD","Value":{"$gen":true,"$generator":"%s","$output":"value","$visibility":"Opaque","$value":"sha256:digest","$hashed":true,"$resolvedFrom":"sha256:digest"}}]}`,
			generatorKsuid,
		))
		_, err := ds.StoreResource(&pkgmodel.Resource{
			Ksuid: destinationKsuid, NativeID: "nested-native", Stack: "app", Label: "task-definition",
			Type: "AWS::ECS::TaskDefinition", Target: "test-target", Properties: props,
		}, "cmd-1")
		require.NoError(t, err)

		bound, err := ds.FindResourcesReferencingGenerator(generatorKsuid)
		require.NoError(t, err)
		require.Len(t, bound, 1, "a $gen nested in an array element must be found")
		assert.Equal(t, destinationKsuid, bound[0].Ksuid)
	})
}

// swapKsuidCase returns the KSUID with the case of every ASCII letter flipped.
// KSUIDs are base62, so the result is a different string that differs from the
// original only in case.
func swapKsuidCase(t *testing.T, ksuid string) string {
	t.Helper()
	swapped := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z':
			return r - 'A' + 'a'
		default:
			return r
		}
	}, ksuid)
	require.NotEqual(t, ksuid, swapped, "the KSUID must contain at least one letter to case-swap")
	return swapped
}

// RunFindResourcesReferencingGeneratorBareGeneratorKeyExcluded verifies that a
// $generator key that is not part of a $gen envelope does not make its owner a
// destination. Only a translated envelope binds a property to a generator; a
// property that merely happens to carry a key of that name does not.
func RunFindResourcesReferencingGeneratorBareGeneratorKeyExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesReferencingGenerator_BareGeneratorKeyExcluded", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck
		createGeneratorRefsTarget(t, td)

		generatorKsuid := mksuid.New().String()
		_, err := ds.StoreResource(&pkgmodel.Resource{
			Ksuid: mksuid.New().String(), NativeID: "bare-native", Stack: "app", Label: "bare",
			Type: "AWS::S3::Bucket", Target: "test-target",
			Properties: json.RawMessage(fmt.Sprintf(`{"Config":{"$generator":"%s"}}`, generatorKsuid)),
		}, "cmd-1")
		require.NoError(t, err)

		bound, err := ds.FindResourcesReferencingGenerator(generatorKsuid)
		require.NoError(t, err)
		assert.Empty(t, bound, "a $generator key outside a $gen envelope does not bind a property")
	})
}

// RunFindResourcesReferencingGeneratorGenFalseExcluded verifies that an object
// carrying $gen: false is not an envelope, so its $generator sibling binds
// nothing.
func RunFindResourcesReferencingGeneratorGenFalseExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesReferencingGenerator_GenFalseExcluded", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck
		createGeneratorRefsTarget(t, td)

		generatorKsuid := mksuid.New().String()
		_, err := ds.StoreResource(&pkgmodel.Resource{
			Ksuid: mksuid.New().String(), NativeID: "gen-false-native", Stack: "app", Label: "gen-false",
			Type: "AWS::RDS::DBInstance", Target: "test-target",
			Properties: json.RawMessage(fmt.Sprintf(
				`{"MasterUserPassword":{"$gen":false,"$generator":"%s","$output":"value"}}`, generatorKsuid)),
		}, "cmd-1")
		require.NoError(t, err)

		bound, err := ds.FindResourcesReferencingGenerator(generatorKsuid)
		require.NoError(t, err)
		assert.Empty(t, bound, "$gen: false is not an envelope")
	})
}

// RunFindResourcesReferencingGeneratorCaseSensitive verifies that the lookup
// matches the generator KSUID exactly. KSUIDs are case-sensitive identifiers,
// so a case-swapped KSUID names a different generator and has no destinations.
func RunFindResourcesReferencingGeneratorCaseSensitive(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesReferencingGenerator_CaseSensitive", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck
		createGeneratorRefsTarget(t, td)

		generatorKsuid := mksuid.New().String()
		_, err := ds.StoreResource(&pkgmodel.Resource{
			Ksuid: mksuid.New().String(), NativeID: "case-native", Stack: "app", Label: "database",
			Type: "AWS::RDS::DBInstance", Target: "test-target",
			Properties: genBoundProperties("MasterUserPassword", generatorKsuid),
		}, "cmd-1")
		require.NoError(t, err)

		bound, err := ds.FindResourcesReferencingGenerator(swapKsuidCase(t, generatorKsuid))
		require.NoError(t, err)
		assert.Empty(t, bound, "a case-swapped KSUID names a different generator")
	})
}

// RunFindResourcesReferencingGeneratorResourceRefExcluded verifies that this
// lookup answers only for generators. A resource KSUID reached through a $ref
// is a resource dependency, which FindResourcesDependingOn answers, and it must
// not surface that resource's dependents here.
func RunFindResourcesReferencingGeneratorResourceRefExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("FindResourcesReferencingGenerator_ResourceRefExcluded", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck
		createGeneratorRefsTarget(t, td)

		parentKsuid := mksuid.New().String()
		_, err := ds.StoreResource(&pkgmodel.Resource{
			Ksuid: parentKsuid, NativeID: "parent-native", Stack: "app", Label: "parent",
			Type: "AWS::S3::Bucket", Target: "test-target",
			Properties: json.RawMessage(`{"BucketName":"parent"}`),
		}, "cmd-1")
		require.NoError(t, err)

		_, err = ds.StoreResource(&pkgmodel.Resource{
			Ksuid: mksuid.New().String(), NativeID: "child-native", Stack: "app", Label: "child",
			Type: "AWS::IAM::Role", Target: "test-target",
			Properties: json.RawMessage(fmt.Sprintf(
				`{"RoleArn":{"$ref":"formae://%s#/Arn","$value":"arn:aws:s3:::parent"}}`, parentKsuid)),
		}, "cmd-2")
		require.NoError(t, err)

		bound, err := ds.FindResourcesReferencingGenerator(parentKsuid)
		require.NoError(t, err)
		assert.Empty(t, bound, "a resource KSUID reached through $ref names no generator")
	})
}
