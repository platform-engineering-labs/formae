// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// These coarse guards protect absent mappings and domain-wide predicates. Their
// identity is fixed; callers must not derive domain keys from raw labels.
const (
	AdmissionStackMappingGuard = "admission:stack-mapping"
	AdmissionTargetGuard       = "admission:targets"
	AdmissionPolicyGuard       = "admission:policies"
	AdmissionGeneratorGuard    = "admission:generators"
	AdmissionTopologyGuard     = "admission:topology"
)

// AdmissionStackGuardKey protects a stable stack incarnation. Unusual legacy
// IDs share a conservative bucket, avoiding a new restriction on stored IDs.
// The ASCII grammar is identical in all three migration dialects.
func AdmissionStackGuardKey(id string) string {
	id = strings.TrimRight(id, " ")
	if len(id) == 0 || len(id) > 128 {
		return "admission:stack:exceptional"
	}
	for _, c := range []byte(id) {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return "admission:stack:exceptional"
		}
	}
	return "admission:stack:" + id
}

// AdmissionScopeResolver registers durable label identities using each backend's
// authoritative label collation. It does not read planning inputs or revisions.
// Resolve labels before the first revision sample. Include mapping, label and
// incarnation guards, plus every domain/predicate used in the plan. Grow closure
// and repeat the entire read interval when discovering additional scopes.
type AdmissionScopeResolver interface {
	ResolveAdmissionStackGuards(labels []string) ([]string, error)
}

// AdmissionPredicateResolver protects successful negative and positive resource
// lookups and target inventory enumeration. Resolve before sampling revisions;
// discovering a new predicate requires restarting the whole certified read.
// Keys are durable opaque identities, never URI or raw-label hashes.
type AdmissionPredicateResolver interface {
	ResolveAdmissionTargetInventoryGuards(targets []string) ([]string, error)
	ResolveAdmissionResourceIdentityGuards(ksuids []string) ([]string, error)
}

func (s AdmissionStore) ResolveAdmissionTargetInventoryGuards(targets []string) ([]string, error) {
	return s.resolveAdmissionIdentities("admission_inventory_targets", targets)
}

func (s AdmissionStore) ResolveAdmissionResourceIdentityGuards(ksuids []string) ([]string, error) {
	return s.resolveAdmissionIdentities("admission_resource_ids", ksuids)
}

func (s AdmissionStore) ResolveAdmissionStackGuards(labels []string) ([]string, error) {
	return s.resolveAdmissionIdentities("admission_stack_labels", labels)
}

// table is an internal constant, never caller-controlled SQL. SQL Server's MAX
// target identity table preserves long JSON targets and authoritative collation;
// its rare first registration requires an exclusive table insert-recheck lock.
func (s AdmissionStore) resolveAdmissionIdentities(table string, labels []string) ([]string, error) {
	labels = append([]string(nil), labels...)
	sort.Strings(labels)
	labels = slices.Compact(labels)
	if len(labels) > MaxAdmissionGuards {
		return nil, fmt.Errorf("%w: too many unique predicate identities", ErrInvalidAdmission)
	}
	tx, err := s.Begin()
	if err != nil {
		return nil, err
	}
	defer func(tx AdmissionTransaction) { _ = tx.Rollback() }(tx)
	keys := map[string]bool{}
	registrationLocked := false
	for i := 0; i < len(labels); i++ {
		label := labels[i]
		// Existing identities need no registration lock. Failed first lookups
		// are rechecked under the backend's insert serialization below.
		existing, e := tx.Query("SELECT guard_key FROM "+table+" WHERE label=?", label)
		if e != nil {
			return nil, e
		}
		if len(existing) == 1 {
			keys[existing[0]] = true
			continue
		}
		if s.Dialect == "sqlite" && !registrationLocked {
			// A WAL read snapshot cannot upgrade to a writer after another
			// connection commits. Abandon the read-only fast path and acquire
			// writer intent in a fresh transaction before rechecking the batch.
			if err = tx.Rollback(); err != nil {
				return nil, err
			}
			tx, err = s.Begin()
			if err != nil {
				return nil, err
			}
			defer func(tx AdmissionTransaction) { _ = tx.Rollback() }(tx)
			// Even a zero-row UPDATE acquires SQLite's writer lock. It changes
			// no identities and runs before any read in this transaction.
			if err = tx.Exec("UPDATE " + table + " SET label=label WHERE 0"); err != nil {
				return nil, err
			}
			registrationLocked = true
			keys = map[string]bool{}
			i = -1
			continue
		}
		// No application byte/Unicode bound: use exactly the authoritative SQL type.
		if s.Dialect == "postgres" && table == "admission_inventory_targets" {
			err = tx.Exec("LOCK TABLE admission_inventory_targets IN SHARE ROW EXCLUSIVE MODE")
			if err == nil {
				err = tx.Exec("INSERT INTO admission_inventory_targets(label) SELECT ? WHERE NOT EXISTS (SELECT 1 FROM admission_inventory_targets WHERE label=?)", label, label)
			}
		} else if s.Dialect == "mssql" {
			hint := "UPDLOCK,HOLDLOCK"
			if table == "admission_inventory_targets" {
				hint = "TABLOCKX,HOLDLOCK"
			}
			err = tx.Exec("IF NOT EXISTS (SELECT 1 FROM "+table+" WHERE label=?) BEGIN IF NOT EXISTS (SELECT 1 FROM "+table+" WITH ("+hint+") WHERE label=?) INSERT INTO "+table+"(label) VALUES (?) END", label, label, label)
		} else {
			err = tx.Exec("INSERT INTO "+table+"(label) VALUES (?) ON CONFLICT(label) DO NOTHING", label)
		}
		if err != nil {
			return nil, err
		}
		row, e := tx.Query("SELECT guard_key FROM "+table+" WHERE label=?", label)
		if e != nil {
			return nil, e
		}
		if len(row) != 1 {
			return nil, fmt.Errorf("missing admission predicate identity")
		}
		keys[row[0]] = true
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(keys))
	for k := range keys {
		result = append(result, k)
	}
	sort.Strings(result)
	return result, nil
}
