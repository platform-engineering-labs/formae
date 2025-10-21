// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	StrategySetOnce = "SetOnce" // Value can only be set once, subsequent updates are ignored or rejected
	StrategyUpdate  = "Update"  // Value can be updated
)

const (
	VisibilityOpaque = "Opaque" // Value is secret/sensitive and should be hidden
	VisibilityClear  = "Clear"  // Value is public and can be displayed
)

type Value struct {
	Strategy   string `json:"$strategy,omitempty"`
	Visibility string `json:"$visibility,omitempty"` // Visibility of the value
	Value      any    `json:"$value,omitempty"`      // The actual value or hashed value, if applicable
}

func (v *Value) IsOpaque() bool {
	return v.Visibility == VisibilityOpaque
}

func (v *Value) IsClear() bool {
	return v.Visibility == VisibilityClear
}

func (v *Value) IsSetOnce() bool {
	return v.Strategy == StrategySetOnce
}

func (v *Value) IsUpdatable() bool {
	return v.Strategy == StrategyUpdate
}

// HasValue returns true if this Value has an actual value (not just a reference)
func (v *Value) HasValue() bool {
	return v.Value != nil
}

// CanUpdate checks if this value can be updated based on its strategy
func (v *Value) CanUpdate(existingValue *Value) bool {
	if v.IsSetOnce() && existingValue != nil && existingValue.Value != nil {
		return false // SetOnce values cannot be updated if they already exist
	}
	return true
}

func (v *Value) ToActual() *Value {
	if v.Value == nil {
		// If no value, return nil which will be handled by ApplyToResource
		return nil
	}

	// Return a simple Value with just the actual value
	return &Value{
		Value: v.Value,
	}
}

func (v *Value) GetStringValue() string {
	if v.Value == nil {
		return ""
	}

	switch val := v.Value.(type) {
	case string:
		return val
	case json.Number:
		return val.String()
	default:
		return fmt.Sprintf("%v", val)
	}
}

// computeHash computes SHA-256 hash of the value
func (v *Value) computeHash() string {
	if v.Value == nil {
		return ""
	}

	valueStr := v.GetStringValue()
	if valueStr == "" {
		return ""
	}

	// Use the same hashing logic as SecretsTransformer
	return ComputeValueHash(valueStr)
}

// ComputeValueHash is a helper function to compute SHA-256 hash
func ComputeValueHash(value string) string {
	// This could be moved to a shared utility if needed
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func (v *Value) Hash() *Value {
	if v.Value == nil {
		return v // Return as-is if no value to hash
	}

	// Create a new Value with the same metadata but hashed value
	hashedValue := &Value{
		Strategy:   v.Strategy,
		Visibility: v.Visibility,
		Value:      v.computeHash(),
	}

	return hashedValue
}
