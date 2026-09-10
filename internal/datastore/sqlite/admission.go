// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package sqlite

import (
	"context"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
)

func (d DatastoreSQLite) admissionStore() datastore.AdmissionStore {
	return datastore.AdmissionStore{Dialect: "sqlite", Begin: func() (datastore.AdmissionTransaction, error) {
		tx, err := d.conn.BeginTx(context.Background(), nil)
		if err != nil {
			return nil, err
		}
		return datastore.SQLAdmissionTransaction{Tx: tx, Prefix: "?", StoreCommand: func(c *forma_command.FormaCommand, id string) error { return d.storeFormaCommandTx(tx, c, id, true) }}, nil
	}}
}
func (d DatastoreSQLite) ReadAdmissionRevisions(keys []string) ([]datastore.RevisionGuard, error) {
	return d.admissionStore().ReadAdmissionRevisions(keys)
}
func (d DatastoreSQLite) LookupCommandAdmission(scope, key string) (*datastore.StoredAdmission, error) {
	return d.admissionStore().LookupCommandAdmission(scope, key)
}
func (d DatastoreSQLite) AdmitFormaCommand(c *forma_command.FormaCommand, a datastore.CommandAdmission) (datastore.AdmissionResult, error) {
	return d.admissionStore().AdmitFormaCommand(c, a)
}

var _ datastore.CommandAdmitter = DatastoreSQLite{}

func (d DatastoreSQLite) ResolveAdmissionStackGuards(labels []string) ([]string, error) {
	return d.admissionStore().ResolveAdmissionStackGuards(labels)
}

var _ datastore.AdmissionScopeResolver = DatastoreSQLite{}

func (d DatastoreSQLite) ResolveAdmissionTargetInventoryGuards(targets []string) ([]string, error) {
	return d.admissionStore().ResolveAdmissionTargetInventoryGuards(targets)
}

func (d DatastoreSQLite) ResolveAdmissionResourceIdentityGuards(ksuids []string) ([]string, error) {
	return d.admissionStore().ResolveAdmissionResourceIdentityGuards(ksuids)
}

var _ datastore.AdmissionPredicateResolver = DatastoreSQLite{}

func (d DatastoreSQLite) GetResourceObservation(ksuid string) (*datastore.ResourceObservation, error) {
	return d.admissionStore().GetResourceObservation(ksuid)
}

func (d DatastoreSQLite) ReadPolicyIdentity(label, stackID string) (*datastore.PolicyIdentity, error) {
	return d.admissionStore().ReadPolicyIdentity(label, stackID)
}

func (d DatastoreSQLite) PinCommandTargetIncarnation(commandID, target, incarnation string, refs []datastore.ResourceUpdateRef) error {
	return d.admissionStore().PinCommandTargetIncarnation(commandID, target, incarnation, refs)
}

func (d DatastoreSQLite) TryRetireEmptyStack(expectedStackID, label, cleanupCommandID string) (bool, error) {
	return d.admissionStore().TryRetireEmptyStack(expectedStackID, label, cleanupCommandID)
}

var _ datastore.EmptyStackRetirer = DatastoreSQLite{}
