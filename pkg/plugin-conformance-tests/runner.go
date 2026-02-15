// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// filterTestCases filters test cases based on the FORMAE_TEST_FILTER environment variable.
// The filter matches against test case names (e.g., "AWS::s3-bucket") or file paths.
// Multiple filters can be comma-separated (e.g., "s3-bucket,ec2-instance").
// If no filter is set, all test cases are returned.
func filterTestCases(t *testing.T, testCases []TestCase) []TestCase {
	filter := os.Getenv("FORMAE_TEST_FILTER")
	if filter == "" {
		return testCases
	}

	// Split filter into multiple patterns (comma-separated)
	patterns := strings.Split(filter, ",")
	for i := range patterns {
		patterns[i] = strings.TrimSpace(patterns[i])
	}

	var filtered []TestCase
	for _, tc := range testCases {
		for _, pattern := range patterns {
			if pattern == "" {
				continue
			}
			// Match against test name, resource type, or PKL file path
			if strings.Contains(strings.ToLower(tc.Name), strings.ToLower(pattern)) ||
				strings.Contains(strings.ToLower(tc.ResourceType), strings.ToLower(pattern)) ||
				strings.Contains(strings.ToLower(tc.PKLFile), strings.ToLower(pattern)) {
				filtered = append(filtered, tc)
				break // Don't add the same test case twice
			}
		}
	}

	if len(filtered) == 0 {
		t.Fatalf("FORMAE_TEST_FILTER=%q matched no test cases. Available: %v", filter, testCaseNames(testCases))
	}

	t.Logf("Filter %q matched %d of %d test case(s)", filter, len(filtered), len(testCases))
	return filtered
}

// testCaseNames returns a slice of test case names for error messages.
func testCaseNames(testCases []TestCase) []string {
	names := make([]string, len(testCases))
	for i, tc := range testCases {
		names[i] = tc.Name
	}
	return names
}

// TestType represents the type of conformance tests to run.
type TestType string

const (
	// TestTypeAll runs both CRUD and discovery tests (default).
	TestTypeAll TestType = "all"
	// TestTypeCRUD runs only CRUD lifecycle tests.
	TestTypeCRUD TestType = "crud"
	// TestTypeDiscovery runs only discovery tests.
	TestTypeDiscovery TestType = "discovery"
)

// getTestType returns the test type from FORMAE_TEST_TYPE environment variable.
// Valid values are: "all" (default), "crud", "discovery".
func getTestType() TestType {
	testType := strings.ToLower(os.Getenv("FORMAE_TEST_TYPE"))
	switch testType {
	case "crud":
		return TestTypeCRUD
	case "discovery":
		return TestTypeDiscovery
	default:
		return TestTypeAll
	}
}

// isParallelEnabled returns true if FORMAE_TEST_PARALLEL is set to a value > 1.
// This enables parallel test execution when used with `go test -parallel N`.
func isParallelEnabled() bool {
	if val := os.Getenv("FORMAE_TEST_PARALLEL"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 1 {
			return true
		}
	}
	return false
}

// getOperationTimeout returns the timeout duration for long-running operations.
// It reads from FORMAE_TEST_TIMEOUT environment variable (in minutes).
// Default is 5 minutes. For resources like Cloud SQL that take longer, set to 15.
func getOperationTimeout() time.Duration {
	if val := os.Getenv("FORMAE_TEST_TIMEOUT"); val != "" {
		if minutes, err := strconv.Atoi(val); err == nil && minutes > 0 {
			return time.Duration(minutes) * time.Minute
		}
	}
	return 5 * time.Minute // Default timeout
}

// RunCRUDTests discovers test cases from the testdata directory and runs
// the standard CRUD lifecycle test for each resource type.
//
// For each test case:
//   - Evaluates the PKL file to get expected state
//   - Waits for the plugin to register
//   - Creates the resource via formae apply
//   - Reads and verifies the resource via inventory
//   - Extracts and verifies the resource (if extractable)
//   - Forces synchronization to read actual state from cloud
//   - Verifies idempotency (resource state unchanged after sync)
//   - Updates the resource (if update file exists)
//   - Replaces the resource (if replace file exists)
//   - Deletes the resource via formae destroy
//
// Environment variables:
//   - FORMAE_BINARY: Path to the formae binary (required)
//   - FORMAE_TEST_FILTER: Filter test cases by name, resource type, or file path (optional).
//     Supports comma-separated patterns for multiple filters.
//     Examples: "s3-bucket", "ec2-instance,vpc", "testdata/network"
//   - FORMAE_TEST_TYPE: Select which tests to run (optional).
//     Values: "all" (default), "crud", "discovery".
//     When set to "discovery", CRUD tests are skipped.
//   - FORMAE_TEST_TIMEOUT: Timeout in minutes for long-running operations (optional).
//     Default is 5 minutes. Set to 15 for slow resources like Cloud SQL.
//
// This function should be called from a plugin's conformance_test.go:
//
//	func TestPluginConformance(t *testing.T) {
//	    conformance.RunCRUDTests(t)
//	}
func RunCRUDTests(t *testing.T) {
	// Skip if not running conformance tests
	if os.Getenv("FORMAE_BINARY") == "" {
		t.Skip("Skipping conformance test: FORMAE_BINARY not set. Run 'make conformance-test' instead.")
	}

	// Skip if test type is discovery-only
	if getTestType() == TestTypeDiscovery {
		t.Skip("Skipping CRUD tests: FORMAE_TEST_TYPE=discovery")
	}

	// Find plugin directory (where the test is running from)
	pluginDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Discover test cases from testdata directory
	testCases, err := DiscoverTestData(pluginDir)
	if err != nil {
		t.Fatalf("failed to discover test cases: %v", err)
	}

	// Apply filter if FORMAE_TEST_FILTER is set
	testCases = filterTestCases(t, testCases)

	t.Logf("Discovered %d test case(s)", len(testCases))

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			runCRUDTest(t, tc)
		})
	}
}

