// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"os"
	"testing"
)

func TestFilterTestCases(t *testing.T) {
	testCases := []TestCase{
		{
			Name:         "AWS::s3-bucket",
			PKLFile:      "/path/to/testdata/s3-bucket.pkl",
			ResourceType: "s3-bucket",
			PluginName:   "aws",
		},
		{
			Name:         "AWS::ec2-instance",
			PKLFile:      "/path/to/testdata/compute/ec2-instance.pkl",
			ResourceType: "ec2-instance",
			PluginName:   "aws",
		},
		{
			Name:         "AWS::vpc",
			PKLFile:      "/path/to/testdata/network/vpc.pkl",
			ResourceType: "vpc",
			PluginName:   "aws",
		},
		{
			Name:         "GCP::storage-bucket",
			PKLFile:      "/path/to/testdata/storage-bucket.pkl",
			ResourceType: "storage-bucket",
			PluginName:   "gcp",
		},
	}

	tests := []struct {
		name          string
		filter        string
		expectedNames []string
	}{
		{
			name:          "no filter returns all",
			filter:        "",
			expectedNames: []string{"AWS::s3-bucket", "AWS::ec2-instance", "AWS::vpc", "GCP::storage-bucket"},
		},
		{
			name:          "filter by resource type",
			filter:        "s3-bucket",
			expectedNames: []string{"AWS::s3-bucket"},
		},
		{
			name:          "filter by resource type case insensitive",
			filter:        "S3-BUCKET",
			expectedNames: []string{"AWS::s3-bucket"},
		},
		{
			name:          "filter by partial name",
			filter:        "AWS",
			expectedNames: []string{"AWS::s3-bucket", "AWS::ec2-instance", "AWS::vpc"},
		},
		{
			name:          "filter by path segment",
			filter:        "network",
			expectedNames: []string{"AWS::vpc"},
		},
		{
			name:          "filter by compute path",
			filter:        "compute",
			expectedNames: []string{"AWS::ec2-instance"},
		},
		{
			name:          "multiple filters comma separated",
			filter:        "s3-bucket,vpc",
			expectedNames: []string{"AWS::s3-bucket", "AWS::vpc"},
		},
		{
			name:          "multiple filters with spaces",
			filter:        "s3-bucket , vpc , ec2-instance",
			expectedNames: []string{"AWS::s3-bucket", "AWS::ec2-instance", "AWS::vpc"},
		},
		{
			name:          "filter matches multiple via partial",
			filter:        "bucket",
			expectedNames: []string{"AWS::s3-bucket", "GCP::storage-bucket"},
		},
		{
			name:          "filter by plugin name in path",
			filter:        "GCP",
			expectedNames: []string{"GCP::storage-bucket"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variable
			if tt.filter != "" {
				os.Setenv("FORMAE_TEST_FILTER", tt.filter)
				defer os.Unsetenv("FORMAE_TEST_FILTER")
			} else {
				os.Unsetenv("FORMAE_TEST_FILTER")
			}

			// Run filter
			filtered := filterTestCases(t, testCases)

			// Check count
			if len(filtered) != len(tt.expectedNames) {
				t.Errorf("expected %d test cases, got %d", len(tt.expectedNames), len(filtered))
				t.Logf("expected: %v", tt.expectedNames)
				t.Logf("got: %v", testCaseNames(filtered))
				return
			}

			// Check names match (order may differ for multi-filter cases)
			gotNames := make(map[string]bool)
			for _, tc := range filtered {
				gotNames[tc.Name] = true
			}
			for _, expected := range tt.expectedNames {
				if !gotNames[expected] {
					t.Errorf("expected test case %q not found in filtered results", expected)
				}
			}
		})
	}
}

