// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package drift

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestDeclaredAcceptanceUsesEffectiveSetOnceDeclaration(t *testing.T) {
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: ":memory:"}}, "test")
	require.NoError(t, err)
	defer ds.Close()
	observed := pkgmodel.Resource{Ksuid: util.NewID(), Label: "resource", Stack: "stack", Target: "target", Type: "test::Resource", Properties: []byte(`{"foo":"current"}`), Schema: pkgmodel.Schema{Fields: []string{"foo"}}}
	_, err = ds.StoreResource(&observed, "observation")
	require.NoError(t, err)
	declared := observed
	declared.Properties = []byte(`{"foo":{"$value":"ignored","$strategy":"SetOnce"}}`)
	command := &forma_command.FormaCommand{}
	err = AddDeclaredAcceptances(ds, map[string][]datastore.ResourceModification{"stack": {{Stack: "stack", Type: observed.Type, Label: observed.Label, Ksuid: observed.Ksuid, Operation: "update"}}}, &pkgmodel.Forma{Resources: []pkgmodel.Resource{declared}}, command)
	require.NoError(t, err)
	require.Len(t, command.ResourceUpdates, 1)
	require.JSONEq(t, `{"foo":"current"}`, string(command.ResourceUpdates[0].DesiredState.Properties))
}

func TestDeclaredAcceptancePreservesAdjacentLargeIntegers(t *testing.T) {
	for _, properties := range []struct{ name, prior, declared string }{
		{"plain", `{"number":9007199254740992}`, `{"number":9007199254740993}`},
		{"nested-reference-transform", `{"reference":{"$ref":"formae://source#/value","$transform":{"arguments":[9007199254740992]}}}`, `{"reference":{"$ref":"formae://source#/value","$transform":{"arguments":[9007199254740993]}}}`},
	} {
		t.Run(properties.name, func(t *testing.T) {
			observed := pkgmodel.Resource{Ksuid: util.NewID(), Label: "resource", Stack: "stack", Target: "target", Type: "test::Resource", Version: "observed", Properties: []byte(properties.declared)}
			prior := datastore.ResourceSnapshot{KSUID: observed.Ksuid, Label: observed.Label, Target: observed.Target, Type: observed.Type, Properties: []byte(properties.prior)}
			ds := acceptanceDeclarationDatastore{prior: prior, observed: &observed}
			command := &forma_command.FormaCommand{}
			err := AddDeclaredAcceptances(ds, map[string][]datastore.ResourceModification{"stack": {{Stack: "stack", Type: observed.Type, Label: observed.Label, Ksuid: observed.Ksuid, Operation: "update"}}}, &pkgmodel.Forma{Resources: []pkgmodel.Resource{observed}}, command)
			require.NoError(t, err)
			require.Len(t, command.ResourceUpdates, 1, "an exact numeric source edit must contribute its desired declaration")
			require.Equal(t, properties.declared, string(command.ResourceUpdates[0].DesiredState.Properties))
			cleaned, err := declarationProperties([]byte(properties.declared))
			require.NoError(t, err)
			require.Contains(t, string(cleaned), "9007199254740993", "normalization must retain the exact number token")
		})
	}
}

type acceptanceDeclarationDatastore struct {
	datastore.Datastore
	prior    datastore.ResourceSnapshot
	observed *pkgmodel.Resource
}

func (d acceptanceDeclarationDatastore) GetResourcesAtLastReconcile(string) ([]datastore.ResourceSnapshot, error) {
	return []datastore.ResourceSnapshot{d.prior}, nil
}
func (d acceptanceDeclarationDatastore) LoadResourcesByStack(string) ([]*pkgmodel.Resource, error) {
	return []*pkgmodel.Resource{d.observed}, nil
}

func TestAcceptedDeclarationReferenceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, prior, declared string
		equal                 bool
	}{
		{"reference-identity", `{"v":{"$ref":"formae://a#/value"}}`, `{"v":{"$ref":"formae://b#/value"}}`, false},
		{"reference-strategy", `{"v":{"$ref":"formae://a#/value","$strategy":"Update"}}`, `{"v":{"$ref":"formae://a#/value","$strategy":"SetOnce"}}`, false},
		{"reference-transform", `{"v":{"$ref":"formae://a#/value","$json":"first"}}`, `{"v":{"$ref":"formae://a#/value","$json":"second"}}`, false},
		{"generator-output", `{"v":{"$gen":true,"$generator":"a","$output":"first"}}`, `{"v":{"$gen":true,"$generator":"a","$output":"second"}}`, false},
		{"generator-execution-only", `{"v":{"$gen":true,"$generator":"a","$output":"value","$visibility":"Opaque","$strategy":"Update","$value":"old-hash","$hashed":true,"$applied":"old-echo","$resolvedFrom":"old-root"}}`, `{"v":{"$gen":true,"$generator":"a","$output":"value","$visibility":"Opaque","$value":"new-hash","$hashed":true,"$applied":"new-echo","$resolvedFrom":"new-root"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			equal, err := sameAcceptedDeclaration(datastore.ResourceSnapshot{Properties: []byte(tc.prior)}, pkgmodel.Resource{Properties: []byte(tc.declared)})
			require.NoError(t, err)
			require.Equal(t, tc.equal, equal)
		})
	}
}

func TestDeclaredAcceptanceNumericEquivalenceWithOpaqueGenerator(t *testing.T) {
	for _, tc := range []struct {
		name, prior, declared string
		changes               int
	}{
		{"equivalent-numeric-spelling", `{"n":1,"g":{"$gen":true,"$generator":"a","$output":"value","$visibility":"Opaque"}}`, `{"n":1.0,"g":{"$gen":true,"$generator":"a","$output":"value","$visibility":"Opaque"}}`, 0},
		{"equivalent-large-number-exponent", `{"n":9007199254740993,"g":{"$gen":true,"$generator":"a","$output":"value","$visibility":"Opaque"}}`, `{"n":9.007199254740993e15,"g":{"$gen":true,"$generator":"a","$output":"value","$visibility":"Opaque"}}`, 0},
		{"distinct-exact-number", `{"n":9007199254740992,"g":{"$gen":true,"$generator":"a","$output":"value","$visibility":"Opaque"}}`, `{"n":9007199254740993,"g":{"$gen":true,"$generator":"a","$output":"value","$visibility":"Opaque"}}`, 1},
		{"generator-identity-edit", `{"n":1,"g":{"$gen":true,"$generator":"a","$output":"value","$visibility":"Opaque"}}`, `{"n":1.0,"g":{"$gen":true,"$generator":"b","$output":"value","$visibility":"Opaque"}}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := pkgmodel.Resource{Ksuid: util.NewID(), Label: "resource", Stack: "stack", Target: "target", Type: "test::Resource", Version: "observed", Properties: []byte(tc.declared)}
			prior := datastore.ResourceSnapshot{KSUID: observed.Ksuid, Label: observed.Label, Target: observed.Target, Type: observed.Type, Properties: []byte(tc.prior)}
			equal, err := sameAcceptedDeclaration(prior, observed)
			require.NoError(t, err)
			if equal != (tc.changes == 0) {
				t.Errorf("declaration equality = %v, want %v", equal, tc.changes == 0)
			}
			command := &forma_command.FormaCommand{}
			err = AddDeclaredAcceptances(acceptanceDeclarationDatastore{prior: prior, observed: &observed}, map[string][]datastore.ResourceModification{"stack": {{Stack: "stack", Type: observed.Type, Label: observed.Label, Ksuid: observed.Ksuid, Operation: "update"}}}, &pkgmodel.Forma{Resources: []pkgmodel.Resource{observed}}, command)
			require.NoError(t, err)
			require.Len(t, command.ResourceUpdates, tc.changes, "numeric spelling and generator execution metadata are not declaration edits")
		})
	}
}

func TestAcceptedDeclarationNumericFallbackPreservesEmptyRoots(t *testing.T) {
	for _, strict := range []bool{false, true} {
		schema := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"items": {PreserveEmptyValues: strict, EdgeKind: pkgmodel.EdgeKindDefault}}}
		prior := datastore.ResourceSnapshot{Schema: schema, Properties: []byte(`{"n":1,"items":[]}`)}
		declared := pkgmodel.Resource{Schema: schema, Properties: []byte(`{"n":1.0}`)}
		equal, err := sameAcceptedDeclaration(prior, declared)
		require.NoError(t, err)
		require.Equal(t, !strict, equal, "preserveEmptyValues=%v must govern the numeric no-op check", strict)
	}
}

