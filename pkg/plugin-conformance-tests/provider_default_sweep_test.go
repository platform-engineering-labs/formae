// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestObserveState(t *testing.T) {
	props := map[string]any{
		"present":         "us-west-2",
		"explicitNull":    nil,
		"emptyMap":        map[string]any{},
		"emptyList":       []any{},
		"emptyString":     "",
		"zero":            float64(0),
		"false":           false,
		"nested":          map[string]any{"leaf": "v", "blank": ""},
		"list":            []any{map[string]any{"leaf": "a"}, map[string]any{"leaf": "b"}},
		"listAllAbsent":   []any{map[string]any{"other": "a"}},
		"listOneAbsent":   []any{map[string]any{"other": "a"}, map[string]any{"leaf": "b"}},
		"listEmptyLeaves": []any{map[string]any{"leaf": ""}},
	}

	cases := []struct {
		path string
		want string
	}{
		{"present", obsValue},
		{"missing", obsAbsent},
		{"explicitNull", obsNull},
		{"emptyMap", obsEmptyCollection},
		{"emptyList", obsEmptyCollection},
		{"emptyString", obsEmptyString},
		{"zero", obsValue},
		{"false", obsValue},
		{"nested.leaf", obsValue},
		{"nested.blank", obsEmptyString},
		{"nested.missing", obsAbsent},
		{"missing.leaf", obsAbsent},
		// A path crossing a list aggregates over the elements: the strongest
		// observation wins, because the question is whether the provider put
		// anything there at all.
		{"list.leaf", obsValue},
		{"listAllAbsent.leaf", obsAbsent},
		{"listOneAbsent.leaf", obsValue},
		{"listEmptyLeaves.leaf", obsEmptyString},
	}

	for _, tc := range cases {
		if got := observeState(props, tc.path); got != tc.want {
			t.Errorf("observeState(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestSweepRecordsOmittedFieldPopulatedByProvider(t *testing.T) {
	sweep := newSweep()

	declared := map[string]any{"bucketName": "b"}
	afterCreate := map[string]any{"bucketName": "b", "encryption": "AES256"}
	afterSync := map[string]any{"bucketName": "b", "encryption": "AES256"}
	hints := map[string]any{
		"encryption": map[string]any{"HasProviderDefault": true},
		"bucketName": map[string]any{"HasProviderDefault": true, "CreateOnly": true},
		"unhinted":   map[string]any{"HasProviderDefault": false},
	}

	sweep.record("s3-bucket", "AWS::S3::Bucket", hints, declared, afterCreate, afterSync)

	got := sweep.observations()
	want := []ProviderDefaultObservation{
		{
			TestCase:     "s3-bucket",
			ResourceType: "AWS::S3::Bucket",
			Path:         "bucketName",
			Declared:     true,
			CreateEcho:   obsValue,
			AfterSync:    obsValue,
			CreateOnly:   true,
		},
		{
			TestCase:     "s3-bucket",
			ResourceType: "AWS::S3::Bucket",
			Path:         "encryption",
			Declared:     false,
			CreateEcho:   obsValue,
			AfterSync:    obsValue,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("observations mismatch:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestSweepFlagsAValueThatMovedBetweenCreateAndSync(t *testing.T) {
	sweep := newSweep()
	hints := map[string]any{"version": map[string]any{"HasProviderDefault": true}}

	sweep.record("db", "AWS::RDS::DBInstance", hints,
		map[string]any{},
		map[string]any{"version": "14.1"},
		map[string]any{"version": "14.2"},
	)

	got := sweep.observations()
	if len(got) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(got))
	}
	if !got[0].Moved {
		t.Errorf("expected Moved=true for a value that changed between create and sync, got %+v", got[0])
	}
}

func TestSweepDoesNotFlagMovementWhenTheValueIsStable(t *testing.T) {
	sweep := newSweep()
	hints := map[string]any{"tags": map[string]any{"HasProviderDefault": true}}

	sweep.record("db", "AWS::RDS::DBInstance", hints,
		map[string]any{},
		map[string]any{"tags": map[string]any{"a": "1"}},
		map[string]any{"tags": map[string]any{"a": "1"}},
	)

	if sweep.observations()[0].Moved {
		t.Error("expected Moved=false for an unchanged value")
	}
}

func TestSweepMergesRepeatedObservationsOfTheSameField(t *testing.T) {
	sweep := newSweep()
	hints := map[string]any{"kmsKeyId": map[string]any{"HasProviderDefault": true}}

	// The same field observed by two fixtures: one declares it, one omits it.
	sweep.record("bucket-plain", "AWS::S3::Bucket", hints,
		map[string]any{},
		map[string]any{},
		map[string]any{},
	)
	sweep.record("bucket-encrypted", "AWS::S3::Bucket", hints,
		map[string]any{"kmsKeyId": "arn:key"},
		map[string]any{"kmsKeyId": "arn:key"},
		map[string]any{"kmsKeyId": "arn:key"},
	)

	got := sweep.observations()
	if len(got) != 2 {
		t.Fatalf("expected one row per (resource type, path, test case), got %d: %+v", len(got), got)
	}
	if got[0].TestCase != "bucket-encrypted" || got[1].TestCase != "bucket-plain" {
		t.Errorf("expected rows sorted by test case, got %q then %q", got[0].TestCase, got[1].TestCase)
	}
}

func TestSweepWritesASortedJSONArtifact(t *testing.T) {
	sweep := newSweep()
	sweep.record("b", "AWS::S3::Bucket",
		map[string]any{"zzz": map[string]any{"HasProviderDefault": true}},
		map[string]any{}, map[string]any{}, map[string]any{})
	sweep.record("a", "AWS::RDS::DBInstance",
		map[string]any{"aaa": map[string]any{"HasProviderDefault": true}},
		map[string]any{}, map[string]any{}, map[string]any{})

	path := filepath.Join(t.TempDir(), "observations.json")
	if err := sweep.writeTo(path); err != nil {
		t.Fatalf("writeTo: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading artifact: %v", err)
	}
	var artifact struct {
		Observations []ProviderDefaultObservation `json:"observations"`
	}
	if err := json.Unmarshal(raw, &artifact); err != nil {
		t.Fatalf("parsing artifact: %v", err)
	}
	if len(artifact.Observations) != 2 {
		t.Fatalf("expected 2 observations, got %d", len(artifact.Observations))
	}
	if artifact.Observations[0].ResourceType != "AWS::RDS::DBInstance" {
		t.Errorf("expected rows sorted by resource type, got %q first", artifact.Observations[0].ResourceType)
	}
}

func TestNewSweepFromEnvIsDisabledWithoutAnArtifactPath(t *testing.T) {
	t.Setenv(envProviderDefaultObservations, "")
	if newSweepFromEnv() != nil {
		t.Error("expected no sweep when the artifact path is unset")
	}

	t.Setenv(envProviderDefaultObservations, "/tmp/obs.json")
	if newSweepFromEnv() == nil {
		t.Error("expected a sweep when the artifact path is set")
	}
}

// A nil sweep is the disabled case and every entry point must tolerate it, so
// the caller never has to guard.
func TestNilSweepIsInert(t *testing.T) {
	var sweep *providerDefaultSweep
	sweep.record("c", "T", map[string]any{"f": map[string]any{"HasProviderDefault": true}},
		map[string]any{}, map[string]any{}, map[string]any{})
	if err := sweep.writeTo(filepath.Join(t.TempDir(), "unwritten.json")); err != nil {
		t.Errorf("writeTo on a nil sweep: %v", err)
	}
}

func TestGetSettleWindow(t *testing.T) {
	cases := []struct {
		env  string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"-5", 0},
		{"junk", 0},
		{"30", 30 * time.Second},
		// Clamped: an oversized settle window would stall a ~100-job matrix.
		{"999", settleWindowCap},
	}
	for _, tc := range cases {
		t.Setenv(envSettleWindowSeconds, tc.env)
		if got := getSettleWindow(); got != tc.want {
			t.Errorf("getSettleWindow() with %s=%q = %v, want %v", envSettleWindowSeconds, tc.env, got, tc.want)
		}
	}
}

func TestSchemaHints(t *testing.T) {
	hints := map[string]any{"f": map[string]any{"HasProviderDefault": true}}
	resource := map[string]any{"Schema": map[string]any{"Hints": hints}}
	if got := schemaHints(resource); !reflect.DeepEqual(got, hints) {
		t.Errorf("schemaHints = %v, want %v", got, hints)
	}
	if got := schemaHints(map[string]any{}); got != nil {
		t.Errorf("schemaHints on a resource with no Schema = %v, want nil", got)
	}
	if got := schemaHints(map[string]any{"Schema": map[string]any{}}); got != nil {
		t.Errorf("schemaHints on a Schema with no Hints = %v, want nil", got)
	}
}

func TestPropertiesOf(t *testing.T) {
	props := map[string]any{"a": "1"}
	if got := propertiesOf(map[string]any{"Properties": props}); !reflect.DeepEqual(got, props) {
		t.Errorf("propertiesOf = %v, want %v", got, props)
	}
	if got := propertiesOf(map[string]any{}); got != nil {
		t.Errorf("propertiesOf on a resource with no Properties = %v, want nil", got)
	}
}