// findTargetResource finds the resource matching the test case's ResourceType from a list of resources.
// Returns the matching resource, or falls back to the last resource if no match found (dependencies are listed first).
func findTargetResource(resources []map[string]any, resourceType string) map[string]any {
	targetTypeNorm := strings.ToLower(strings.ReplaceAll(resourceType, "-", ""))
	for _, res := range resources {
		if resType, ok := res["Type"].(string); ok {
			// Normalize the type (e.g., "Provider::Service::ResourceName" -> "resourcename")
			typeParts := strings.Split(resType, "::")
			typeName := strings.ToLower(typeParts[len(typeParts)-1])
			if typeName == targetTypeNorm {
				return res
			}
		}
	}
	// Fallback to last resource if no match found
	return resources[len(resources)-1]
}

// isResolvable checks if a value is a resolvable reference ($res: true)
func isResolvable(value any) bool {
	m, ok := value.(map[string]any)
	if !ok {
		return false
	}
	res, exists := m["$res"]
	if !exists {
		return false
	}
	resBool, ok := res.(bool)
	return ok && resBool
}

// normalizeEscaping removes common backslash escape sequences from a string
// to allow comparison of values that may have been escaped differently during
// JSON/PKL round-trips (e.g. `"` vs `\"`).
func normalizeEscaping(s string) string {
	r := strings.NewReplacer(`\"`, `"`, `\\`, `\`)
	return r.Replace(s)
}

// compareResolvable validates that an actual resolvable has a resolved $value
// and that its metadata fields match the expected resolvable.
// If the actual value is not a resolvable (e.g. after extraction, where
// resolvables are resolved to plain values), the comparison is skipped.
func compareResolvable(t *testing.T, name string, expected, actual any, context string) bool {
	if !isResolvable(actual) {
		t.Logf("Skipping resolvable comparison for %s: actual is a resolved value (%s)", name, context)
		return true
	}

	expectedMap := expected.(map[string]any)
	actualMap := actual.(map[string]any)

	resolvedValue, hasValue := actualMap["$value"]
	if !hasValue || resolvedValue == "" {
		// $value may be absent after extraction — the resolvable reference is
		// preserved but the resolved value is not included. This is expected.
		t.Logf("Resolvable %s has no resolved $value yet (%s)", name, context)
	} else {
		t.Logf("Resolvable %s resolved to: %v", name, resolvedValue)
	}

	ok := true
	for _, field := range []string{"$label", "$type", "$stack", "$property"} {
		expectedFieldValue, expectedHasField := expectedMap[field]
		actualFieldValue, actualHasField := actualMap[field]
		if expectedHasField && actualHasField && expectedFieldValue != actualFieldValue {
			t.Errorf("Resolvable %s field %s mismatch: expected %v, got %v (%s)",
				name, field, expectedFieldValue, actualFieldValue, context)
			ok = false
		}
	}
	return ok
}

// resolvableMetadataKey returns a string key from a resolvable's metadata fields
// for use in matching expected resolvables to actual ones in arrays.
func resolvableMetadataKey(v any) string {
	m := v.(map[string]any)
	label, _ := m["$label"].(string)
	typ, _ := m["$type"].(string)
	stack, _ := m["$stack"].(string)
	prop, _ := m["$property"].(string)
	return label + "|" + typ + "|" + stack + "|" + prop
}

// compareArrayUnordered compares two arrays ignoring element order.
// If the array contains resolvable elements, it uses element-wise matching
// that validates resolvable metadata and $value. Otherwise, elements are
// serialized to JSON for canonical comparison.
func compareArrayUnordered(t *testing.T, key string, expected, actual any, context string) bool {
	expectedArr, ok := expected.([]any)
	if !ok {
		t.Errorf("Expected %s is not an array (%s)", key, context)
		return false
	}
	actualArr, ok := actual.([]any)
	if !ok {
		t.Errorf("Actual %s is not an array (%s)", key, context)
		return false
	}

	if len(expectedArr) != len(actualArr) {
		t.Errorf("Array %s length mismatch: expected %d, got %d (%s)", key, len(expectedArr), len(actualArr), context)
		return false
	}

	// Check if any expected element is a resolvable
	hasResolvables := false
	for _, v := range expectedArr {
		if isResolvable(v) {
			hasResolvables = true
			break
		}
	}

	// If array contains resolvables, use element-wise matching
	if hasResolvables {
		return compareArrayWithResolvables(t, key, expectedArr, actualArr, context)
	}

	// Fast path: serialize to JSON, sort, and compare
	serialize := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}

	expectedSorted := make([]string, len(expectedArr))
	for i, v := range expectedArr {
		expectedSorted[i] = serialize(v)
	}
	sort.Strings(expectedSorted)

	actualSorted := make([]string, len(actualArr))
	for i, v := range actualArr {
		actualSorted[i] = serialize(v)
	}
	sort.Strings(actualSorted)

	for i := range expectedSorted {
		if expectedSorted[i] != actualSorted[i] {
			t.Errorf("Array %s mismatch at sorted index %d (%s): expected %s, got %s",
				key, i, context, expectedSorted[i], actualSorted[i])
			return false
		}
	}

	return true
}

// compareArrayWithResolvables compares arrays element-wise, handling resolvable
// elements by matching on metadata and validating $value, and non-resolvable
// elements by JSON equality.
func compareArrayWithResolvables(t *testing.T, key string, expectedArr, actualArr []any, context string) bool {
	ok := true
	matched := make([]bool, len(actualArr))

	serialize := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}

	// Check if actual array has any resolvables (it won't after extraction)
	actualHasResolvables := false
	for _, v := range actualArr {
		if isResolvable(v) {
			actualHasResolvables = true
			break
		}
	}

	for i, exp := range expectedArr {
		elemName := fmt.Sprintf("%s[%d]", key, i)
		found := false

		if isResolvable(exp) {
			if !actualHasResolvables {
				// After extraction, resolvables become plain values — skip comparison
				t.Logf("Skipping resolvable comparison for %s: actual array has resolved values (%s)", elemName, context)
				found = true
			} else {
				expKey := resolvableMetadataKey(exp)
				for j, act := range actualArr {
					if matched[j] || !isResolvable(act) {
						continue
					}
					if resolvableMetadataKey(act) == expKey {
						if !compareResolvable(t, elemName, exp, act, context) {
							ok = false
						}
						matched[j] = true
						found = true
						break
					}
				}
			}
		} else {
			expJSON := serialize(exp)
			for j, act := range actualArr {
				if matched[j] {
					continue
				}
				if serialize(act) == expJSON {
					matched[j] = true
					found = true
					break
				}
			}
		}

		if !found {
			t.Errorf("Array %s: no match found for expected element %d (%s): %s",
				key, i, context, serialize(exp))
			ok = false
		}
	}

	return ok
}

// extractHasProviderDefaultFields returns dot-separated field paths that have
// HasProviderDefault=true in the resource's Schema.Hints map.
func extractHasProviderDefaultFields(expectedResource map[string]any) []string {
	schema, ok := expectedResource["Schema"].(map[string]any)
	if !ok {
		return nil
	}
	hints, ok := schema["Hints"].(map[string]any)
	if !ok {
		return nil
	}

	var fields []string
	for fieldPath, hint := range hints {
		hintMap, ok := hint.(map[string]any)
		if !ok {
			continue
		}
		if hasDefault, ok := hintMap["HasProviderDefault"].(bool); ok && hasDefault {
			fields = append(fields, fieldPath)
		}
	}
	return fields
}

// removeProviderDefaultsFromActual removes fields with provider defaults from actual
// properties, but only if those fields are NOT present in expected properties.
// This prevents false failures when cloud providers return default values for fields
// that were not specified in the test PKL file.
func removeProviderDefaultsFromActual(actual, expected map[string]any, fields []string) {
	for _, fieldPath := range fields {
		pathParts := strings.Split(fieldPath, ".")
		if !fieldExistsInNestedMap(expected, pathParts) {
			removeNestedField(actual, pathParts)
		}
	}
}

// fieldExistsInNestedMap checks if a field at the given dot-separated path exists
// in a nested map structure.
func fieldExistsInNestedMap(obj map[string]any, path []string) bool {
	if len(path) == 0 {
		return false
	}

	val, exists := obj[path[0]]
	if !exists {
		return false
	}

	if len(path) == 1 {
		return true
	}

	if nested, ok := val.(map[string]any); ok {
		return fieldExistsInNestedMap(nested, path[1:])
	}

	return false
}

// removeNestedField removes a field at the given path from a nested map structure.
func removeNestedField(obj map[string]any, path []string) {
	if len(path) == 0 {
		return
	}

	if len(path) == 1 {
		delete(obj, path[0])
		return
	}

	if nested, ok := obj[path[0]].(map[string]any); ok {
		removeNestedField(nested, path[1:])
	}
}

// deepCopyMap creates a deep copy of a map[string]any to avoid mutating the original.
func deepCopyMap(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var copy map[string]any
	_ = json.Unmarshal(b, &copy)
	return copy
}

// compareProperties compares expected properties against actual properties from inventory.
// hasProviderDefaultFields contains dot-separated field paths that have provider defaults.
// Fields with provider defaults are stripped from actual properties before comparison,
// but only if they are NOT present in expected properties.
func compareProperties(t *testing.T, expectedProperties map[string]any, actualResource map[string]any, context string, hasProviderDefaultFields []string) bool {
	hasErrors := false

	actualProperties, ok := actualResource["Properties"].(map[string]any)
	if !ok {
		t.Errorf("Could not extract Properties from inventory resource (%s)", context)
		return false
	}

	// Strip provider-default fields from actual properties (deep copy to avoid mutation)
	if len(hasProviderDefaultFields) > 0 {
		actualProperties = deepCopyMap(actualProperties)
		removeProviderDefaultsFromActual(actualProperties, expectedProperties, hasProviderDefaultFields)
	}

	t.Logf("Comparing expected properties with actual properties (%s)...", context)
	for key, expectedValue := range expectedProperties {
		actualValue, exists := actualProperties[key]
		if !exists {
			t.Errorf("Property %s should exist in actual resource (%s)", key, context)
			hasErrors = true
			continue
		}

		// Validate resolvable properties - they should be resolved at apply time
		if isResolvable(expectedValue) {
			t.Logf("Validating resolvable property %s (resolved at runtime)", key)
			if !compareResolvable(t, key, expectedValue, actualValue, context) {
				hasErrors = true
			}
			continue
		}

		// Use order-independent comparison for all arrays
		if _, isArray := expectedValue.([]any); isArray {
			if !compareArrayUnordered(t, key, expectedValue, actualValue, context) {
				hasErrors = true
			}
		} else {
			expectedStr := fmt.Sprintf("%v", expectedValue)
			actualStr := fmt.Sprintf("%v", actualValue)
			if expectedStr != actualStr {
				// Normalize escaped strings before failing — extraction round-trips
				// can introduce or remove backslash escaping (e.g. " vs \")
				if normalizeEscaping(expectedStr) != normalizeEscaping(actualStr) {
					t.Errorf("Property %s should match expected value (%s): expected %v, got %v",
						key, context, expectedValue, actualValue)
					hasErrors = true
				}
			}
		}
	}

	if !hasErrors {
		t.Logf("All expected properties matched (%s)!", context)
	}
	return !hasErrors
}

// runCRUDTest runs the full CRUD lifecycle for a single test case.
// This matches the structure of formae-internal's runLifecycleTest exactly.
func runCRUDTest(t *testing.T, tc TestCase) {
	if isParallelEnabled() {
		t.Parallel()
	}
	t.Logf("Testing resource: %s (file: %s)", tc.Name, tc.PKLFile)

	// Track if all property comparisons pass (used for final success message)
	allPropertiesMatched := true

	// Create test harness
	harness := NewTestHarness(t)
	defer harness.Cleanup()

	// Start the formae agent
	if err := harness.StartAgent(); err != nil {
		t.Fatalf("failed to start agent: %v", err)
	}

	// === Step 1: Eval the PKL file to get expected state ===
	t.Log("Step 1: Evaluating PKL file to get expected state...")
	expectedOutput, err := harness.Eval(tc.PKLFile)
	if err != nil {
		t.Fatalf("Eval command failed: %v", err)
	}

	// Parse eval output to get expected properties
	var evalResult InventoryResponse
	if err := json.Unmarshal([]byte(expectedOutput), &evalResult); err != nil {
		t.Fatalf("Failed to parse eval output: %v\nRaw output:\n%s", err, expectedOutput)
	}
	if len(evalResult.Resources) == 0 {
		t.Fatal("Eval should return at least one resource")
	}

	// Find the target resource by matching the test case's ResourceType to the resource Type
	expectedResource := findTargetResource(evalResult.Resources, tc.ResourceType)

	actualResourceType, ok := expectedResource["Type"].(string)
	if !ok {
		t.Fatal("Expected resource should have Type field")
	}
	expectedProperties, ok := expectedResource["Properties"].(map[string]any)
	if !ok {
		t.Fatal("Expected resource should have Properties field")
	}
	t.Logf("Expected resource type: %s", actualResourceType)

	// Extract fields with provider defaults from schema hints
	hasProviderDefaultFields := extractHasProviderDefaultFields(expectedResource)
	if len(hasProviderDefaultFields) > 0 {
		t.Logf("Found %d field(s) with provider defaults: %v", len(hasProviderDefaultFields), hasProviderDefaultFields)
	}

	// === Step 2: Wait for plugin to be registered before any commands ===
	t.Log("Step 2: Waiting for plugin to be registered...")
	namespace := strings.Split(actualResourceType, "::")[0]
	if err := harness.WaitForPluginRegistered(namespace, 30*time.Second); err != nil {
		t.Fatalf("Plugin should register within timeout: %v", err)
	}

	// === Step 3: Apply the forma to create the resource ===
	t.Log("Step 3: Applying forma to create resource...")
	applyCommandID, err := harness.Apply(tc.PKLFile)
	if err != nil {
		t.Fatalf("Apply command failed: %v", err)
	}
	if applyCommandID == "" {
		t.Fatal("Apply should return a command ID")
	}

	// === Step 4: Poll for apply command to complete successfully ===
	t.Log("Step 4: Polling for apply command completion...")
	applyStatus, err := harness.PollStatus(applyCommandID, getOperationTimeout())
	if err != nil {
		t.Fatalf("Apply command should complete successfully: %v", err)
	}
	if applyStatus != "Success" {
		t.Fatalf("Apply command should reach Success state, got: %s", applyStatus)
	}

	// === Step 5: Verify resource exists in inventory with correct properties ===
	t.Log("Step 5: Verifying resource in inventory...")
	inventory, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
	if err != nil {
		t.Fatalf("Inventory command failed: %v", err)
	}
	if inventory == nil {
		t.Fatal("Inventory should return a response")
	}
	if len(inventory.Resources) != 1 {
		t.Fatalf("Inventory should contain exactly 1 resource, got %d", len(inventory.Resources))
	}

	actualResource := inventory.Resources[0]
	t.Logf("Found resource in inventory with type: %s", actualResource["Type"])

	// Verify the resource type matches
	if actualResource["Type"] != actualResourceType {
		t.Errorf("Resource type should match: expected %s, got %s", actualResourceType, actualResource["Type"])
	}

	// Compare properties using helper
	if !compareProperties(t, expectedProperties, actualResource, "after create", hasProviderDefaultFields) {
		allPropertiesMatched = false
	}

	// === Step 6: Extract resource and verify it matches original (if extractable) ===
	// Check extractable from the eval output's Schema field
	shouldSkipExtract := false
	if schema, ok := expectedResource["Schema"].(map[string]any); ok {
		if extractable, ok := schema["Extractable"].(bool); ok && !extractable {
			t.Logf("Skipping extract validation: resource type %s has extractable=false", actualResourceType)
			shouldSkipExtract = true
		}
	}

	if !shouldSkipExtract {
		t.Log("Step 6: Extracting resource to PKL and verifying...")

		// Create temp directory for extracted files
		extractDir, err := os.MkdirTemp("", "formae-extract-test-*")
		if err != nil {
			t.Fatalf("Failed to create temp directory for extraction: %v", err)
		}
		defer os.RemoveAll(extractDir)
		t.Logf("Created extract directory: %s", extractDir)

		// Extract the resource
		extractFile := filepath.Join(extractDir, "extracted.pkl")
		if err := harness.Extract(fmt.Sprintf("type:%s", actualResourceType), extractFile); err != nil {
			t.Fatalf("Extract command failed: %v", err)
		}

		// Eval the extracted PKL file
		extractedOutput, err := harness.Eval(extractFile)
		if err != nil {
			t.Fatalf("Eval of extracted file failed: %v", err)
		}

		// Parse extracted eval output
		var extractedResult InventoryResponse
		if err := json.Unmarshal([]byte(extractedOutput), &extractedResult); err != nil {
			t.Fatalf("Failed to parse extracted eval output: %v", err)
		}
		if len(extractedResult.Resources) == 0 {
			t.Fatal("Extracted eval should return at least one resource")
		}

		// Get the extracted resource
		extractedResource := extractedResult.Resources[0]

		// Compare properties using the same logic as inventory comparison
		if !compareProperties(t, expectedProperties, extractedResource, "after extract", hasProviderDefaultFields) {
			allPropertiesMatched = false
		}
		t.Log("Extract validation completed!")
	} else {
		t.Log("Step 6: Skipping extract validation")
	}

	// === Step 7: Force synchronization to read actual state from cloud ===
	t.Log("Step 7: Forcing synchronization...")
	if err := harness.Sync(); err != nil {
		t.Fatalf("Sync command failed: %v", err)
	}

	// === Step 8: Wait for synchronization to complete ===
	t.Log("Step 8: Waiting for synchronization to complete...")
	if err := harness.WaitForSyncCompletion(60 * time.Second); err != nil {
		t.Fatalf("Synchronization should complete successfully: %v", err)
	}

	// === Step 9: Verify inventory still matches expected state (idempotency check) ===
	t.Log("Step 9: Verifying resource state unchanged after sync (idempotency)...")
	inventoryAfterSync, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
	if err != nil {
		t.Fatalf("Inventory command failed after sync: %v", err)
	}
	if len(inventoryAfterSync.Resources) != 1 {
		t.Fatalf("Inventory should still contain exactly 1 resource after sync, got %d", len(inventoryAfterSync.Resources))
	}

	resourceAfterSync := inventoryAfterSync.Resources[0]

	// Compare properties using helper - verifies idempotency
	if !compareProperties(t, expectedProperties, resourceAfterSync, "after sync", hasProviderDefaultFields) {
		allPropertiesMatched = false
	}
	t.Log("Idempotency verified!")

	// === Step 10: Update test (if update file exists) ===
	if tc.UpdateFile != "" {
		t.Log("Step 10: Running update test using update file...")

		// Store current NativeID to verify it doesn't change during update
		inventoryBeforeUpdate, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
		if err != nil {
			t.Fatalf("Inventory command failed before update: %v", err)
		}
		if len(inventoryBeforeUpdate.Resources) != 1 {
			t.Fatal("Inventory should contain exactly 1 resource before update")
		}
		oldNativeID, ok := inventoryBeforeUpdate.Resources[0]["NativeID"].(string)
		if !ok {
			t.Fatal("Resource should have NativeID field")
		}
		t.Logf("Current NativeID before update: %s", oldNativeID)

		// Eval update file for expected state
		updateExpected, err := harness.Eval(tc.UpdateFile)
		if err != nil {
			t.Fatalf("Update file eval failed: %v", err)
		}

		// Parse to get expected properties
		var updateEvalResult InventoryResponse
		if err := json.Unmarshal([]byte(updateExpected), &updateEvalResult); err != nil {
			t.Fatalf("Failed to parse update eval output: %v", err)
		}
		if len(updateEvalResult.Resources) == 0 {
			t.Fatal("Update eval should return at least one resource")
		}

		// Find the target resource
		updateExpectedResource := findTargetResource(updateEvalResult.Resources, tc.ResourceType)
		updateExpectedProperties, ok := updateExpectedResource["Properties"].(map[string]any)
		if !ok {
			t.Fatal("Update expected resource should have Properties field")
		}
		updateHasProviderDefaultFields := extractHasProviderDefaultFields(updateExpectedResource)

		// Apply with patch mode
		updateCmdID, err := harness.ApplyWithMode(tc.UpdateFile, "patch")
		if err != nil {
			t.Fatalf("Update apply failed: %v", err)
		}
		if updateCmdID == "" {
			t.Fatal("Update apply should return a command ID")
		}

		// === Step 11: Poll for update command completion ===
		t.Log("Step 11: Polling for update command completion...")
		updateStatus, err := harness.PollStatus(updateCmdID, getOperationTimeout())
		if err != nil {
			t.Fatalf("Update command should complete successfully: %v", err)
		}
		if updateStatus != "Success" {
			t.Fatalf("Update command should reach Success state, got: %s", updateStatus)
		}

		// === Step 12: Verify resource properties after update ===
		t.Log("Step 12: Verifying resource properties after update...")
		inventoryAfterUpdate, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
		if err != nil {
			t.Fatalf("Inventory command failed after update: %v", err)
		}
		if len(inventoryAfterUpdate.Resources) != 1 {
			t.Fatal("Inventory should still contain exactly 1 resource after update")
		}

		resourceAfterUpdate := inventoryAfterUpdate.Resources[0]

		// Verify NativeID did NOT change (proves resource was updated, not replaced)
		newNativeID, ok := resourceAfterUpdate["NativeID"].(string)
		if !ok {
			t.Fatal("Resource should have NativeID field after update")
		}
		t.Logf("NativeID after update: %s", newNativeID)
		if oldNativeID != newNativeID {
			t.Errorf("NativeID should NOT change during update (old: %s, new: %s) - if it changed, a createOnly property was modified",
				oldNativeID, newNativeID)
		}

		if !compareProperties(t, updateExpectedProperties, resourceAfterUpdate, "after update", updateHasProviderDefaultFields) {
			allPropertiesMatched = false
		}
		t.Log("Update test completed successfully!")
	} else {
		t.Log("No update file found, skipping update test")
	}

	// === Step 13: Replace test (if replace file exists) ===
	if tc.ReplaceFile != "" {
		t.Log("Step 13: Running replace test using replace file...")

		// Store current NativeID for comparison
		inventoryBeforeReplace, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
		if err != nil {
			t.Fatalf("Inventory command failed before replace: %v", err)
		}
		if len(inventoryBeforeReplace.Resources) != 1 {
			t.Fatal("Inventory should contain exactly 1 resource before replace")
		}
		oldNativeID, ok := inventoryBeforeReplace.Resources[0]["NativeID"].(string)
		if !ok {
			t.Fatal("Resource should have NativeID field")
		}
		t.Logf("Current NativeID before replace: %s", oldNativeID)

		// Eval replace file
		replaceExpected, err := harness.Eval(tc.ReplaceFile)
		if err != nil {
			t.Fatalf("Replace file eval failed: %v", err)
		}

		// Parse to get expected properties
		var replaceEvalResult InventoryResponse
		if err := json.Unmarshal([]byte(replaceExpected), &replaceEvalResult); err != nil {
			t.Fatalf("Failed to parse replace eval output: %v", err)
		}
		if len(replaceEvalResult.Resources) == 0 {
			t.Fatal("Replace eval should return at least one resource")
		}

		// Find the target resource
		replaceExpectedResource := findTargetResource(replaceEvalResult.Resources, tc.ResourceType)
		replaceExpectedProperties, ok := replaceExpectedResource["Properties"].(map[string]any)
		if !ok {
			t.Fatal("Replace expected resource should have Properties field")
		}
		replaceHasProviderDefaultFields := extractHasProviderDefaultFields(replaceExpectedResource)

		// Apply with patch mode (createOnly change triggers delete+create)
		replaceCmdID, err := harness.ApplyWithMode(tc.ReplaceFile, "patch")
		if err != nil {
			t.Fatalf("Replace apply failed: %v", err)
		}
		if replaceCmdID == "" {
			t.Fatal("Replace apply should return a command ID")
		}

		// === Step 14: Poll for replace command completion ===
		t.Log("Step 14: Polling for replace command completion...")
		replaceStatus, err := harness.PollStatus(replaceCmdID, getOperationTimeout())
		if err != nil {
			t.Fatalf("Replace command should complete successfully: %v", err)
		}
		if replaceStatus != "Success" {
			t.Fatalf("Replace command should reach Success state, got: %s", replaceStatus)
		}

		// === Step 15: Verify resource was replaced (NativeID changed) ===
		t.Log("Step 15: Verifying resource was replaced (NativeID changed)...")
		inventoryAfterReplace, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
		if err != nil {
			t.Fatalf("Inventory command failed after replace: %v", err)
		}
		if len(inventoryAfterReplace.Resources) != 1 {
			t.Fatal("Inventory should still contain exactly 1 resource after replace")
		}

		newNativeID, ok := inventoryAfterReplace.Resources[0]["NativeID"].(string)
		if !ok {
			t.Fatal("Resource should have NativeID field after replace")
		}
		t.Logf("New NativeID after replace: %s", newNativeID)

		if oldNativeID == newNativeID {
			t.Errorf("NativeID should change after replace (old: %s, new: %s)", oldNativeID, newNativeID)
		}

		// Verify properties match expected state
		resourceAfterReplace := inventoryAfterReplace.Resources[0]
		if !compareProperties(t, replaceExpectedProperties, resourceAfterReplace, "after replace", replaceHasProviderDefaultFields) {
			allPropertiesMatched = false
		}

		t.Log("Replace test completed successfully - resource was recreated with new NativeID!")
	} else {
		t.Log("No replace file found, skipping replace test")
	}

	// === Step 16: Destroy the resource ===
	t.Log("Step 16: Destroying resource...")
	destroyCommandID, err := harness.Destroy(tc.PKLFile)
	if err != nil {
		t.Fatalf("Destroy command failed: %v", err)
	}
	if destroyCommandID == "" {
		t.Fatal("Destroy should return a command ID")
	}

	// === Step 17: Poll for destroy command to complete successfully ===
	t.Log("Step 17: Polling for destroy command completion...")
	destroyStatus, err := harness.PollStatus(destroyCommandID, getOperationTimeout())
	if err != nil {
		t.Fatalf("Destroy command should complete successfully: %v", err)
	}
	if destroyStatus != "Success" {
		t.Fatalf("Destroy command should reach Success state, got: %s", destroyStatus)
	}

	// === Step 18: Verify resource no longer exists in inventory ===
	t.Log("Step 18: Verifying resource removed from inventory...")
	inventoryAfterDestroy, err := harness.Inventory(fmt.Sprintf("type: %s", actualResourceType))
	if err != nil {
		t.Fatalf("Inventory command failed after destroy: %v", err)
	}
	if len(inventoryAfterDestroy.Resources) != 0 {
		t.Errorf("Inventory should be empty after destroy, got %d resources", len(inventoryAfterDestroy.Resources))
	}

	// Only log success if all property comparisons passed
	if allPropertiesMatched {
		t.Logf("Resource lifecycle test completed successfully for %s!", tc.Name)
	}
}

// RunDiscoveryTests tests that the plugin can discover resources created out-of-band.
//
// This test:
//   - Creates a resource directly via the plugin (bypassing formae)
//   - Runs formae discovery
//   - Verifies the resource appears as unmanaged
//   - Cleans up the resource
//
// Environment variables:
//   - FORMAE_BINARY: Path to the formae binary (required)
//   - FORMAE_TEST_FILTER: Filter test cases by name, resource type, or file path (optional).
//     Supports comma-separated patterns for multiple filters.
//     Examples: "s3-bucket", "ec2-instance,vpc", "testdata/network"
//   - FORMAE_TEST_TYPE: Select which tests to run (optional).
//     Values: "all" (default), "crud", "discovery".
//     When set to "crud", discovery tests are skipped.
//
// This function should be called from a plugin's conformance_test.go:
//
//	func TestPluginDiscovery(t *testing.T) {
//	    conformance.RunDiscoveryTests(t)
//	}
func RunDiscoveryTests(t *testing.T) {
	// Skip if not running conformance tests
	if os.Getenv("FORMAE_BINARY") == "" {
		t.Skip("Skipping conformance test: FORMAE_BINARY not set. Run 'make conformance-test' instead.")
	}

	// Skip if test type is CRUD-only
	if getTestType() == TestTypeCRUD {
		t.Skip("Skipping discovery tests: FORMAE_TEST_TYPE=crud")
	}

	// Find plugin directory
	pluginDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Discover test cases
	testCases, err := DiscoverTestData(pluginDir)
	if err != nil {
		t.Fatalf("failed to discover test cases: %v", err)
	}

	if len(testCases) == 0 {
		t.Skip("No test cases found")
	}

	// Apply filter if FORMAE_TEST_FILTER is set
	testCases = filterTestCases(t, testCases)

	t.Logf("Discovered %d test case(s) for discovery testing", len(testCases))

	// Run discovery test for each test case
	for _, tc := range testCases {
		t.Run("Discovery/"+tc.Name, func(t *testing.T) {
			runDiscoveryTest(t, tc)
		})
	}
}

// runDiscoveryTest runs the discovery test for a single test case.
func runDiscoveryTest(t *testing.T, tc TestCase) {
	if isParallelEnabled() {
		t.Parallel()
	}
	// Create test harness
	harness := NewTestHarness(t)
	defer harness.Cleanup()

	// Get resource descriptor to find the resource type for discovery config
	// tc.PKLFile is already the absolute path from DiscoverTestData
	pklFile := tc.PKLFile

	// Evaluate to get resource type
	evalOutput, err := harness.Eval(pklFile)
	if err != nil {
		t.Fatalf("failed to eval PKL file: %v", err)
	}

	// Extract the plugin namespace and resource type from the evaluated forma resources
	namespace, resourceType, err := extractNamespaceFromEvalOutput(evalOutput)
	if err != nil {
		t.Fatalf("failed to extract namespace from eval output: %v", err)
	}
	t.Logf("Extracted plugin namespace: %s, resource type: %s", namespace, resourceType)

	t.Log("Step 1: Creating resource out-of-band via plugin...")

	// Create all resources directly via the plugin (bypassing formae)
	// Use CreateAllUnmanagedResources to get all created resources for cleanup
	createdResources, err := harness.CreateAllUnmanagedResources(evalOutput)
	if err != nil {
		t.Fatalf("failed to create unmanaged resource: %v", err)
	}
	if len(createdResources) == 0 {
		t.Fatal("no resources were created")
	}

	// The main resource is the last one (after dependencies)
	nativeID := createdResources[len(createdResources)-1].NativeID
	t.Logf("Created %d resource(s), main resource NativeID: %s", len(createdResources), nativeID)

	// Parse target from eval output for cleanup
	var forma pkgmodel.Forma
	if err := json.Unmarshal([]byte(evalOutput), &forma); err != nil {
		t.Fatalf("failed to parse forma for cleanup: %v", err)
	}
	if len(forma.Targets) == 0 {
		t.Fatal("no targets found in forma for cleanup")
	}
	target := forma.Targets[0]

	// Register cleanup to delete all created resources (in reverse order - dependents before dependencies)
	harness.RegisterCleanup(func() {
		t.Logf("Cleaning up %d unmanaged resource(s)...", len(createdResources))
		for i := len(createdResources) - 1; i >= 0; i-- {
			res := createdResources[i]
			t.Logf("Deleting unmanaged resource: type=%s, label=%s, nativeID=%s", res.ResourceType, res.Label, res.NativeID)
			if err := harness.DeleteUnmanagedResource(res.ResourceType, res.NativeID, &target); err != nil {
				t.Logf("Warning: failed to delete unmanaged resource %s: %v", res.Label, err)
			}
		}
	})

	// Extract all unique resource types from created resources for discovery configuration.
	// This includes both the main resource type and any parent/dependency types.
	resourceTypes := make([]string, 0)
	seenTypes := make(map[string]bool)
	for _, res := range createdResources {
		if !seenTypes[res.ResourceType] {
			resourceTypes = append(resourceTypes, res.ResourceType)
			seenTypes[res.ResourceType] = true
		}
	}

	// Configure discovery to only scan the resource types being tested (including dependencies).
	// This prevents discovery from scanning all resource types, which causes
	// excessive rate limiting and timeouts.
	if err := harness.ConfigureDiscovery(resourceTypes); err != nil {
		t.Fatalf("failed to configure discovery: %v", err)
	}

	// Start the agent with discovery enabled
	if err := harness.StartAgent(); err != nil {
		t.Fatalf("failed to start agent: %v", err)
	}

	// Step 2: Register target for discovery
	t.Log("Step 2: Registering target for discovery...")
	if err := harness.RegisterTargetForDiscovery([]byte(evalOutput)); err != nil {
		t.Fatalf("failed to register target: %v", err)
	}

	// Step 3: Wait for plugin to register (using extracted namespace, not directory name)
	t.Log("Step 3: Waiting for plugin to register...")
	if err := harness.WaitForPluginRegistered(namespace, 30*time.Second); err != nil {
		t.Fatalf("plugin did not register: %v", err)
	}

	// Step 4: Check if resource is discoverable before continuing
	t.Log("Step 4: Checking if resource type is discoverable...")
	descriptor, err := harness.GetResourceDescriptorFromCoordinator(resourceType)
	if err != nil {
		t.Skipf("Skipping discovery test: failed to get resource descriptor for %s: %v", resourceType, err)
	}
	if !descriptor.Discoverable {
		t.Skipf("Skipping discovery test: resource type %s has discoverable=false", resourceType)
	}

	// Step 5: Trigger discovery
	t.Log("Step 5: Triggering discovery...")
	if err := harness.TriggerDiscovery(); err != nil {
		t.Fatalf("failed to trigger discovery: %v", err)
	}

	// Step 6: Wait for resource to appear in inventory
	t.Log("Step 6: Waiting for resource in inventory...")
	if err := harness.WaitForResourceInInventory(resourceType, nativeID, false, 2*time.Minute); err != nil {
		t.Fatalf("resource not discovered: %v", err)
	}

	// Step 7: Verify the discovered resource
	t.Log("Step 7: Verifying discovered resource...")
	inventory, err := harness.Inventory(fmt.Sprintf("type: %s managed: false", resourceType))
	if err != nil {
		t.Fatalf("failed to query inventory: %v", err)
	}

	// Find our specific resource by NativeID
	var discoveredResource map[string]any
	for _, res := range inventory.Resources {
		if resNativeID, ok := res["NativeID"].(string); ok {
			if resNativeID == nativeID {
				discoveredResource = res
				break
			}
		}
	}

	if discoveredResource == nil {
		t.Fatalf("did not find resource with NativeID %s in discovered resources", nativeID)
	}

	// Verify resource is on the unmanaged stack
	stack, ok := discoveredResource["Stack"].(string)
	if !ok || stack != "$unmanaged" {
		t.Fatalf("discovered resource should be on $unmanaged stack, got: %s", stack)
	}

	// Verify resource type matches
	discoveredType, ok := discoveredResource["Type"].(string)
	if !ok || discoveredType != resourceType {
		t.Fatalf("discovered resource type should be %s, got: %s", resourceType, discoveredType)
	}

	t.Log("Discovery test passed!")
}

// extractNamespaceFromEvalOutput parses the evaluated JSON to extract the plugin namespace
// and resource type from the last cloud resource (e.g., "OVH::Network::FloatingIP" -> namespace="OVH", resourceType="OVH::Network::FloatingIP").
// The last resource is used because it's typically the main resource being tested,
// while earlier resources are dependencies.
func extractNamespaceFromEvalOutput(evalOutput string) (namespace string, resourceType string, err error) {
	var forma pkgmodel.Forma
	if err := json.Unmarshal([]byte(evalOutput), &forma); err != nil {
		return "", "", err
	}

	// Find the last cloud resource (those with "::" in the type)
	for i := len(forma.Resources) - 1; i >= 0; i-- {
		res := forma.Resources[i]
		if strings.Contains(res.Type, "::") {
			parts := strings.Split(res.Type, "::")
			if len(parts) > 0 {
				return parts[0], res.Type, nil
			}
		}
	}

	return "", "", fmt.Errorf("no cloud resource found in eval output")
}