// TestFilterTestCases_ExactMatchPriority verifies that when a filter pattern
// exactly matches a ResourceType, only that exact match is returned — not
// substring matches. This prevents CI cross-contamination where e.g.
// "s3-bucket" also matches "s3-bucketpolicy".
func TestFilterTestCases_ExactMatchPriority(t *testing.T) {
	testCases := []TestCase{
		{
			Name:         "AWS::s3-bucket",
			PKLFile:      "/path/to/testdata/s3-bucket.pkl",
			ResourceType: "s3-bucket",
			PluginName:   "aws",
		},
		{
			Name:         "AWS::s3-bucketpolicy",
			PKLFile:      "/path/to/testdata/s3-bucketpolicy.pkl",
			ResourceType: "s3-bucketpolicy",
			PluginName:   "aws",
		},
		{
			Name:         "AWS::ec2-transitgateway",
			PKLFile:      "/path/to/testdata/ec2-transitgateway.pkl",
			ResourceType: "ec2-transitgateway",
			PluginName:   "aws",
		},
		{
			Name:         "AWS::ec2-transitgatewayroutetable",
			PKLFile:      "/path/to/testdata/ec2-transitgatewayroutetable.pkl",
			ResourceType: "ec2-transitgatewayroutetable",
			PluginName:   "aws",
		},
		{
			Name:         "AWS::iam-role",
			PKLFile:      "/path/to/testdata/iam-role.pkl",
			ResourceType: "iam-role",
			PluginName:   "aws",
		},
		{
			Name:         "AWS::iam-rolepolicy",
			PKLFile:      "/path/to/testdata/iam-rolepolicy.pkl",
			ResourceType: "iam-rolepolicy",
			PluginName:   "aws",
		},
	}

	tests := []struct {
		name          string
		filter        string
		expectedNames []string
	}{
		{
			name:          "exact match s3-bucket does not match s3-bucketpolicy",
			filter:        "s3-bucket",
			expectedNames: []string{"AWS::s3-bucket"},
		},
		{
			name:          "exact match ec2-transitgateway does not match ec2-transitgatewayroutetable",
			filter:        "ec2-transitgateway",
			expectedNames: []string{"AWS::ec2-transitgateway"},
		},
		{
			name:          "exact match iam-role does not match iam-rolepolicy",
			filter:        "iam-role",
			expectedNames: []string{"AWS::iam-role"},
		},
		{
			name:          "exact match s3-bucketpolicy returns only s3-bucketpolicy",
			filter:        "s3-bucketpolicy",
			expectedNames: []string{"AWS::s3-bucketpolicy"},
		},
		{
			name:          "non-exact filter still uses substring matching",
			filter:        "transitgateway",
			expectedNames: []string{"AWS::ec2-transitgateway", "AWS::ec2-transitgatewayroutetable"},
		},
		{
			name:          "multiple exact filters",
			filter:        "s3-bucket,iam-role",
			expectedNames: []string{"AWS::s3-bucket", "AWS::iam-role"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variable
			if tt.filter != "" {
				os.Setenv("FORMAE_TEST_FILTER", tt.filter)
				defer os.Unsetenv("FORMAE_TEST_FILTER")
			} else {
				os.Unsetenv("FORMAE_TEST_FILTER")
			}

			// Run filter
			filtered := filterTestCases(t, testCases)

			// Check count
			if len(filtered) != len(tt.expectedNames) {
				t.Errorf("expected %d test cases, got %d", len(tt.expectedNames), len(filtered))
				t.Logf("expected: %v", tt.expectedNames)
				t.Logf("got: %v", testCaseNames(filtered))
				return
			}

			// Check names match (order may differ for multi-filter cases)
			gotNames := make(map[string]bool)
			for _, tc := range filtered {
				gotNames[tc.Name] = true
			}
			for _, expected := range tt.expectedNames {
				if !gotNames[expected] {
					t.Errorf("expected test case %q not found in filtered results", expected)
				}
			}
		})
	}
}

func TestFilterTestCases_NoMatch(t *testing.T) {
	testCases := []TestCase{
		{
			Name:         "AWS::s3-bucket",
			PKLFile:      "/path/to/testdata/s3-bucket.pkl",
			ResourceType: "s3-bucket",
			PluginName:   "aws",
		},
	}

	os.Setenv("FORMAE_TEST_FILTER", "nonexistent")
	defer os.Unsetenv("FORMAE_TEST_FILTER")

	// Create a sub-test to capture the fatal
	subT := &testing.T{}

	// This should call t.Fatalf, but since we're using a mock T, we can't easily test this
	// In real usage, this would fail the test with a helpful message
	// For now, we just document the expected behavior
	t.Log("Note: filterTestCases calls t.Fatalf when no matches found - cannot easily unit test fatal behavior")

	// We can at least verify the helper function works
	names := testCaseNames(testCases)
	if len(names) != 1 || names[0] != "AWS::s3-bucket" {
		t.Errorf("testCaseNames returned unexpected result: %v", names)
	}

	_ = subT // silence unused variable warning
}

