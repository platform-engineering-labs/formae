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
	boundary, err := tx.Query("SELECT COALESCE(MAX(version"+coll+"),'') FROM resource_updates WHERE command_id=? AND ksuid=?", baselineCommandID, ksuid)
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
	rows, err := tx.Query(`SELECT CAST(COUNT(*) AS VARCHAR(20)),CAST(COALESCE(SUM(CASE WHEN fc.command_id IS NOT NULL AND fc.command='sync' AND fc.source='synchronizer' AND COALESCE(fc.config_mode,'')!='patch' AND r.operation IN ('create','update','delete') THEN 0 ELSE 1 END),0) AS VARCHAR(20)) FROM resources r LEFT JOIN forma_commands fc ON fc.command_id=r.command_id WHERE r.ksuid=? AND r.version`+coll+`>? AND r.version`+coll+`<=?`, ksuid, boundary[0], observedVersion)
	if err != nil {
		return false, err
	}
	return len(rows) == 2 && rows[0] != "0" && rows[1] == "0", nil
}
