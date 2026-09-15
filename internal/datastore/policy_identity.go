// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package datastore

import "fmt"

// PolicyIdentity is the current declaration identity, separate from model config.
// An empty StackID denotes a standalone policy.
type PolicyIdentity struct{ ID, Version, Label, StackID string }

type PolicyIdentityReader interface {
	// ReadPolicyIdentity must be called inside the caller's revision-certified
	// planning interval. It does not establish that interval by itself.
	ReadPolicyIdentity(label, stackID string) (*PolicyIdentity, error)
}

func (s AdmissionStore) ReadPolicyIdentity(label, stackID string) (*PolicyIdentity, error) {
	tx, err := s.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := tx.Query(s.first(`SELECT id,version,CAST(COUNT(*) OVER() AS VARCHAR(20)) FROM (SELECT id,version,label,operation,ROW_NUMBER() OVER(PARTITION BY id ORDER BY version DESC) rn FROM policies WHERE COALESCE(stack_id,'')=?) p WHERE rn=1 AND operation!='delete' AND label=?`), stackID, label)
	if err != nil {
		return nil, err
	}
	var identity *PolicyIdentity
	if row != nil {
		if len(row) != 3 || row[2] != "1" {
			return nil, fmt.Errorf("%w: ambiguous policy identity", ErrAdmissionConflict)
		}
		identity = &PolicyIdentity{ID: row[0], Version: row[1], Label: label, StackID: stackID}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return identity, nil
}
