// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// UniqueStackName generates a unique stack name by appending a random hex
// suffix to the given prefix. This allows parallel test execution without
// stack name collisions.
func UniqueStackName(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%x", prefix, b)
}

// FormaeBinary returns the path to the formae binary. It checks the
// E2E_FORMAE_BINARY environment variable first and falls back to the
// built binary at the repository root.
func FormaeBinary(t *testing.T) string {
	t.Helper()

	if bin := os.Getenv("E2E_FORMAE_BINARY"); bin != "" {
		if _, err := os.Stat(bin); err != nil {
			t.Fatalf("E2E_FORMAE_BINARY=%q does not exist: %v", bin, err)
		}
		return bin
	}

	// Walk up from the test directory to find the repo root.
	// tests/e2e/go/ -> repo root is three levels up.
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}

	bin := filepath.Join(repoRoot, "formae")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("formae binary not found at %s (run 'make build' first or set E2E_FORMAE_BINARY): %v", bin, err)
	}

	return bin
}

// RequireCommandSuccess asserts that the command result reached the "Success"
// state. If not, it fails the test with the actual state.
func RequireCommandSuccess(t *testing.T, result CommandResult) {
	t.Helper()

	if result.State != "Success" {
		t.Fatalf("expected command state Success, got %s", result.State)
	}
}

// FindResource searches the resource slice for the first resource matching
// the given label. Returns nil if not found.
func FindResource(resources []Resource, label string) *Resource {
	for i := range resources {
		if resources[i].Label == label {
			return &resources[i]
		}
	}
	return nil
}

// FindResourceByType returns all resources matching the given resource type.
func FindResourceByType(resources []Resource, resourceType string) []Resource {
	var matched []Resource
	for i := range resources {
		if resources[i].Type == resourceType {
			matched = append(matched, resources[i])
		}
	}
	return matched
}

// RequireResource searches the resource slice for the first resource matching
// the given label and fails the test if not found.
func RequireResource(t *testing.T, resources []Resource, label string) Resource {
	t.Helper()
	r := FindResource(resources, label)
	if r == nil {
		t.Fatalf("resource with label %q not found in %d resources", label, len(resources))
	}
	return *r
}

// AssertStringProperty asserts that a resource property is a plain string with
// the expected value.
func AssertStringProperty(t *testing.T, resource Resource, key, expected string) {
	t.Helper()
	val, ok := resource.Properties[key]
	if !ok {
		t.Fatalf("resource %s missing property %s", resource.Label, key)
	}
	str, ok := val.(string)
	if !ok {
		t.Fatalf("resource %s property %s: expected string, got %T: %v", resource.Label, key, val, val)
	}
	if str != expected {
		t.Errorf("resource %s property %s: got %q, want %q", resource.Label, key, str, expected)
	}
}

// StringProperty extracts a string property from a resource, failing the test
// if the property is missing or not a string.
func StringProperty(t *testing.T, resource Resource, key string) string {
	t.Helper()
	val, ok := resource.Properties[key]
	if !ok {
		t.Fatalf("resource %s missing property %s", resource.Label, key)
	}
	str, ok := val.(string)
	if !ok {
		t.Fatalf("resource %s property %s: expected string, got %T: %v", resource.Label, key, val, val)
	}
	return str
}

// AssertResolvable asserts that a property value is a resolvable reference
// with the expected label, type, property, and resolved value. In inventory
// JSON, a resolvable is a map with "$res": true plus metadata fields.
func AssertResolvable(t *testing.T, propValue any, expectedLabel, expectedType, expectedProperty, expectedValue string) {
	t.Helper()
	m, ok := propValue.(map[string]any)
	if !ok {
		t.Fatalf("expected resolvable (map), got %T: %v", propValue, propValue)
	}
	if res, ok := m["$res"].(bool); !ok || !res {
		t.Fatalf("expected $res=true, got %v", m["$res"])
	}
	if m["$label"] != expectedLabel {
		t.Errorf("resolvable $label: got %v, want %s", m["$label"], expectedLabel)
	}
	if m["$type"] != expectedType {
		t.Errorf("resolvable $type: got %v, want %s", m["$type"], expectedType)
	}
	if m["$property"] != expectedProperty {
		t.Errorf("resolvable $property: got %v, want %s", m["$property"], expectedProperty)
	}
	if m["$value"] != expectedValue {
		t.Errorf("resolvable $value: got %v, want %s", m["$value"], expectedValue)
	}
}
