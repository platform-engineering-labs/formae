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

// TestFilterTestCases_Regex verifies /…/ delimited regex matching
// against test case name and resource type (RFC-0022).
func TestFilterTestCases_Regex(t *testing.T) {
	testCases := []TestCase{
		{Name: "AWS::s3-bucket", ResourceType: "s3-bucket", PKLFile: "/path/s3-bucket.pkl", PluginName: "aws"},
		{Name: "AWS::ec2-instance", ResourceType: "ec2-instance", PKLFile: "/path/ec2-instance.pkl", PluginName: "aws"},
		{Name: "AWS::vpc", ResourceType: "vpc", PKLFile: "/path/vpc.pkl", PluginName: "aws"},
		{Name: "GCP::storage-bucket", ResourceType: "storage-bucket", PKLFile: "/path/storage-bucket.pkl", PluginName: "gcp"},
		{Name: "K8S::nginx-chart", ResourceType: "nginx-chart", PKLFile: "/path/nginx-chart.pkl", PluginName: "k8s"},
		{Name: "K8S::redis-chart", ResourceType: "redis-chart", PKLFile: "/path/redis-chart.pkl", PluginName: "k8s"},
		{Name: "K8S::clusterrole", ResourceType: "clusterrole", PKLFile: "/path/clusterrole.pkl", PluginName: "k8s"},
		{Name: "K8S::clusterrolebinding", ResourceType: "clusterrolebinding", PKLFile: "/path/clusterrolebinding.pkl", PluginName: "k8s"},
	}

	tests := []struct {
		name          string
		filter        string
		expectedNames []string
	}{
		{
			name:          "regex matching chart tests",
			filter:        "/.*-chart/",
			expectedNames: []string{"K8S::nginx-chart", "K8S::redis-chart"},
		},
		{
			name:          "anchored regex matching cluster-prefixed resources",
			filter:        "/^cluster.*/",
			expectedNames: []string{"K8S::clusterrole", "K8S::clusterrolebinding"},
		},
		{
			name:          "regex matching by name prefix",
			filter:        "/^AWS::.*/",
			expectedNames: []string{"AWS::s3-bucket", "AWS::ec2-instance", "AWS::vpc"},
		},
		{
			name:          "mixed regex and literal",
			filter:        "/.*-chart/,vpc",
			expectedNames: []string{"K8S::nginx-chart", "K8S::redis-chart", "AWS::vpc"},
		},
		{
			name:          "empty regex matches all",
			filter:        "//",
			expectedNames: []string{"AWS::s3-bucket", "AWS::ec2-instance", "AWS::vpc", "GCP::storage-bucket", "K8S::nginx-chart", "K8S::redis-chart", "K8S::clusterrole", "K8S::clusterrolebinding"},
		},
		{
			name:          "regex with alternation",
			filter:        "/s3-bucket|vpc/",
			expectedNames: []string{"AWS::s3-bucket", "AWS::vpc"},
		},
		{
			name:          "regex matches resource type only",
			filter:        "/^ec2-instance$/",
			expectedNames: []string{"AWS::ec2-instance"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("FORMAE_TEST_FILTER", tt.filter)
			defer os.Unsetenv("FORMAE_TEST_FILTER")

			filtered := filterTestCases(t, testCases)

			if len(filtered) != len(tt.expectedNames) {
				t.Errorf("expected %d test cases, got %d", len(tt.expectedNames), len(filtered))
				t.Logf("expected: %v", tt.expectedNames)
				t.Logf("got: %v", testCaseNames(filtered))
				return
			}

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

// TestFilterTestCases_InvalidRegex verifies that an invalid regex
// pattern causes the test run to fail immediately.
func TestFilterTestCases_InvalidRegex(t *testing.T) {
	testCases := []TestCase{
		{Name: "AWS::s3-bucket", ResourceType: "s3-bucket", PKLFile: "/path/s3-bucket.pkl", PluginName: "aws"},
	}

	os.Setenv("FORMAE_TEST_FILTER", "/[invalid/")
	defer os.Unsetenv("FORMAE_TEST_FILTER")

	// filterTestCases calls t.Fatalf on invalid regex — we can't easily
	// capture that in a unit test without a subprocess, so we document
	// the expected behavior here and test the regex compilation path
	// indirectly through the passing tests above.
	t.Log("Note: filterTestCases calls t.Fatalf on invalid regex /[invalid/ — cannot unit test fatal behavior directly")
	_ = testCases
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

	result := compareProperties(t, expectedProperties, actualResource, "after create", map[string]providerDefault{})
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
		if !compareMap(t, "Config", expected, actual, "test", map[string]providerDefault{}) {
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
		if compareMap(fakeT, "Config", expected, actual, "test", map[string]providerDefault{}) {
			t.Error("compareMap should fail for scalar mismatch")
		}
	})

	t.Run("extra keys in actual flagged when not provider default", func(t *testing.T) {
		fakeT := &testing.T{}
		expected := map[string]any{
			"Name": "my-app",
		}
		actual := map[string]any{
			"Name":    "my-app",
			"ExtraID": "extra-value",
		}
		if compareMap(fakeT, "Config", expected, actual, "test", map[string]providerDefault{}) {
			t.Error("compareMap should fail when actual has extra keys not marked as provider default")
		}
	})

	t.Run("extra keys in actual allowed when provider default", func(t *testing.T) {
		expected := map[string]any{
			"Name": "my-app",
		}
		actual := map[string]any{
			"Name":    "my-app",
			"ExtraID": "extra-value",
		}
		providerDefaults := map[string]providerDefault{
			"Config.ExtraID": {},
		}
		if !compareMap(t, "Config", expected, actual, "test", providerDefaults) {
			t.Error("compareMap should pass when extra key is a provider default")
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
		if !compareMap(t, "Config", expected, actual, "test", map[string]providerDefault{}) {
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
		if compareMap(fakeT, "Config", expected, actual, "test", map[string]providerDefault{}) {
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

	result := compareProperties(t, expectedProperties, actualResource, "after create", map[string]providerDefault{})
	if !result {
		t.Errorf("compareProperties should pass when an array element contains a nested resolvable with resolved $value")
	}
}

func TestCompareArrayUnordered_ProviderDefaultsAllowed(t *testing.T) {
	// Mimics K8S service ports: expected has {name, port}, actual has {name, port, protocol, targetPort}
	// protocol and targetPort are hasProviderDefault — should be allowed
	expected := []any{
		map[string]any{"name": "http", "port": float64(80)},
	}
	actual := []any{
		map[string]any{"name": "http", "port": float64(80), "protocol": "TCP", "targetPort": float64(80)},
	}
	providerDefaults := map[string]providerDefault{
		"spec.ports.protocol":   {},
		"spec.ports.targetPort": {},
	}
	result := compareArrayUnordered(t, "spec.ports", expected, actual, "after create", providerDefaults)
	if !result {
		t.Error("should pass when extra keys are provider defaults")
	}
}

func TestCompareArrayUnordered_NonProviderDefaultFlagged(t *testing.T) {
	// Extra key "bogus" is NOT a provider default — should fail
	expected := []any{
		map[string]any{"name": "http", "port": float64(80)},
	}
	actual := []any{
		map[string]any{"name": "http", "port": float64(80), "bogus": "bad"},
	}
	providerDefaults := map[string]providerDefault{
		"spec.ports.protocol": {},
	}
	// Use a sub-test so the failure doesn't abort the parent
	inner := &testing.T{}
	result := compareArrayUnordered(inner, "spec.ports", expected, actual, "after create", providerDefaults)
	if result {
		t.Error("should fail when extra key is not a provider default")
	}
}

func TestCompareArrayUnordered_MultipleElementsDifferentOrder(t *testing.T) {
	// Two ports, both with provider-default extra keys, different order
	expected := []any{
		map[string]any{"name": "https", "port": float64(443)},
		map[string]any{"name": "http", "port": float64(80)},
	}
	actual := []any{
		map[string]any{"name": "http", "port": float64(80), "protocol": "TCP", "targetPort": float64(80)},
		map[string]any{"name": "https", "port": float64(443), "protocol": "TCP", "targetPort": float64(443)},
	}
	providerDefaults := map[string]providerDefault{
		"spec.ports.protocol":   {},
		"spec.ports.targetPort": {},
	}
	result := compareArrayUnordered(t, "spec.ports", expected, actual, "after create", providerDefaults)
	if !result {
		t.Error("should pass with multiple elements in different order when extra keys are provider defaults")
	}
}

func TestCompareArrayUnordered_NestedProviderDefaults(t *testing.T) {
	// Mimics K8S webhooks: expected has {name, clientConfig}, actual has extra nested fields
	expected := []any{
		map[string]any{
			"name": "my-webhook",
			"clientConfig": map[string]any{
				"service": map[string]any{"name": "webhook-svc", "namespace": "default"},
			},
		},
	}
	actual := []any{
		map[string]any{
			"name": "my-webhook",
			"clientConfig": map[string]any{
				"service": map[string]any{"name": "webhook-svc", "namespace": "default", "port": float64(443)},
			},
			"matchPolicy":    "Equivalent",
			"timeoutSeconds": float64(10),
			"failurePolicy":  "Fail",
			"sideEffects":    "None",
		},
	}
	providerDefaults := map[string]providerDefault{
		"webhooks.matchPolicy":          {},
		"webhooks.timeoutSeconds":       {},
		"webhooks.failurePolicy":        {},
		"webhooks.sideEffects":          {},
		"webhooks.clientConfig.service.port": {},
	}
	result := compareArrayUnordered(t, "webhooks", expected, actual, "after create", providerDefaults)
	if !result {
		t.Error("should pass for nested maps when extra keys are provider defaults")
	}
}

func TestCompareProperties_ProviderDefaultsAllowed(t *testing.T) {
	// Full-path test: extra fields in array map elements allowed when hasProviderDefault
	expectedProperties := map[string]any{
		"metadata": map[string]any{"name": "my-service"},
		"spec": map[string]any{
			"ports": []any{
				map[string]any{"name": "http", "port": float64(80)},
			},
		},
	}
	actualResource := map[string]any{
		"Properties": map[string]any{
			"metadata": map[string]any{"name": "my-service"},
			"spec": map[string]any{
				"ports": []any{
					map[string]any{"name": "http", "port": float64(80), "protocol": "TCP", "targetPort": float64(80)},
				},
			},
		},
	}
	providerDefaults := map[string]providerDefault{
		"spec.ports.protocol":   {},
		"spec.ports.targetPort": {},
	}
	result := compareProperties(t, expectedProperties, actualResource, "after create", providerDefaults)
	if !result {
		t.Error("should pass when extra array element keys are provider defaults")
	}
}

func TestCompareProperties_ExtraTopLevelProviderDefault(t *testing.T) {
	// Actual has an extra top-level key that is a provider default
	expectedProperties := map[string]any{
		"metadata": map[string]any{"name": "my-svc"},
	}
	actualResource := map[string]any{
		"Properties": map[string]any{
			"metadata":      map[string]any{"name": "my-svc"},
			"clusterIP":     "10.96.0.1",
		},
	}
	providerDefaults := map[string]providerDefault{
		"clusterIP": {},
	}
	result := compareProperties(t, expectedProperties, actualResource, "after create", providerDefaults)
	if !result {
		t.Error("should pass when extra top-level key is a provider default")
	}
}

func TestCompareProperties_ExtraTopLevelNonProviderDefault(t *testing.T) {
	// Actual has an extra top-level key that is NOT a provider default — should fail
	expectedProperties := map[string]any{
		"metadata": map[string]any{"name": "my-svc"},
	}
	actualResource := map[string]any{
		"Properties": map[string]any{
			"metadata": map[string]any{"name": "my-svc"},
			"bogus":    "unexpected",
		},
	}
	providerDefaults := map[string]providerDefault{}
	inner := &testing.T{}
	result := compareProperties(inner, expectedProperties, actualResource, "after create", providerDefaults)
	if result {
		t.Error("should fail when extra top-level key is not a provider default")
	}
}

func TestCompareMap_ExtraNestedProviderDefault(t *testing.T) {
	// Extra nested key in a map that is a provider default
	expected := map[string]any{"name": "my-svc"}
	actual := map[string]any{"name": "my-svc", "sessionAffinity": "None"}
	providerDefaults := map[string]providerDefault{
		"spec.sessionAffinity": {},
	}
	result := compareMap(t, "spec", expected, actual, "after create", providerDefaults)
	if !result {
		t.Error("should pass when extra nested key is a provider default")
	}
}

func TestCompareMap_ExtraNestedNonProviderDefault(t *testing.T) {
	// Extra nested key that is NOT a provider default — should fail
	expected := map[string]any{"name": "my-svc"}
	actual := map[string]any{"name": "my-svc", "unknown": "bad"}
	providerDefaults := map[string]providerDefault{}
	inner := &testing.T{}
	result := compareMap(inner, "spec", expected, actual, "after create", providerDefaults)
	if result {
		t.Error("should fail when extra nested key is not a provider default")
	}
}

func TestIsProviderDefault(t *testing.T) {
	t.Run("scalar provider defaults", func(t *testing.T) {
		providerDefaults := map[string]providerDefault{
			"spec.ports.protocol":  {},
			"spec.sessionAffinity": {},
			"webhooks.matchPolicy": {},
		}

		tests := []struct {
			path     string
			expected bool
		}{
			{"spec.ports.protocol", true},
			{"spec.ports[0].protocol", true},
			{"spec.ports[12].protocol", true},
			{"spec.sessionAffinity", true},
			{"webhooks[0].matchPolicy", true},
			{"spec.ports.bogus", false},
			{"spec.unknown", false},
			{"bogus", false},
		}
		for _, tc := range tests {
			got := isProviderDefault(tc.path, providerDefaults)
			if got != tc.expected {
				t.Errorf("isProviderDefault(%q) = %v, want %v", tc.path, got, tc.expected)
			}
		}
	})

	t.Run("collection provider defaults - broad tolerance", func(t *testing.T) {
		providerDefaults := map[string]providerDefault{
			"metadata.labels":      {IsCollection: true},
			"spec.selector":        {IsCollection: true},
			"spec.revisionHistory": {}, // scalar, not collection
		}

		tests := []struct {
			path     string
			expected bool
		}{
			// Collection: any child key tolerated
			{"metadata.labels", true},
			{"metadata.labels.app", true},
			{"metadata.labels.env", true},
			{"metadata.labels.app.kubernetes.io/name", true},
			{"spec.selector.app", true},
			{"spec.selector.matchLabels", true},
			// Scalar: only exact match
			{"spec.revisionHistory", true},
			{"spec.revisionHistory.limit", false},
			// Non-defaults: still flagged
			{"metadata.annotations", false},
			{"spec.unknown", false},
		}
		for _, tc := range tests {
			got := isProviderDefault(tc.path, providerDefaults)
			if got != tc.expected {
				t.Errorf("isProviderDefault(%q) = %v, want %v", tc.path, got, tc.expected)
			}
		}
	})

	t.Run("collection provider defaults - simple key patterns", func(t *testing.T) {
		providerDefaults := map[string]providerDefault{
			"metadata.labels": {
				IsCollection: true,
				KeyPatterns:  []string{"app", "helm"},
			},
		}

		tests := []struct {
			path     string
			expected bool
		}{
			{"metadata.labels.app", true},
			{"metadata.labels.helm", true},
			{"metadata.labels.app.example.com/name", true}, // first segment "app" matches
			{"metadata.labels.env", false},
			{"metadata.labels.random", false},
			{"metadata.labels", true},
		}
		for _, tc := range tests {
			got := isProviderDefault(tc.path, providerDefaults)
			if got != tc.expected {
				t.Errorf("isProviderDefault(%q) = %v, want %v", tc.path, got, tc.expected)
			}
		}
	})

	t.Run("collection provider defaults - dotted key patterns", func(t *testing.T) {
		providerDefaults := map[string]providerDefault{
			"metadata.labels": {
				IsCollection: true,
				KeyPatterns:  []string{"provider.example.com/*", "mgmt.example.com/*"},
			},
		}

		tests := []struct {
			path     string
			expected bool
		}{
			{"metadata.labels.provider.example.com/name", true},
			{"metadata.labels.provider.example.com/instance", true},
			{"metadata.labels.mgmt.example.com/chart", true},
			{"metadata.labels.other.example.com/job-name", false},
			{"metadata.labels.env", false},
		}
		for _, tc := range tests {
			got := isProviderDefault(tc.path, providerDefaults)
			if got != tc.expected {
				t.Errorf("isProviderDefault(%q) = %v, want %v", tc.path, got, tc.expected)
			}
		}
	})

	t.Run("collection provider defaults with array indices", func(t *testing.T) {
		providerDefaults := map[string]providerDefault{
			"spec.template.metadata.labels": {IsCollection: true},
		}

		tests := []struct {
			path     string
			expected bool
		}{
			{"spec.template.metadata.labels.app", true},
			{"spec.template.metadata.labels", true},
		}
		for _, tc := range tests {
			got := isProviderDefault(tc.path, providerDefaults)
			if got != tc.expected {
				t.Errorf("isProviderDefault(%q) = %v, want %v", tc.path, got, tc.expected)
			}
		}
	})
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
