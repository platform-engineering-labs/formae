// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package model

// DriftResolution accompanies a complete reconcile declaration. ObservationID
// identifies the drift shown to the caller; ReviewID identifies the final plan.
// A real submission requires the final review and a stable caller retry key.
type DriftResolution struct {
	ObservationID  string          `json:"ObservationID"`
	ReviewID       string          `json:"ReviewID,omitempty"`
	Decisions      []DriftDecision `json:"Decisions"`
	IdempotencyKey string          `json:"IdempotencyKey,omitempty"`
}
type DriftDecision struct {
	ResourceID string `json:"ResourceID"`
	Action     string `json:"Action"` // absorb or revert
}

// DriftObservation is immutable provenance, with no resource property values.
type DriftObservation struct {
	ResourceID        string `json:"ResourceID"`
	StackID           string `json:"StackID"`
	Stack             string `json:"Stack"`
	Type              string `json:"Type"`
	Label             string `json:"Label"`
	Kind              string `json:"Kind"` // update, create, or delete
	ObservedVersion   string `json:"ObservedVersion"`
	ObservedCommandID string `json:"ObservedCommandID,omitempty"`
	BaselineCommandID string `json:"BaselineCommandID,omitempty"`
}

// DriftReview is also the immutable receipt stored on the admitted command.
// Command resource contributions provide the corresponding desired delta;
// inventory extraction is not a source of accepted intent.
type DriftReview struct {
	ObservationID string             `json:"ObservationID"`
	ReviewID      string             `json:"ReviewID"`
	Decisions     []DriftDecision    `json:"Decisions"`
	Observations  []DriftObservation `json:"Observations"`
}
