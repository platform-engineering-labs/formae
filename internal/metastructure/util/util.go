// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package util

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"

	"github.com/segmentio/ksuid"
)

// NewID generates a unique ID via ksuid
func NewID() string {
	return ksuid.New().String()
}

func RandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		result[i] = charset[rand.IntN(len(charset))]
	}

	return string(result)
}

func InlineJSON(data any) string {
	//might be useful to have the other function in the future of debugging
	//b, err := json.MarshalIndent(data, "", "")
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Sprintf("error marshalling: %v", err)
	}
	return string(b)
}

func As[T any](data any) (T, error) {
	var result T
	var errs []error

	if data == nil {
		return result, fmt.Errorf("cannot convert nil value")
	}

	// Try direct type assertion
	if typedValue, ok := data.(T); ok {
		return typedValue, nil
	} else {
		errs = append(errs, fmt.Errorf("failed to assert type directly"))
	}

	// If data holds a pointer, try to convert the pointed value
	valueOf := reflect.ValueOf(data)
	if valueOf.Kind() == reflect.Pointer && !valueOf.IsNil() {
		if converted, ok := valueOf.Elem().Interface().(T); ok {
			return converted, nil
		} else {
			errs = append(errs, fmt.Errorf("failed to assert type of pointed value"))
		}
	}

	// Try JSON marshaling and unmarshaling as a fallback
	jsonData, err := json.Marshal(data)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to marshal source data: %w", err))
	} else {
		if err = json.Unmarshal(jsonData, &result); err == nil {
			return result, nil
		}

		errs = append(errs, fmt.Errorf("failed to convert to target type: %w", err))
	}

	return result, errors.Join(errs...)
}

func StringPtr(s string) *string {
	return &s
}

func StringPtrToString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
