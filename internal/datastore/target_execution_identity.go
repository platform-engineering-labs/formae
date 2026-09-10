// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"encoding/json"
	"fmt"
)

// CommandTargetIdentityWriter pins only execution provenance, without replacing
// command intent, progress, or resource versions.
type CommandTargetIdentityWriter interface {
	PinCommandTargetIncarnation(commandID, target, incarnation string, refs []ResourceUpdateRef) error
}

func (s AdmissionStore) PinCommandTargetIncarnation(commandID, target, incarnation string, refs []ResourceUpdateRef) error {
	if incarnation == "" {
		return fmt.Errorf("missing committed target incarnation")
	}
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = s.revision(tx, AdmissionTargetGuard); err != nil {
		return err
	}
	top, limit, textType := "", " LIMIT 1", "TEXT"
	if s.Dialect == "mssql" {
		top = "TOP (1) "
		limit = ""
		textType = "NVARCHAR(MAX)"
	}
	current, err := tx.Query("SELECT "+top+"target_incarnation_id,health_state FROM targets WHERE label=? ORDER BY version DESC"+limit, target)
	if err != nil {
		return err
	}
	if len(current) != 2 || current[0] != incarnation || current[1] == "reaped" {
		return fmt.Errorf("target incarnation changed before dependent execution")
	}
	for _, ref := range refs {
		row, err := tx.Query("SELECT CAST(resource_target AS "+textType+") FROM resource_updates WHERE command_id=? AND ksuid=? AND operation=?", commandID, ref.KSUID, string(ref.Operation))
		if err != nil {
			return err
		}
		if len(row) != 1 {
			return fmt.Errorf("missing resource update for target identity pin")
		}
		var payload map[string]json.RawMessage
		if err = json.Unmarshal([]byte(row[0]), &payload); err != nil {
			return err
		}
		var label string
		if err = json.Unmarshal(payload["Label"], &label); err != nil {
			return err
		}
		if label != target {
			return fmt.Errorf("resource update target changed before identity pin")
		}
		payload["ExecutionIncarnation"], _ = json.Marshal(incarnation)
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if err = tx.Exec("UPDATE resource_updates SET resource_target=? WHERE command_id=? AND ksuid=? AND operation=?", string(encoded), commandID, ref.KSUID, string(ref.Operation)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
