// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"encoding/json"
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ResourceObservation is one uniquely identified current physical resource row,
// including delete and reaped tombstones. Operation, Version and Resource belong
// to this exact row; historical ResourceModification.Operation is not substituted.
// StackID is empty when durable history cannot prove the row's incarnation.
type ResourceObservation struct {
	URI       string
	Version   string
	Operation string
	CommandID string // command that wrote this exact physical observation
	// ConfirmedDeletion distinguishes managed deletion from DB-only forgetting of
	// unmanaged/reaped inventory. It requires an evidenced preceding managed live
	// row under the same physical identity, with no intervening reap. Uncertain
	// legacy history is unconfirmed; the raw stored operation remains available.
	ConfirmedDeletion   bool
	KSUID               string
	Stack               string // physical stack; Resource.Stack retains embedded provenance
	Target              string // physical target; Resource.Target retains embedded provenance
	StackID             string
	TargetIncarnationID string // exact physical resource-row target incarnation, never inferred from label
	Resource            *pkgmodel.Resource
	// PreviousLiveResource supplies the declaration/schema from the last preceding
	// non-delete row only when that row is create/update. Its Version is the real
	// prior row version, not the deletion's version. DeleteResource stores {} in
	// the tombstone payload; callers must not relabel prior data as current data.
	PreviousLiveResource *pkgmodel.Resource
}

// ResourceObservationReader reads the true latest row for each logical URI,
// including tombstones (unlike the legacy live-only LoadResourceById). A missing
// identity returns nil; multiple current URI candidates for one KSUID fail closed.
// Call under resource-identity, stack-label/incarnation and mapping guards. Closure
// expansion requires a complete read restart. Only ConfirmedDeletion permits a
// deletion acceptance; nil, raw delete, reaped and read errors alone never do.
type ResourceObservationReader interface {
	GetResourceObservation(ksuid string) (*ResourceObservation, error)
}

func (s AdmissionStore) GetResourceObservation(ksuid string) (*ResourceObservation, error) {
	tx, err := s.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	coll, textType, top, limit := "", "TEXT", "", " LIMIT 1"
	managedTrue := "1"
	if s.Dialect == "postgres" {
		coll = ` COLLATE "C"`
		managedTrue = "TRUE"
	}
	if s.Dialect == "mssql" {
		coll = " COLLATE Latin1_General_BIN2"
		textType = "NVARCHAR(MAX)"
		top = "TOP (1) "
		limit = ""
	}
	// Apply KSUID selection after true-latest URI selection. The window count
	// rejects arbitrary map-last-wins behavior for independent duplicate URIs.
	query := "SELECT " + top + "r.uri,r.version,r.operation,r.ksuid,COALESCE(r.stack,''),COALESCE(r.target,''),CAST(r.data AS " + textType + "),COALESCE(r.command_id,''),CAST(COUNT(*) OVER () AS VARCHAR(20)),CASE WHEN r.managed=" + managedTrue + " THEN '1' ELSE '0' END,COALESCE(r.target_incarnation_id,'') FROM resources r WHERE r.ksuid=? AND NOT EXISTS (SELECT 1 FROM resources newer WHERE newer.uri=r.uri AND newer.version" + coll + ">r.version" + coll + ")" + limit
	row, err := tx.Query(query, ksuid)
	if err != nil {
		return nil, err
	}
	if len(row) == 0 {
		return nil, nil
	}
	if len(row) != 11 || row[8] != "1" {
		return nil, fmt.Errorf("ambiguous current resource identity %q", ksuid)
	}
	var resource pkgmodel.Resource
	if err = json.Unmarshal([]byte(row[6]), &resource); err != nil {
		return nil, err
	}
	resource.Ksuid = row[3]
	resource.Version = row[1]
	observation := &ResourceObservation{URI: row[0], Version: row[1], Operation: row[2], CommandID: row[7], KSUID: row[3], Stack: row[4], Target: row[5], TargetIncarnationID: row[10], Resource: &resource}
	if observation.Operation == "delete" {
		previous, err := tx.Query("SELECT "+top+"operation,ksuid,version,CASE WHEN managed="+managedTrue+" THEN '1' ELSE '0' END,CAST(data AS "+textType+") FROM resources WHERE uri=? AND version"+coll+"<? AND operation!='delete' ORDER BY version"+coll+" DESC"+limit, row[0], row[1])
		if err != nil {
			return nil, err
		}
		if len(previous) > 0 {
			if len(previous) != 5 {
				return nil, fmt.Errorf("invalid prior observation")
			}
			var prior pkgmodel.Resource
			if err = json.Unmarshal([]byte(previous[4]), &prior); err != nil {
				return nil, err
			}
			if previous[0] == "create" || previous[0] == "update" {
				prior.Ksuid = previous[1]
				prior.Version = previous[2]
				observation.PreviousLiveResource = &prior
				observation.ConfirmedDeletion = row[9] == "1" && previous[1] == row[3] && previous[3] == "1" && prior.Managed
			}
		}
	}
	// Explicit membership is strongest evidence and can name an old incarnation
	// after label reuse. Consumers must compare it with their guarded plan stack.
	membership, err := tx.Query("SELECT COALESCE(MIN(stack_id),''),CAST(COUNT(DISTINCT stack_id) AS VARCHAR(20)) FROM command_stacks WHERE command_id=? AND stack_label=?", row[7], row[4])
	if err != nil {
		return nil, err
	}
	if len(membership) != 2 {
		return nil, fmt.Errorf("invalid observation membership")
	}
	switch membership[1] {
	case "1":
		observation.StackID = membership[0]
	case "0":
		// A single actual historical incarnation predating this row is evidence for
		// legacy/sync observations. Never infer a historical ID across label reuse.
		history, err := tx.Query("SELECT COALESCE(MIN(id),''),CAST(COUNT(DISTINCT id) AS VARCHAR(20)),CAST(CASE WHEN MIN(version"+coll+")<=? THEN 1 ELSE 0 END AS VARCHAR(1)) FROM stacks WHERE label=?", row[1], row[4])
		if err != nil {
			return nil, err
		}
		if len(history) != 3 {
			return nil, fmt.Errorf("invalid observation stack history")
		}
		if history[1] == "1" && history[2] == "1" {
			observation.StackID = history[0]
		}
	default:
		return nil, fmt.Errorf("ambiguous observation stack incarnation for %q", ksuid)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return observation, nil
}