func TestAcceptedDeclarationNumericShortcutDoesNotBypassOpaqueStructure(t *testing.T) {
	for _, tc := range []struct{ name, prior, declared string }{
		{"array-order", `{"secret":[1,2],"n":1}`, `{"secret":[2,1],"n":1.0}`},
		{"empty-vs-absent", `{"n":1}`, `{"secret":[],"n":1.0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"secret": {Opaque: true, EdgeKind: pkgmodel.EdgeKindDefault}}}
			equal, err := sameAcceptedDeclaration(datastore.ResourceSnapshot{Schema: schema, Properties: []byte(tc.prior)}, pkgmodel.Resource{Schema: schema, Properties: []byte(tc.declared)})
			require.NoError(t, err)
			require.False(t, equal, "numeric equivalence must not bypass opaque structural changes")
		})
	}
}

func (d acceptanceDeclarationDatastore) GetResourceObservation(string) (*datastore.ResourceObservation, error) {
	return &datastore.ResourceObservation{KSUID: d.observed.Ksuid, Stack: d.observed.Stack, Target: d.observed.Target, Version: d.observed.Version, Operation: "update", Resource: d.observed}, nil
}

func TestDeclaredAcceptancePinsCurrentObservation(t *testing.T) {
	current := &pkgmodel.Resource{Ksuid: "r", Version: "current", Stack: "s", Target: "t", Type: "test::Resource", Label: "r", Properties: []byte(`{"x":2}`)}
	ds := staleAcceptanceIndex{acceptanceDeclarationDatastore{observed: current}}
	command := &forma_command.FormaCommand{}
	err := AddDeclaredAcceptances(ds, map[string][]datastore.ResourceModification{"s": {{Stack: "s", Type: current.Type, Label: "r", Ksuid: "r", Operation: "update"}}}, &pkgmodel.Forma{Resources: []pkgmodel.Resource{*current}}, command)
	require.NoError(t, err)
	require.Len(t, command.ResourceUpdates, 1)
	require.Equal(t, "current", command.ResourceUpdates[0].Version)
}

type staleAcceptanceIndex struct{ acceptanceDeclarationDatastore }

func (d staleAcceptanceIndex) LoadResourcesByStack(string) ([]*pkgmodel.Resource, error) {
	r := *d.observed
	r.Version = "stale"
	return []*pkgmodel.Resource{&r}, nil
}

func TestDeclaredAcceptanceKeepsPreviouslyAcceptedOwnership(t *testing.T) {
	observed := pkgmodel.Resource{Ksuid: "r", Label: "r", Stack: "stack", Target: "t", Type: "Test::Resource", Version: "observed", Properties: json.RawMessage(`{"name":"new"}`), OwnedMembers: pkgmodel.OwnedMembers{"tags": {Rule: "Mapping", Members: []string{"old"}}}}
	declaration := observed
	declaration.Properties = json.RawMessage(`{"name":"old"}`)
	declaration.OwnedMembers = pkgmodel.OwnedMembers{"tags": {Rule: "Mapping", Members: []string{"accepted"}}}
	prior := datastore.ResourceSnapshot{KSUID: "r", Properties: declaration.Properties, Schema: declaration.Schema, Declaration: &declaration}
	command := &forma_command.FormaCommand{}
	err := AddDeclaredAcceptances(acceptanceDeclarationDatastore{prior: prior, observed: &observed}, map[string][]datastore.ResourceModification{"stack": {{Stack: "stack", Type: observed.Type, Label: "r", Ksuid: "r", Operation: "update"}}}, &pkgmodel.Forma{Resources: []pkgmodel.Resource{observed}}, command)
	require.NoError(t, err)
	require.Len(t, command.ResourceUpdates, 1)
	require.Equal(t, declaration.OwnedMembers, command.ResourceUpdates[0].DesiredState.OwnedMembers)
}
