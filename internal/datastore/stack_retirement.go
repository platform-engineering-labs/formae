// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"fmt"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
)

// EmptyStackRetirer proves that an expected incarnation owns no actual, desired,
// generator or nonterminal command state and tombstones it in one transaction.
// A nonempty cleanupCommandID additionally requires durable eligible terminal
// command membership. Empty IDs are reserved for automatic TTL/reap cleanup.
type EmptyStackRetirer interface {
	TryRetireEmptyStack(expectedStackID, label, cleanupCommandID string) (bool, error)
}

// ExpiredEmptyStackRetirer binds the scheduler's certified expiry decision to
// the same transaction that proves emptiness and tombstones the stack.
type ExpiredEmptyStackRetirer interface {
	TryRetireExpiredEmptyStack(candidate ExpiredStackInfo, expected []RevisionGuard, cleanupCommandID string) (bool, error)
}

// TryRetireEmptyStack serializes with admission and ordinary writers on their
// existing durable guards. It never manufactures delete intent for resources
// which failed to create. Retention and all errors leave the stack untouched.
func (s AdmissionStore) TryRetireEmptyStack(expectedStackID, label, cleanupCommandID string) (bool, error) {
	if expectedStackID == "" || label == "" {
		return false, nil
	}
	tx, err := s.beginStackRetirement(expectedStackID, label, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	return s.tryRetireEmptyStackTx(tx, expectedStackID, label, cleanupCommandID)
}

func (s AdmissionStore) TryRetireExpiredEmptyStack(candidate ExpiredStackInfo, expected []RevisionGuard, cleanupCommandID string) (bool, error) {
	if candidate.StackID == "" || candidate.StackLabel == "" || candidate.HasUnreadableDeadline() {
		return false, nil
	}
	expected, err := CanonicalAdmissionGuards(expected)
	if err != nil {
		return false, err
	}
	if len(expected) == 0 {
		return false, fmt.Errorf("%w: expiry retirement requires certified guards", ErrInvalidAdmission)
	}
	tx, err := s.beginStackRetirement(candidate.StackID, candidate.StackLabel, expected)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	eligible, err := s.expiredCandidateStillMatches(tx, candidate)
	if err != nil || !eligible {
		return false, err
	}
	return s.tryRetireEmptyStackTx(tx, candidate.StackID, candidate.StackLabel, cleanupCommandID)
}

func (s AdmissionStore) beginStackRetirement(expectedStackID, label string, expected []RevisionGuard) (AdmissionTransaction, error) {
	keys, err := s.ResolveAdmissionStackGuards([]string{label})
	if err != nil {
		return nil, err
	}
	keys = append(keys, AdmissionStackMappingGuard, AdmissionStackGuardKey(expectedStackID), AdmissionGeneratorGuard, AdmissionPolicyGuard)
	byKey := make(map[string]int64, len(expected))
	for _, guard := range expected {
		keys = append(keys, guard.Key)
		byKey[guard.Key] = guard.Revision
	}
	guards := make([]RevisionGuard, len(keys))
	for i, key := range keys {
		guards[i].Key = key
	}
	guards, err = CanonicalAdmissionGuards(guards)
	if err != nil {
		return nil, err
	}
	tx, err := s.Begin()
	if err != nil {
		return nil, err
	}
	for _, guard := range guards {
		actual, revisionErr := s.revision(tx, guard.Key)
		if revisionErr != nil {
			_ = tx.Rollback()
			return nil, revisionErr
		}
		if wanted, ok := byKey[guard.Key]; ok && actual != wanted {
			_ = tx.Rollback()
			return nil, fmt.Errorf("%w: %q expected %d, got %d", ErrStaleAdmission, guard.Key, wanted, actual)
		}
	}
	return tx, nil
}

func (s AdmissionStore) expiredCandidateStillMatches(tx AdmissionTransaction, candidate ExpiredStackInfo) (bool, error) {
	jsonValue := func(path string) string {
		switch s.Dialect {
		case "postgres":
			return "p.policy_data::jsonb->>'" + path + "'"
		case "mssql":
			return "JSON_VALUE(p.policy_data,'$." + path + "')"
		default:
			return "json_extract(p.policy_data,'$." + path + "')"
		}
	}
	prefix := s.retirementCollation(`WITH latest_policies AS (
 SELECT p.*,ROW_NUMBER() OVER(PARTITION BY p.id ORDER BY p.version DESC) rn FROM policies p
)
`)
	outer := `SELECT CAST(1 AS VARCHAR(1)) FROM latest_policies p
WHERE p.rn=1 AND p.operation!='delete' AND p.policy_type='ttl'
AND (p.stack_id=? OR ((p.stack_id IS NULL OR p.stack_id='') AND EXISTS (SELECT 1 FROM stack_policies sp WHERE sp.policy_id=p.id AND sp.stack_id=?)))
		AND COALESCE(` + jsonValue("OnDependents") + `,'abort')=? AND `
	args := []any{candidate.StackID, candidate.StackID, candidate.OnDependents}
	if candidate.ExpiresAt != "" {
		outer += jsonValue("ExpiresAt") + `=?`
		args = append(args, candidate.ExpiresAt)
	} else if candidate.TTLSeconds != nil {
		outer += jsonValue("ExpiresAt") + ` IS NULL AND CAST(` + jsonValue("TTLSeconds") + ` AS VARCHAR(32))=?`
		args = append(args, fmt.Sprint(*candidate.TTLSeconds))
	} else {
		return false, nil
	}
	row, err := tx.Query(prefix+s.first(outer), args...)
	return row != nil, err
}

func (s AdmissionStore) tryRetireEmptyStackTx(tx AdmissionTransaction, expectedStackID, label, cleanupCommandID string) (bool, error) {
	row, err := tx.Query(s.first("SELECT id,version,operation,description FROM stacks WHERE label=? ORDER BY version DESC"), label)
	if err != nil {
		return false, err
	}
	if row == nil || row[0] != expectedStackID || row[2] == "delete" {
		return false, nil
	}
	previous, description := row[1], row[3]
	if cleanupCommandID != "" {
		row, err = tx.Query(s.first(`SELECT fc.command_id FROM forma_commands fc JOIN command_stacks cs ON cs.command_id=fc.command_id WHERE fc.command_id=? AND cs.stack_id=? AND cs.stack_label=? AND fc.state IN ('Success','Failed') AND (fc.command='destroy' OR (fc.command='apply' AND fc.config_mode='reconcile')) AND (fc.source IS NULL OR fc.source IN ('','user','auto-reconciler','stack-expirer'))`), cleanupCommandID, expectedStackID, label)
		if err != nil {
			return false, err
		}
		if row == nil {
			return false, nil
		}
	}
	// Unknown/missing command state is not evidence of terminal completion. Include
	// all durable memberships, even metadata-only commands before resource rows.
	row, err = tx.Query(s.first(`SELECT cs.command_id FROM command_stacks cs LEFT JOIN forma_commands fc ON fc.command_id=cs.command_id WHERE cs.stack_id=? AND (fc.state IS NULL OR fc.state NOT IN ('Success','Failed','Canceled'))`), expectedStackID)
	if err != nil {
		return false, err
	}
	if row != nil {
		return false, nil
	}
	row, err = tx.Query(s.first(s.retirementCollation(`SELECT r.uri FROM resources r WHERE r.stack=? AND r.operation NOT IN ('delete','reaped') AND NOT EXISTS (SELECT 1 FROM resources newer WHERE newer.uri=r.uri AND newer.version > r.version)`)), label)
	if err != nil {
		return false, err
	}
	if row != nil {
		return false, nil
	}
	// A present declaration or generator is ownership even when its payload is
	// corrupt: no decoding or best-effort skipping can turn it into emptiness.
	row, err = tx.Query(s.first(`SELECT id FROM (SELECT id,operation,ROW_NUMBER() OVER(PARTITION BY id ORDER BY version DESC) rn FROM generators WHERE stack_id=?) g WHERE rn=1 AND operation!='delete'`), expectedStackID)
	if err != nil {
		return false, err
	}
	if row != nil {
		return false, nil
	}
	row, err = tx.Query(s.retirementCollation(`WITH eligible AS (
 SELECT ru.ksuid,ru.operation,fc.timestamp FROM resource_updates ru JOIN forma_commands fc ON fc.command_id=ru.command_id
 WHERE ru.stack_label=? AND (fc.command='destroy' OR (fc.command='apply' AND fc.config_mode='reconcile'))
 AND fc.state IN ('Success','Failed') AND ru.source='user'
 AND (fc.source IS NULL OR fc.source IN ('','user','auto-reconciler','stack-expirer'))
 AND (EXISTS (SELECT 1 FROM command_stacks cs WHERE cs.command_id=fc.command_id AND cs.stack_label=ru.stack_label AND cs.stack_id=?)
 OR (NOT EXISTS (SELECT 1 FROM command_stacks cs WHERE cs.command_id=fc.command_id AND cs.stack_label=ru.stack_label)
 AND NOT EXISTS (SELECT 1 FROM stacks sa JOIN stacks sb ON sa.label=sb.label AND sa.id!=sb.id WHERE sa.label=ru.stack_label)))
 ), latest AS (SELECT ksuid,operation,ROW_NUMBER() OVER(PARTITION BY ksuid ORDER BY timestamp DESC,CASE WHEN operation='delete' THEN 1 ELSE 0 END) rn FROM eligible)
 `)+s.first("SELECT ksuid FROM latest WHERE rn=1 AND operation NOT IN ('delete','accept_delete','withdraw')"), label, expectedStackID)
	if err != nil {
		return false, err
	}
	if row != nil {
		return false, nil
	}
	provenance := cleanupCommandID
	if provenance == "" {
		provenance = "retire-" + util.NewID()
	}
	if err = s.deleteSetupPolicies(tx, expectedStackID, &forma_command.FormaCommand{ID: provenance}); err != nil {
		return false, err
	}
	version := ""
	if err = setupVersion(&version, previous); err != nil {
		return false, err
	}
	if err = tx.Exec("INSERT INTO stacks(id,version,command_id,operation,label,description) VALUES (?,?,?,'delete',?,?)", expectedStackID, version, provenance, label, description); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s AdmissionStore) retirementCollation(q string) string {
	collation := ""
	if s.Dialect == "postgres" {
		collation = ` COLLATE "C"`
	}
	if s.Dialect == "mssql" {
		collation = " COLLATE Latin1_General_BIN2"
	}
	q = strings.ReplaceAll(q, "ORDER BY version DESC", "ORDER BY version"+collation+" DESC")
	q = strings.ReplaceAll(q, "ORDER BY p.version DESC", "ORDER BY p.version"+collation+" DESC")
	return strings.ReplaceAll(q, "newer.version > r.version", "newer.version"+collation+" > r.version"+collation)
}

// RetireCommandStack binds a delayed cleanup message to the command's durable
// incarnation, never to today's reusable label. The transaction rechecks it.
func RetireCommandStack(ds Datastore, label, commandID string) (bool, error) {
	retire, ok := ds.(EmptyStackRetirer)
	if !ok {
		return false, fmt.Errorf("datastore does not support atomic stack retirement")
	}
	command, err := ds.GetFormaCommandByCommandID(commandID)
	if err != nil {
		return false, err
	}
	if command == nil {
		return false, nil
	}
	for _, stack := range command.Stacks {
		if stack.Label == label {
			return retire.TryRetireEmptyStack(stack.ID, label, commandID)
		}
	}
	return false, nil
}
