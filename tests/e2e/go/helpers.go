// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
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

// AssertIsResolvable asserts that a property value is a resolvable reference
// with the expected type, property, and resolved value. Unlike AssertResolvable,
// it does not check the label (useful for discovered resources with auto-generated labels).
func AssertIsResolvable(t *testing.T, propValue any, expectedType, expectedProperty, expectedValue string) {
	t.Helper()
	m, ok := propValue.(map[string]any)
	if !ok {
		t.Fatalf("expected resolvable (map), got %T: %v", propValue, propValue)
	}
	if res, ok := m["$res"].(bool); !ok || !res {
		t.Fatalf("expected $res=true, got %v", m["$res"])
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

// AssertResolvableProperty asserts that a resource property is a resolvable
// reference pointing at the expected resource type. Unlike AssertResolvable,
// it does not check the resolved value (useful when the value is an
// AWS-generated ID that can't be predicted).
func AssertResolvableProperty(t *testing.T, resource Resource, key, expectedType string) {
	t.Helper()
	val, ok := resource.Properties[key]
	if !ok {
		t.Fatalf("resource %s missing property %s", resource.Label, key)
	}
	m, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("resource %s property %s: expected resolvable (map), got %T: %v",
			resource.Label, key, val, val)
	}
	if res, ok := m["$res"].(bool); !ok || !res {
		t.Fatalf("resource %s property %s: expected $res=true, got %v",
			resource.Label, key, m["$res"])
	}
	if m["$type"] != expectedType {
		t.Errorf("resource %s property %s: $type got %v, want %s",
			resource.Label, key, m["$type"], expectedType)
	}
}

// FindResourceByNativeIDContains returns the first resource whose NativeID
// contains the given substring. Returns nil if not found.
func FindResourceByNativeIDContains(resources []Resource, substring string) *Resource {
	for i := range resources {
		if strings.Contains(resources[i].NativeID, substring) {
			return &resources[i]
		}
	}
	return nil
}

// RequireResourceByNativeID finds a resource by NativeID substring and fails
// the test if not found.
func RequireResourceByNativeID(t *testing.T, resources []Resource, nativeIDSubstring string) Resource {
	t.Helper()
	r := FindResourceByNativeIDContains(resources, nativeIDSubstring)
	if r == nil {
		t.Fatalf("resource with NativeID containing %q not found in %d resources", nativeIDSubstring, len(resources))
	}
	return *r
}

// FilterUnmanaged returns only unmanaged resources from the given slice.
func FilterUnmanaged(resources []Resource) []Resource {
	var unmanaged []Resource
	for _, r := range resources {
		if !r.Managed {
			unmanaged = append(unmanaged, r)
		}
	}
	return unmanaged
}

// FilterByNativeIDContains returns resources whose NativeID contains the given
// substring. Useful for filtering test resources by naming convention prefix.
func FilterByNativeIDContains(resources []Resource, substring string) []Resource {
	var filtered []Resource
	for _, r := range resources {
		if strings.Contains(r.NativeID, substring) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// WaitForDiscoveryByNames polls inventory with retries, triggering discovery
// cycles, until all expectedNames are found as substrings in unmanaged resource
// NativeIDs. Returns all unmanaged resources once all expected names are found.
// This is more precise than count-based waiting and avoids false positives from
// parallel tests or leftover resources.
func WaitForDiscoveryByNames(t *testing.T, cli *FormaeCLI, expectedNames []string, timeout time.Duration) []Resource {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var resources []Resource
	for time.Now().Before(deadline) {
		resources = cli.Inventory(t, "--query", "managed:false")
		missing := missingNames(resources, expectedNames)
		if len(missing) == 0 {
			return resources
		}
		t.Logf("waiting for discovery: %d/%d found, missing: %v — triggering another cycle",
			len(expectedNames)-len(missing), len(expectedNames), missing)
		cli.ForceDiscover(t)
		time.Sleep(5 * time.Second)
	}
	missing := missingNames(resources, expectedNames)
	t.Fatalf("timeout waiting for discovery of %d resources, still missing: %v",
		len(expectedNames), missing)
	return nil
}

// missingNames returns the subset of expectedNames that are not found as
// substrings of any unmanaged resource's NativeID.
func missingNames(resources []Resource, expectedNames []string) []string {
	var missing []string
	for _, name := range expectedNames {
		found := false
		for _, r := range resources {
			if strings.Contains(r.NativeID, name) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, name)
		}
	}
	return missing
}

// AssertTagExists checks that the resource has a tag list under the given
// property key containing a tag with the specified Key and Value.
func AssertTagExists(t *testing.T, resource Resource, propertyKey, expectedKey, expectedValue string) {
	t.Helper()

	tagsRaw, ok := resource.Properties[propertyKey]
	if !ok {
		t.Fatalf("resource %s missing property %s", resource.Label, propertyKey)
	}

	tags, ok := tagsRaw.([]any)
	if !ok {
		t.Fatalf("resource %s property %s: expected []any, got %T: %v",
			resource.Label, propertyKey, tagsRaw, tagsRaw)
	}

	for _, tagRaw := range tags {
		tag, ok := tagRaw.(map[string]any)
		if !ok {
			continue
		}
		key, _ := tag["Key"].(string)
		value, _ := tag["Value"].(string)
		if key == expectedKey && value == expectedValue {
			return
		}
	}

	t.Errorf("resource %s: tag Key=%q Value=%q not found in %s",
		resource.Label, expectedKey, expectedValue, propertyKey)
}

// HasTag checks if the resource has a tag with the given key and value.
func HasTag(resource Resource, propertyKey, expectedKey, expectedValue string) bool {
	tagsRaw, ok := resource.Properties[propertyKey]
	if !ok {
		return false
	}
	tags, ok := tagsRaw.([]any)
	if !ok {
		return false
	}
	for _, tagRaw := range tags {
		tag, ok := tagRaw.(map[string]any)
		if !ok {
			continue
		}
		key, _ := tag["Key"].(string)
		value, _ := tag["Value"].(string)
		if key == expectedKey && value == expectedValue {
			return true
		}
	}
	return false
}

// WaitForOOBChange polls inventory with sync triggers until the given tag
// appears on the resource, confirming the agent has detected an out-of-band
// change. This replaces naive time.Sleep-based waits.
func WaitForOOBChange(t *testing.T, cli *FormaeCLI, query, resourceLabel, tagProperty, tagKey, tagValue string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resources := cli.Inventory(t, "--query", query)
		r := FindResource(resources, resourceLabel)
		if r != nil && HasTag(*r, tagProperty, tagKey, tagValue) {
			t.Logf("OOB change detected: tag %s=%s on %s", tagKey, tagValue, resourceLabel)
			return
		}
		t.Logf("waiting for OOB change detection on %s, triggering sync", resourceLabel)
		cli.ForceSync(t)
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("timeout waiting for OOB tag %s=%s on resource %s", tagKey, tagValue, resourceLabel)
}

// WaitForOOBChangeGone polls inventory with sync triggers until the given
// property key is no longer present on the resource. Used to confirm that a
// force reconcile has successfully overwritten OOB changes.
func WaitForOOBChangeGone(t *testing.T, cli *FormaeCLI, query, resourceLabel, propertyKey string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resources := cli.Inventory(t, "--query", query)
		r := FindResource(resources, resourceLabel)
		if r != nil {
			if _, has := r.Properties[propertyKey]; !has {
				t.Logf("OOB property %s removed from %s", propertyKey, resourceLabel)
				return
			}
		}
		t.Logf("waiting for property %s to be removed from %s, triggering sync", propertyKey, resourceLabel)
		cli.ForceSync(t)
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("timeout waiting for property %s to be removed from resource %s", propertyKey, resourceLabel)
}

// SetExtractedStackLabel reads the extracted PKL file, replaces the
// commented-out $unmanaged stack label with the given stackLabel, and writes
// the file back. The extract command generates:
//
//	// Please provide a stack to bring the resources in this Forma under management
//	// label = ""
//
// This function replaces those two lines with an uncommented label assignment.
func SetExtractedStackLabel(t *testing.T, pklPath string, stackLabel string) {
	t.Helper()

	data, err := os.ReadFile(pklPath)
	if err != nil {
		t.Fatalf("failed to read extracted PKL file %s: %v", pklPath, err)
	}

	content := string(data)
	old := "// Please provide a stack to bring the resources in this Forma under management\n    // label = \"\""
	replacement := fmt.Sprintf("label = %q", stackLabel)

	if !strings.Contains(content, old) {
		t.Fatalf("extracted PKL does not contain expected $unmanaged stack comment:\n%s", content)
	}

	content = strings.Replace(content, old, replacement, 1)

	if err := os.WriteFile(pklPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write modified PKL file %s: %v", pklPath, err)
	}
}

// verifyAWSRoleDeleted uses the AWS IAM SDK to confirm that the given role
// no longer exists. It expects a NoSuchEntity error from GetRole.
func verifyAWSRoleDeleted(t *testing.T, roleName string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-west-2"))
	if err != nil {
		t.Fatalf("failed to load AWS config: %v", err)
	}

	client := iam.NewFromConfig(cfg)
	_, err = client.GetRole(ctx, &iam.GetRoleInput{
		RoleName: &roleName,
	})
	if err == nil {
		t.Errorf("expected IAM role %q to be deleted, but GetRole succeeded", roleName)
		return
	}

	if !strings.Contains(err.Error(), "NoSuchEntity") {
		t.Errorf("expected NoSuchEntity error for role %q, got: %v", roleName, err)
	}
}
