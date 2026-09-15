// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
)

type admissionTx struct {
	tx  pgx.Tx
	d   DatastorePostgres
	ctx context.Context
}

func (t admissionTx) Exec(q string, args ...any) error {
	_, err := t.tx.Exec(t.ctx, datastore.AdmissionBind(q, "$"), args...)
	return err
}
func (t admissionTx) Query(q string, args ...any) ([]string, error) {
	rows, err := t.tx.Query(t.ctx, datastore.AdmissionBind(q, "$"), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	out := make([]string, len(rows.FieldDescriptions()))
	dest := make([]any, len(out))
	for i := range out {
		dest[i] = &out[i]
	}
	if err = rows.Scan(dest...); err != nil {
		return nil, err
	}
	return out, rows.Err()
}
func (t admissionTx) Store(c *forma_command.FormaCommand, id string) error {
	return t.d.storeFormaCommandTx(t.ctx, t.tx, c, id, true)
}
func (t admissionTx) Commit() error   { return t.tx.Commit(t.ctx) }
func (t admissionTx) Rollback() error { return t.tx.Rollback(t.ctx) }
func (d DatastorePostgres) admissionStore() datastore.AdmissionStore {
	return datastore.AdmissionStore{Dialect: "postgres", Begin: func() (datastore.AdmissionTransaction, error) {
		ctx := context.Background()
		tx, err := d.pool.Begin(ctx)
		if err != nil {
			return nil, err
		}
		return admissionTx{tx: tx, d: d, ctx: ctx}, nil
	}}
}
func (d DatastorePostgres) ReadAdmissionRevisions(keys []string) ([]datastore.RevisionGuard, error) {
	return d.admissionStore().ReadAdmissionRevisions(keys)
}
func (d DatastorePostgres) LookupCommandAdmission(scope, key string) (*datastore.StoredAdmission, error) {
	return d.admissionStore().LookupCommandAdmission(scope, key)
}
func (d DatastorePostgres) AdmitFormaCommand(c *forma_command.FormaCommand, a datastore.CommandAdmission) (datastore.AdmissionResult, error) {
	return d.admissionStore().AdmitFormaCommand(c, a)
}

var _ datastore.CommandAdmitter = DatastorePostgres{}

func (d DatastorePostgres) ResolveAdmissionStackGuards(labels []string) ([]string, error) {
	return d.admissionStore().ResolveAdmissionStackGuards(labels)
}

var _ datastore.AdmissionScopeResolver = DatastorePostgres{}

func (d DatastorePostgres) ResolveAdmissionTargetInventoryGuards(targets []string) ([]string, error) {
	return d.admissionStore().ResolveAdmissionTargetInventoryGuards(targets)
}

func (d DatastorePostgres) ResolveAdmissionResourceIdentityGuards(ksuids []string) ([]string, error) {
	return d.admissionStore().ResolveAdmissionResourceIdentityGuards(ksuids)
}

var _ datastore.AdmissionPredicateResolver = DatastorePostgres{}

func (d DatastorePostgres) GetResourceObservation(ksuid string) (*datastore.ResourceObservation, error) {
	return d.admissionStore().GetResourceObservation(ksuid)
}

func (d DatastorePostgres) ReadPolicyIdentity(label, stackID string) (*datastore.PolicyIdentity, error) {
	return d.admissionStore().ReadPolicyIdentity(label, stackID)
}

func (d DatastorePostgres) PinCommandTargetIncarnation(commandID, target, incarnation string, refs []datastore.ResourceUpdateRef) error {
	return d.admissionStore().PinCommandTargetIncarnation(commandID, target, incarnation, refs)
}

func (d DatastorePostgres) TryRetireEmptyStack(expectedStackID, label, cleanupCommandID string) (bool, error) {
	return d.admissionStore().TryRetireEmptyStack(expectedStackID, label, cleanupCommandID)
}

var _ datastore.EmptyStackRetirer = DatastorePostgres{}

func (d DatastorePostgres) HasOnlyExternalChanges(ksuid, baselineCommandID, observedVersion string) (bool, error) {
	return d.admissionStore().HasOnlyExternalChanges(ksuid, baselineCommandID, observedVersion)
}
