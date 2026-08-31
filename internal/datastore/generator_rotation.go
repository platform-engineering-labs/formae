// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"time"
)

// GeneratorRotationRow is one row a backend's GetGeneratorsWithRotation query
// produces, before the cadence is read out of the stored spec.
//
// Each backend's SQL answers only what SQL is good at — which generator rows
// are live, which stack owns them, and when the latest committed draw
// happened — and hands the stored spec over untouched. Whether that spec
// declares a cadence, and what the cadence is, is decided once in
// RotationInfoFromRows rather than in four dialects of JSON path extraction
// that would eventually disagree with each other and with the Go model.
type GeneratorRotationRow struct {
	GeneratorID    string
	Label          string
	StackLabel     string
	GeneratorData  []byte
	LastRotationAt time.Time
}

// RotationInfoFromRows keeps the rows whose stored spec declares a rotation
// cadence and projects them to GeneratorRotationInfo.
//
// A non-positive interval is dropped rather than scheduled: PKL requires
// `every`, so no authored generator produces one, and treating it as a cadence
// would mean "rotate on every sweep". A spec that cannot be parsed at all is
// dropped too — the cadence is unknowable, and guessing at one would rotate a
// live credential on a guess.
func RotationInfoFromRows(rows []GeneratorRotationRow) ([]GeneratorRotationInfo, error) {
	var infos []GeneratorRotationInfo
	for _, row := range rows {
		gen, err := GeneratorFromData(row.GeneratorData)
		if err != nil {
			return nil, err
		}
		rotation := gen.GetRotation()
		if rotation == nil || rotation.EverySeconds <= 0 {
			continue
		}
		infos = append(infos, GeneratorRotationInfo{
			GeneratorID:     row.GeneratorID,
			Label:           row.Label,
			StackLabel:      row.StackLabel,
			IntervalSeconds: rotation.EverySeconds,
			LastRotationAt:  row.LastRotationAt,
		})
	}
	return infos, nil
}
