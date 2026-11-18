// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package aurora

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata/types"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
)

type admissionTx struct {
	id   string
	d    *DatastoreAuroraDataAPI
	ctx  context.Context
	done bool
}

func admissionParams(args []any) []types.SqlParameter {
	out := make([]types.SqlParameter, len(args))
	for i, arg := range args {
		out[i] = types.SqlParameter{Name: aws.String(fmt.Sprintf("p%d", i+1)), Value: &types.FieldMemberStringValue{Value: fmt.Sprint(arg)}}
	}
	return out
}
func (t *admissionTx) Exec(q string, args ...any) error {
	_, err := t.d.executeStatementInTransaction(t.ctx, t.id, datastore.AdmissionBind(q, ":p"), admissionParams(args))
	return err
}
func (t *admissionTx) ExecAffected(q string, args ...any) (int64, error) {
	result, err := t.d.executeStatementInTransaction(t.ctx, t.id, datastore.AdmissionBind(q, ":p"), admissionParams(args))
	if err != nil {
		return 0, err
	}
	return result.NumberOfRecordsUpdated, nil
}
func (t *admissionTx) Query(q string, args ...any) ([]string, error) {
	rows, err := t.QueryAll(q, args...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}
func (t *admissionTx) QueryAll(q string, args ...any) ([][]string, error) {
	result, err := t.d.executeStatementInTransaction(t.ctx, t.id, datastore.AdmissionBind(q, ":p"), admissionParams(args))
	if err != nil {
		return nil, err
	}
	all := make([][]string, 0, len(result.Records))
	for _, record := range result.Records {
		out := make([]string, len(record))
		for i, field := range record {
			v, ok := field.(*types.FieldMemberStringValue)
			if !ok {
				return nil, fmt.Errorf("unexpected admission field %T", field)
			}
			out[i] = v.Value
		}
		all = append(all, out)
	}
	return all, nil
}
func (t *admissionTx) Store(c *forma_command.FormaCommand, id string) error {
	return t.d.storeFormaCommandTx(t.ctx, t.id, c, id, true)
}
func (t *admissionTx) Commit() error {
	err := t.d.commitTransaction(t.ctx, t.id)
	if err == nil {
		t.done = true
	}
	return err
}
func (t *admissionTx) Rollback() error {
	if t.done {
		return nil
	}
	return t.d.rollbackTransaction(t.ctx, t.id)
}
func (d *DatastoreAuroraDataAPI) admissionStore() datastore.AdmissionStore {
	return datastore.AdmissionStore{Dialect: "postgres", Begin: func() (datastore.AdmissionTransaction, error) {
		ctx := context.Background()
		id, err := d.beginTransaction(ctx)
		if err != nil {
			return nil, err
		}
		return &admissionTx{id: id, d: d, ctx: ctx}, nil
	}}
}
func (d *DatastoreAuroraDataAPI) ReadAdmissionRevisions(keys []string) ([]datastore.RevisionGuard, error) {
	return d.admissionStore().ReadAdmissionRevisions(keys)
}
func (d *DatastoreAuroraDataAPI) LookupCommandAdmission(scope, key string) (*datastore.StoredAdmission, error) {
	return d.admissionStore().LookupCommandAdmission(scope, key)
}
func (d *DatastoreAuroraDataAPI) AdmitFormaCommand(c *forma_command.FormaCommand, a datastore.CommandAdmission) (datastore.AdmissionResult, error) {
	return d.admissionStore().AdmitFormaCommand(c, a)
}
func (d *DatastoreAuroraDataAPI) ConflictingCommandIDsForStacks(labels []string) ([]string, error) {
	return d.admissionStore().ConflictingCommandIDsForStacks(labels)
}
func (d *DatastoreAuroraDataAPI) FailOrphanedCommands() (int, error) {
	return d.admissionStore().FailOrphanedCommands()
}

var _ datastore.CommandAdmitter = (*DatastoreAuroraDataAPI)(nil)

func (d *DatastoreAuroraDataAPI) ResolveAdmissionStackGuards(labels []string) ([]string, error) {
	return d.admissionStore().ResolveAdmissionStackGuards(labels)
}

var _ datastore.AdmissionScopeResolver = (*DatastoreAuroraDataAPI)(nil)

func (d *DatastoreAuroraDataAPI) ResolveAdmissionTargetInventoryGuards(targets []string) ([]string, error) {
	return d.admissionStore().ResolveAdmissionTargetInventoryGuards(targets)
}

func (d *DatastoreAuroraDataAPI) ResolveAdmissionResourceIdentityGuards(ksuids []string) ([]string, error) {
	return d.admissionStore().ResolveAdmissionResourceIdentityGuards(ksuids)
}

var _ datastore.AdmissionPredicateResolver = (*DatastoreAuroraDataAPI)(nil)

func (d *DatastoreAuroraDataAPI) GetResourceObservation(ksuid string) (*datastore.ResourceObservation, error) {
	return d.admissionStore().GetResourceObservation(ksuid)
}

func (d *DatastoreAuroraDataAPI) ReadPolicyIdentity(label, stackID string) (*datastore.PolicyIdentity, error) {
	return d.admissionStore().ReadPolicyIdentity(label, stackID)
}

func (d *DatastoreAuroraDataAPI) PinCommandTargetIncarnation(commandID, target, incarnation string, refs []datastore.ResourceUpdateRef) error {
	return d.admissionStore().PinCommandTargetIncarnation(commandID, target, incarnation, refs)
}

func (d *DatastoreAuroraDataAPI) TryRetireEmptyStack(expectedStackID, label, cleanupCommandID string) (bool, error) {
	return d.admissionStore().TryRetireEmptyStack(expectedStackID, label, cleanupCommandID)
}
func (d *DatastoreAuroraDataAPI) TryRetireExpiredEmptyStack(candidate datastore.ExpiredStackInfo, expected []datastore.RevisionGuard, cleanupCommandID string) (bool, error) {
	return d.admissionStore().TryRetireExpiredEmptyStack(candidate, expected, cleanupCommandID)
}

var _ datastore.EmptyStackRetirer = &DatastoreAuroraDataAPI{}
var _ datastore.ExpiredEmptyStackRetirer = &DatastoreAuroraDataAPI{}

func (d *DatastoreAuroraDataAPI) HasOnlyExternalChanges(ksuid, baselineCommandID, observedVersion string) (bool, error) {
	return d.admissionStore().HasOnlyExternalChanges(ksuid, baselineCommandID, observedVersion)
}
