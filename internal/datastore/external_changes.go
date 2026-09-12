// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import "strings"

// ExternalChangeReader classifies the complete stored interval, not merely the
// latest observation. Missing historical evidence always requires a decision.
type ExternalChangeReader interface {
	HasOnlyExternalChanges(ksuid, baselineCommandID, observedVersion string) (bool, error)
}

func (s AdmissionStore) HasOnlyExternalChanges(ksuid, baselineCommandID, observedVersion string) (bool, error) {
	if ksuid == "" || baselineCommandID == "" || observedVersion == "" {
		return false, nil
	}
	tx, err := s.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	coll := ""
	if s.Dialect == "postgres" {
		coll = ` COLLATE "C"`
	}
	if s.Dialect == "mssql" {
		coll = " COLLATE Latin1_General_BIN2"
	}
	// The desired contribution carries the physical version it was planned
	// against, including acceptance commands which perform no provider write.
	// Failed intent is still desired intent, not proof that the provider
	// realized it. Never automatically absorb it away after a later sync.
	boundary, err := tx.Query("SELECT COALESCE(MAX(version"+coll+"),'') FROM resource_updates WHERE command_id=? AND ksuid=? AND state='Success' AND EXISTS (SELECT 1 FROM forma_commands WHERE command_id=? AND timestamp IS NOT NULL)", baselineCommandID, ksuid, baselineCommandID)
	if err != nil {
		return false, err
	}
	if len(boundary) != 1 || boundary[0] == "" {
		return false, nil
	}
	// Provider updates historically store StoreResource's ksuid_version receipt;
	// acceptance updates store the physical version directly.
	boundary[0] = strings.TrimPrefix(boundary[0], ksuid+"_")
	exists, err := tx.Query("SELECT CAST(COUNT(*) AS VARCHAR(20)) FROM resources WHERE ksuid=? AND version=?", ksuid, boundary[0])
	if err != nil {
		return false, err
	}
	if len(exists) != 1 || exists[0] != "1" {
		return false, nil
	}
	// Failed patches can leave intent without a physical resource version (the
	// provider may have changed before reporting failure). Such interventions
	// still require a decision. This also catches successful patches whose
	// physical version was relabeled by a later read-only sync in place.
	// Acceptance starts a new command boundary even
	// when it keeps the same physical version. Include timestamp ties rather
	// than guessing which intervention came first; missing evidence is manual.
	intent, err := tx.Query(`SELECT CAST(COUNT(*) AS VARCHAR(20)) FROM resource_updates ru LEFT JOIN forma_commands fc ON fc.command_id=ru.command_id WHERE ru.ksuid=? AND ru.command_id<>? AND (fc.command_id IS NULL OR fc.timestamp IS NULL OR (fc.timestamp >= (SELECT timestamp FROM forma_commands WHERE command_id=?) AND (COALESCE(fc.command,'')<>'sync' OR COALESCE(fc.source,'')<>'synchronizer')))`, ksuid, baselineCommandID, baselineCommandID)
	if err != nil {
		return false, err
	}
	if len(intent) != 1 || intent[0] != "0" {
		return false, nil
	}
	// Sync commands use patch mode internally. Command type and trusted source
	// distinguish them from firefighting apply/patch commands.
	rows, err := tx.Query(`SELECT CAST(COUNT(*) AS VARCHAR(20)),CAST(COALESCE(SUM(CASE WHEN fc.command_id IS NOT NULL AND fc.command='sync' AND fc.source='synchronizer' AND r.operation IN ('create','update','delete') THEN 0 ELSE 1 END),0) AS VARCHAR(20)) FROM resources r LEFT JOIN forma_commands fc ON fc.command_id=r.command_id WHERE r.ksuid=? AND r.version`+coll+`>? AND r.version`+coll+`<=?`, ksuid, boundary[0], observedVersion)
	if err != nil {
		return false, err
	}
	return len(rows) == 2 && rows[0] != "0" && rows[1] == "0", nil
}
