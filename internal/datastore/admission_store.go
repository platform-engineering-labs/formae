// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
)

// AdmissionTransaction must use one database transaction for every operation.
// Query returns nil for no row. SQL uses ? placeholders; adapters bind them.
type AdmissionTransaction interface {
	Exec(string, ...any) error
	Query(string, ...any) ([]string, error)
	Store(*forma_command.FormaCommand, string) error
	Commit() error
	Rollback() error
}

// AdmissionStore shares the internal persistence protocol across SQL dialects.
// Writer triggers protect revisions; runtime composition must still establish
// the planning read interval, authenticated scope and actual schema-input binding.
type AdmissionStore struct {
	Dialect string
	Begin   func() (AdmissionTransaction, error)
}

func AdmissionBind(query, prefix string) string {
	if prefix == "?" {
		return query
	}
	parts := strings.Split(query, "?")
	var out strings.Builder
	out.WriteString(parts[0])
	for i, p := range parts[1:] {
		out.WriteString(prefix + strconv.Itoa(i+1))
		out.WriteString(p)
	}
	return out.String()
}

func (s AdmissionStore) lockedSelect(query string) string {
	if s.Dialect == "mssql" {
		return strings.Replace(query, " WHERE ", " WITH (UPDLOCK, HOLDLOCK) WHERE ", 1)
	}
	if s.Dialect == "postgres" {
		return query + " FOR UPDATE"
	}
	return query
}

func (s AdmissionStore) seedRevision(tx AdmissionTransaction, key string) error {
	if s.Dialect == "mssql" {
		// The indexed serializable range lock protects absent rows as well as rows.
		return tx.Exec("UPDATE admission_revisions WITH (UPDLOCK, HOLDLOCK) SET revision=revision WHERE guard_key=?; IF @@ROWCOUNT=0 INSERT INTO admission_revisions(guard_key,revision) VALUES (?,0)", key, key)
	}
	return tx.Exec("INSERT INTO admission_revisions(guard_key,revision) VALUES (?,0) ON CONFLICT(guard_key) DO NOTHING", key)
}
func (s AdmissionStore) revision(tx AdmissionTransaction, key string) (int64, error) {
	if err := s.seedRevision(tx, key); err != nil {
		return 0, err
	}
	row, err := tx.Query(s.lockedSelect("SELECT CAST(revision AS VARCHAR(20)) FROM admission_revisions WHERE guard_key=?"), key)
	if err != nil {
		return 0, err
	}
	if len(row) != 1 {
		return 0, fmt.Errorf("missing locked admission revision %q", key)
	}
	return strconv.ParseInt(row[0], 10, 64)
}

