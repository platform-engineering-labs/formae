// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ---------------------------------------------------------------------------
// resourceRow: cell builders
// ---------------------------------------------------------------------------

func TestResourceRow_Cells_Unmanaged(t *testing.T) {
	r := pkgmodel.Resource{
		NativeID: "arn:aws:s3:::logs",
		Stack:    "$unmanaged",
		Type:     "AWS::S3::Bucket",
		Label:    "logs",
	}
	got := resourceRow(r)
	assert.Equal(t, []string{"arn:aws:s3:::logs", "⚠ unmanaged", "AWS::S3::Bucket", "logs"}, got.cells)
}

func TestResourceRow_Cells_ManagedStack(t *testing.T) {
	r := pkgmodel.Resource{
		NativeID: "arn:aws:s3:::mybucket",
		Stack:    "production",
		Type:     "AWS::S3::Bucket",
		Label:    "mybucket",
	}
	got := resourceRow(r)
	assert.Equal(t, []string{"arn:aws:s3:::mybucket", "production", "AWS::S3::Bucket", "mybucket"}, got.cells)
}

// ---------------------------------------------------------------------------
// resourceRow: detail renderer — identity lines
// ---------------------------------------------------------------------------

func TestResourceRow_Detail_IdentityLines(t *testing.T) {
	r := pkgmodel.Resource{
		NativeID:   "arn:aws:s3:::logs",
		Stack:      "default",
		Type:       "AWS::S3::Bucket",
		Label:      "logs",
		Target:     "aws-prod",
		Managed:    true,
		Properties: json.RawMessage(`{}`),
	}
	got := resourceRow(r)
	require.NotNil(t, got.detail)
	lines := got.detail(80)

	expected := []string{
		"Label:    logs",
		"Type:     AWS::S3::Bucket",
		"Stack:    default",
		"Target:   aws-prod",
		"NativeID: arn:aws:s3:::logs",
		"Managed:  yes",
	}
	assert.Equal(t, expected, lines[:6])
}

func TestResourceRow_Detail_ManagedFalse(t *testing.T) {
	r := pkgmodel.Resource{
		Managed:    false,
		Properties: json.RawMessage(`{}`),
	}
	got := resourceRow(r)
	lines := got.detail(80)
	assert.Contains(t, lines, "Managed:  no")
}

// ---------------------------------------------------------------------------
// resourceRow: detail renderer — Properties tree
// ---------------------------------------------------------------------------

func TestResourceRow_Detail_PropertiesTree(t *testing.T) {
	props := json.RawMessage(`{"BucketName":"logs","Tags":{"Environment":"production","ManagedBy":"formae"}}`)
	r := pkgmodel.Resource{
		NativeID:   "arn:aws:s3:::logs",
		Stack:      "default",
		Type:       "AWS::S3::Bucket",
		Label:      "logs",
		Properties: props,
	}
	got := resourceRow(r)
	lines := got.detail(80)

	assert.Contains(t, lines, "Properties:")
	assert.Contains(t, lines, " BucketName: logs")
	assert.Contains(t, lines, " Tags:")
	assert.Contains(t, lines, "   Environment: production")
	assert.Contains(t, lines, "   ManagedBy: formae")
}

// ---------------------------------------------------------------------------
// resourceRow: detail renderer — ReadOnlyProperties section
// ---------------------------------------------------------------------------

func TestResourceRow_Detail_ReadOnlyProperties_NonEmpty(t *testing.T) {
	r := pkgmodel.Resource{
		Properties:         json.RawMessage(`{"BucketName":"b"}`),
		ReadOnlyProperties: json.RawMessage(`{"Arn":"arn:aws:s3:::b"}`),
	}
	got := resourceRow(r)
	lines := got.detail(80)

	assert.Contains(t, lines, "ReadOnlyProperties:")
	assert.Contains(t, lines, " Arn: arn:aws:s3:::b")
}

func TestResourceRow_Detail_ReadOnlyProperties_EmptyOmitted(t *testing.T) {
	r := pkgmodel.Resource{
		Properties:         json.RawMessage(`{"BucketName":"b"}`),
		ReadOnlyProperties: json.RawMessage(`{}`),
	}
	got := resourceRow(r)
	lines := got.detail(80)
	for _, l := range lines {
		assert.NotEqual(t, "ReadOnlyProperties:", l)
	}
}

func TestResourceRow_Detail_ReadOnlyProperties_NullOmitted(t *testing.T) {
	r := pkgmodel.Resource{
		Properties:         json.RawMessage(`{"BucketName":"b"}`),
		ReadOnlyProperties: json.RawMessage(`null`),
	}
	got := resourceRow(r)
	lines := got.detail(80)
	for _, l := range lines {
		assert.NotEqual(t, "ReadOnlyProperties:", l)
	}
}

func TestResourceRow_Detail_ReadOnlyProperties_NilOmitted(t *testing.T) {
	r := pkgmodel.Resource{
		Properties:         json.RawMessage(`{"BucketName":"b"}`),
		ReadOnlyProperties: nil,
	}
	got := resourceRow(r)
	lines := got.detail(80)
	for _, l := range lines {
		assert.NotEqual(t, "ReadOnlyProperties:", l)
	}
}

// ---------------------------------------------------------------------------
// fetch integration via newSpecs
// ---------------------------------------------------------------------------

func TestResourcesSpec_FetchDelegates(t *testing.T) {
	c := &fakeClient{
		forma: &pkgmodel.Forma{
			Resources: []pkgmodel.Resource{
				{NativeID: "arn:x", Stack: "$unmanaged", Type: "AWS::S3::Bucket", Label: "x"},
				{NativeID: "arn:y", Stack: "prod", Type: "AWS::S3::Bucket", Label: "y", Managed: true},
			},
		},
	}
	specs := newSpecs(nil)
	rows, nags, err := specs[TabResources].fetch(c, "", true)
	require.NoError(t, err)
	assert.Empty(t, nags)
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"arn:x", "⚠ unmanaged", "AWS::S3::Bucket", "x"}, rows[0].cells)
	assert.Equal(t, []string{"arn:y", "prod", "AWS::S3::Bucket", "y"}, rows[1].cells)
	assert.NotNil(t, rows[0].detail)
	assert.NotNil(t, rows[1].detail)
}