func TestTestCaseNames(t *testing.T) {
	testCases := []TestCase{
		{Name: "test1"},
		{Name: "test2"},
		{Name: "test3"},
	}

	names := testCaseNames(testCases)

	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}

	expected := []string{"test1", "test2", "test3"}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("expected name[%d] = %q, got %q", i, expected[i], name)
		}
	}
}

func TestTestCaseNames_Empty(t *testing.T) {
	names := testCaseNames([]TestCase{})
	if len(names) != 0 {
		t.Errorf("expected empty slice, got %v", names)
	}
}

// TestCompareProperties_NestedResolvable verifies that compareProperties handles
// Resolvable references nested inside SubResource maps. This reproduces the
// elasticbeanstalk failure where ResourceLifecycleConfig.ServiceRole is a
// Resolvable: Pkl eval produces $visibility:Clear, but after Formae resolves
// the reference the inventory has $value with the actual ARN.
func TestCompareProperties_NestedResolvable(t *testing.T) {
	// Expected: from Pkl eval — Resolvable has $visibility but no $value
	expectedProperties := map[string]any{
		"ApplicationName": "my-app",
		"ResourceLifecycleConfig": map[string]any{
			"ServiceRole": map[string]any{
				"$label":      "eb-service-role",
				"$property":   "Arn",
				"$res":        true,
				"$stack":      "my-stack",
				"$type":       "AWS::IAM::Role",
				"$visibility": "Clear",
			},
			"VersionLifecycleConfig": map[string]any{
				"MaxAgeRule": map[string]any{
					"DeleteSourceFromS3": false,
					"Enabled":            false,
					"MaxAgeInDays":       float64(180),
				},
				"MaxCountRule": map[string]any{
					"DeleteSourceFromS3": false,
					"Enabled":            false,
					"MaxCount":           float64(200),
				},
			},
		},
	}

	// Actual: from inventory after resolution — Resolvable has $value instead of $visibility
	actualResource := map[string]any{
		"Properties": map[string]any{
			"ApplicationName": "my-app",
			"ResourceLifecycleConfig": map[string]any{
				"ServiceRole": map[string]any{
					"$label":    "eb-service-role",
					"$property": "Arn",
					"$res":      true,
					"$stack":    "my-stack",
					"$type":     "AWS::IAM::Role",
					"$value":    "arn:aws:iam::123456789012:role/my-role",
				},
				"VersionLifecycleConfig": map[string]any{
					"MaxAgeRule": map[string]any{
						"DeleteSourceFromS3": false,
						"Enabled":            false,
						"MaxAgeInDays":       float64(180),
					},
					"MaxCountRule": map[string]any{
						"DeleteSourceFromS3": false,
						"Enabled":            false,
						"MaxCount":           float64(200),
					},
				},
			},
		},
	}

	result := compareProperties(t, expectedProperties, actualResource, "after create")
	if !result {
		t.Errorf("compareProperties should pass when SubResource contains a nested Resolvable with resolved $value")
	}
}