// ReadAdmissionRevisions obtains sorted write/range locks so the returned vector
// is one consistent snapshot, including absent keys at zero. Zero-row seeding is
// durable but never resets an existing revision. This is not a planning lease.
func (s AdmissionStore) ReadAdmissionRevisions(keys []string) ([]RevisionGuard, error) {
	guards := make([]RevisionGuard, len(keys))
	for i, key := range keys {
		guards[i].Key = key
	}
	guards, err := CanonicalAdmissionGuards(guards)
	if err != nil {
		return nil, err
	}
	if len(guards) > MaxAdmissionGuards {
		return nil, fmt.Errorf("%w: too many unique guard keys", ErrInvalidAdmission)
	}
	if len(guards) == 0 {
		return guards, nil
	}
	tx, err := s.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	for i, g := range guards {
		guards[i].Revision, err = s.revision(tx, g.Key)
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return guards, nil
}

func validateAdmissionLookup(scope, key string) error {
	if !validateAdmissionIdentity(scope, 200) || !validateAdmissionIdentity(key, 200) {
		return fmt.Errorf("%w: invalid principal scope or idempotency key", ErrInvalidAdmission)
	}
	return nil
}
func (s AdmissionStore) lookup(tx AdmissionTransaction, scope, key string, lock bool) (*StoredAdmission, error) {
	query := "SELECT command_id, request_digest, receipt FROM command_admissions WHERE principal_scope=? AND idempotency_key=?"
	if lock {
		query = s.lockedSelect(query)
	}
	row, err := tx.Query(query, scope, key)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	if len(row) != 3 {
		return nil, fmt.Errorf("invalid stored admission")
	}
	return &StoredAdmission{CommandID: row[0], RequestDigest: row[1], Receipt: []byte(row[2])}, nil
}
func (s AdmissionStore) LookupCommandAdmission(scope, key string) (*StoredAdmission, error) {
	if err := validateAdmissionLookup(scope, key); err != nil {
		return nil, err
	}
	tx, err := s.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := s.lookup(tx, scope, key, false)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return a, nil
}

func (s AdmissionStore) AdmitFormaCommand(command *forma_command.FormaCommand, a CommandAdmission) (AdmissionResult, error) {
	var result AdmissionResult
	a, err := NormalizeCommandAdmission(a)
	if err != nil {
		return result, err
	}
	if command == nil || !validateAdmissionIdentity(command.ID, 450) {
		return result, fmt.Errorf("%w: command ID required", ErrInvalidAdmission)
	}
	tx, err := s.Begin()
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	// Reserve under the unique key before inspecting guards. A raced insert waits
	// for the winner, then observes its receipt. Reservation is rolled back on any
	// failure and can never become visible without a complete command.
	if s.Dialect == "mssql" {
		err = tx.Exec("UPDATE command_admissions WITH (UPDLOCK, HOLDLOCK) SET command_id=command_id WHERE principal_scope=? AND idempotency_key=?; IF @@ROWCOUNT=0 INSERT INTO command_admissions(principal_scope,idempotency_key,command_id,request_digest,receipt) VALUES (?,?,'','','{}')", a.PrincipalScope, a.IdempotencyKey, a.PrincipalScope, a.IdempotencyKey)
	} else {
		err = tx.Exec("INSERT INTO command_admissions(principal_scope,idempotency_key,command_id,request_digest,receipt) VALUES (?,?,'','','{}') ON CONFLICT(principal_scope,idempotency_key) DO NOTHING", a.PrincipalScope, a.IdempotencyKey)
	}
	if err != nil {
		return result, err
	}
	stored, err := s.lookup(tx, a.PrincipalScope, a.IdempotencyKey, true)
	if err != nil {
		return result, err
	}
	if stored == nil {
		return result, fmt.Errorf("missing admission reservation")
	}
	if stored.CommandID != "" {
		if stored.RequestDigest != a.RequestDigest {
			return result, ErrAdmissionConflict
		}
		result = AdmissionResult{StoredAdmission: *stored, Replayed: true}
		if err = tx.Commit(); err != nil {
			return AdmissionResult{}, err
		}
		return result, nil
	}
	for _, g := range a.Guards {
		actual, err := s.revision(tx, g.Key)
		if err != nil {
			return result, err
		}
		if actual != g.Revision {
			return result, fmt.Errorf("%w: %q expected %d, got %d", ErrStaleAdmission, g.Key, g.Revision, actual)
		}
	}
	// Admission is creation only. Lifecycle StoreFormaCommand may upsert, but a
	// different idempotency key must never overwrite an existing command ID.
	row, err := tx.Query(s.lockedSelect("SELECT command_id FROM forma_commands WHERE command_id=?"), command.ID)
	if err != nil {
		return result, err
	}
	if row != nil {
		return result, fmt.Errorf("%w: command ID already exists", ErrAdmissionConflict)
	}
	// Work on a private snapshot: failed/uncertain admissions must not mutate caller intent.
	data, err := json.Marshal(command)
	if err != nil {
		return result, err
	}
	var committed forma_command.FormaCommand
	if err = json.Unmarshal(data, &committed); err != nil {
		return result, err
	}
	// Draw values are never stored; the separate setup carrier owns exact draw
	// intent omitted by FormaCommand's ordinary JSON representation.
	setup, err := command.MarshalSetupMetadata(false)
	if err != nil {
		return result, err
	}
	if err = committed.UnmarshalSetupMetadata(setup); err != nil {
		return result, err
	}
	if err = s.setupCommand(tx, &committed, a.Guards); err != nil {
		return result, err
	}
	if err = tx.Store(&committed, command.ID); err != nil {
		return result, err
	}
	err = tx.Exec("UPDATE command_admissions SET command_id=?,request_digest=?,receipt=? WHERE principal_scope=? AND idempotency_key=?", command.ID, a.RequestDigest, string(a.Receipt), a.PrincipalScope, a.IdempotencyKey)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return AdmissionResult{StoredAdmission: StoredAdmission{CommandID: command.ID, RequestDigest: a.RequestDigest, Receipt: a.Receipt}, Command: &committed}, nil
}

// SQLAdmissionTransaction adapts database/sql backends without opening any bare
// connection while the transaction is held (especially SQLite's sole writer).
type SQLAdmissionTransaction struct {
	Tx           *sql.Tx
	Prefix       string
	StoreCommand func(*forma_command.FormaCommand, string) error
}

func (t SQLAdmissionTransaction) Exec(q string, args ...any) error {
	_, err := t.Tx.Exec(AdmissionBind(q, t.Prefix), args...)
	return err
}
func (t SQLAdmissionTransaction) Query(q string, args ...any) ([]string, error) {
	rows, err := t.Tx.Query(AdmissionBind(q, t.Prefix), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, rows.Err()
	}
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(cols))
	dest := make([]any, len(cols))
	for i := range out {
		dest[i] = &out[i]
	}
	if err = rows.Scan(dest...); err != nil {
		return nil, err
	}
	return out, rows.Err()
}
func (t SQLAdmissionTransaction) Store(c *forma_command.FormaCommand, id string) error {
	return t.StoreCommand(c, id)
}
func (t SQLAdmissionTransaction) Commit() error   { return t.Tx.Commit() }
func (t SQLAdmissionTransaction) Rollback() error { return t.Tx.Rollback() }
