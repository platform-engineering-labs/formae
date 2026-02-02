// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import "time"

type Stack struct {
	ID          string    `json:"ID,omitempty"`
	Label       string    `json:"Label"`
	Description string    `json:"Description"`
	TTLSeconds  int64     `json:"TTLSeconds,omitempty"` // 0 means no TTL, > 0 means TTL in seconds
	ExpiresAt   time.Time `json:"ExpiresAt,omitempty"`  // Zero value means no expiration
	CreatedAt   time.Time `json:"CreatedAt,omitempty"`
	UpdatedAt   time.Time `json:"UpdatedAt,omitempty"`
}