func TestCompareMap(t *testing.T) {
	t.Run("nested resolvable passes", func(t *testing.T) {
		expected := map[string]any{
			"ServiceRole": map[string]any{
				"$label":      "eb-service-role",
				"$property":   "Arn",
				"$res":        true,
				"$stack":      "my-stack",
				"$type":       "AWS::IAM::Role",
				"$visibility": "Clear",
			},
		}
		actual := map[string]any{
			"ServiceRole": map[string]any{
				"$label":    "eb-service-role",
				"$property": "Arn",
				"$res":      true,
				"$stack":    "my-stack",
				"$type":     "AWS::IAM::Role",
				"$value":    "arn:aws:iam::123456789012:role/my-role",
			},
		}
		if !compareMap(t, "Config", expected, actual, "test") {
			t.Error("compareMap should pass for nested resolvable")
		}
	})

	t.Run("scalar mismatch fails", func(t *testing.T) {
		fakeT := &testing.T{}
		expected := map[string]any{
			"Name": "expected-value",
		}
		actual := map[string]any{
			"Name": "different-value",
		}
		if compareMap(fakeT, "Config", expected, actual, "test") {
			t.Error("compareMap should fail for scalar mismatch")
		}
	})

	t.Run("extra keys in actual are ignored", func(t *testing.T) {
		expected := map[string]any{
			"Name": "my-app",
		}
		actual := map[string]any{
			"Name":    "my-app",
			"ExtraID": "extra-value",
		}
		if !compareMap(t, "Config", expected, actual, "test") {
			t.Error("compareMap should pass when actual has extra keys not in expected")
		}
	})

	t.Run("deeply nested resolvable passes", func(t *testing.T) {
		expected := map[string]any{
			"Outer": map[string]any{
				"Inner": map[string]any{
					"Ref": map[string]any{
						"$label":      "my-ref",
						"$property":   "Id",
						"$res":        true,
						"$stack":      "stack",
						"$type":       "AWS::EC2::VPC",
						"$visibility": "Clear",
					},
				},
			},
		}
		actual := map[string]any{
			"Outer": map[string]any{
				"Inner": map[string]any{
					"Ref": map[string]any{
						"$label":    "my-ref",
						"$property": "Id",
						"$res":      true,
						"$stack":    "stack",
						"$type":     "AWS::EC2::VPC",
						"$value":    "vpc-12345",
					},
				},
			},
		}
		if !compareMap(t, "Config", expected, actual, "test") {
			t.Error("compareMap should pass for deeply nested resolvable")
		}
	})

	t.Run("missing key in actual fails", func(t *testing.T) {
		fakeT := &testing.T{}
		expected := map[string]any{
			"Name":    "my-app",
			"Missing": "value",
		}
		actual := map[string]any{
			"Name": "my-app",
		}
		if compareMap(fakeT, "Config", expected, actual, "test") {
			t.Error("compareMap should fail when expected key is missing from actual")
		}
	})
}

// TestCompareProperties_ResolvableNestedInArray verifies that compareProperties
// handles resolvable refs nested inside array elements. The array element itself
// is NOT a resolvable — it's a map that contains a resolvable field.
// Currently compareArrayUnordered only checks if the element itself is resolvable
// and falls through to JSON string comparison, which fails because expected has
// $visibility while actual has $value.
func TestCompareProperties_ResolvableNestedInArray(t *testing.T) {
	expectedProperties := map[string]any{
		"items": []any{
			map[string]any{
				"name": "default",
				"ref": map[string]any{
					"$label":      "my-ref",
					"$property":   "name",
					"$res":        true,
					"$stack":      "my-stack",
					"$type":       "TEST::Core::Namespace",
					"$visibility": "Clear",
				},
			},
		},
	}

	actualResource := map[string]any{
		"Properties": map[string]any{
			"items": []any{
				map[string]any{
					"name": "default",
					"ref": map[string]any{
						"$label":    "my-ref",
						"$property": "name",
						"$res":      true,
						"$stack":    "my-stack",
						"$type":     "TEST::Core::Namespace",
						"$value":    "resolved-value",
					},
				},
			},
		},
	}

	result := compareProperties(t, expectedProperties, actualResource, "after create")
	if !result {
		t.Errorf("compareProperties should pass when an array element contains a nested resolvable with resolved $value")
	}
}

func TestGetTestType(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected TestType
	}{
		{
			name:     "empty returns all",
			envValue: "",
			expected: TestTypeAll,
		},
		{
			name:     "crud returns crud",
			envValue: "crud",
			expected: TestTypeCRUD,
		},
		{
			name:     "CRUD uppercase returns crud",
			envValue: "CRUD",
			expected: TestTypeCRUD,
		},
		{
			name:     "discovery returns discovery",
			envValue: "discovery",
			expected: TestTypeDiscovery,
		},
		{
			name:     "DISCOVERY uppercase returns discovery",
			envValue: "DISCOVERY",
			expected: TestTypeDiscovery,
		},
		{
			name:     "all returns all",
			envValue: "all",
			expected: TestTypeAll,
		},
		{
			name:     "ALL uppercase returns all",
			envValue: "ALL",
			expected: TestTypeAll,
		},
		{
			name:     "invalid value returns all",
			envValue: "invalid",
			expected: TestTypeAll,
		},
		{
			name:     "mixed case returns correct type",
			envValue: "CrUd",
			expected: TestTypeCRUD,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv("FORMAE_TEST_TYPE", tt.envValue)
				defer os.Unsetenv("FORMAE_TEST_TYPE")
			} else {
				os.Unsetenv("FORMAE_TEST_TYPE")
			}

			result := getTestType()
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
