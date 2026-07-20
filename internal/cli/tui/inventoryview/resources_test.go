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
	assert.Equal(t, []string{"logs", "⚠ unmanaged", "AWS::S3::Bucket", "arn:aws:s3:::logs"}, got.cells)
}

func TestResourceRow_Cells_ManagedStack(t *testing.T) {
	r := pkgmodel.Resource{
		NativeID: "arn:aws:s3:::mybucket",
		Stack:    "production",
		Type:     "AWS::S3::Bucket",
		Label:    "mybucket",
	}
	got := resourceRow(r)
	assert.Equal(t, []string{"mybucket", "production", "AWS::S3::Bucket", "arn:aws:s3:::mybucket"}, got.cells)
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
// resourceRow: detail renderer — merged Properties (desired + read-only)
// ---------------------------------------------------------------------------

// Read-only (cloud-computed) fields render alongside desired properties under a
// single "Properties:" header — never a separate "ReadOnlyProperties:" section.
func TestResourceRow_Detail_Properties_MergedWithReadOnly(t *testing.T) {
	r := pkgmodel.Resource{
		Properties:         json.RawMessage(`{"BucketName":"b"}`),
		ReadOnlyProperties: json.RawMessage(`{"Arn":"arn:aws:s3:::b"}`),
	}
	got := resourceRow(r)
	lines := got.detail(80)

	assert.Contains(t, lines, "Properties:")
	assert.Contains(t, lines, " Arn: arn:aws:s3:::b")
	assert.Contains(t, lines, " BucketName: b")
	// No separate read-only section.
	for _, l := range lines {
		assert.NotEqual(t, "ReadOnlyProperties:", l)
	}
}

// Empty / null / nil read-only properties simply contribute nothing.
func TestResourceRow_Detail_Properties_EmptyReadOnly(t *testing.T) {
	for _, ro := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`null`), nil} {
		r := pkgmodel.Resource{
			Properties:         json.RawMessage(`{"BucketName":"b"}`),
			ReadOnlyProperties: ro,
		}
		lines := resourceRow(r).detail(80)
		assert.Contains(t, lines, " BucketName: b")
		for _, l := range lines {
			assert.NotEqual(t, "ReadOnlyProperties:", l)
		}
	}
}

// Special-value wrappers render readably: SetOnce/Clear unwraps, Opaque masks,
// resolvables show "value → target", and {Key,Value} tags collapse.
func TestResourceRow_Detail_Properties_SpecialValues(t *testing.T) {
	r := pkgmodel.Resource{
		Properties: json.RawMessage(`{
			"Tags":[{"Key":"Name","Value":{"$strategy":"SetOnce","$value":"lifeline-igw","$visibility":"Clear"}}],
			"Password":{"$visibility":"Opaque","$value":"s3cr3t"},
			"VpcId":{"$res":true,"$label":"lifeline-vpc","$property":"VpcId","$value":"vpc-0b5"}
		}`),
	}
	lines := resourceRow(r).detail(80)

	assert.Contains(t, lines, " Tags:")
	assert.Contains(t, lines, "   - Name: lifeline-igw")
	assert.Contains(t, lines, " Password: "+opaqueMask)
	assert.Contains(t, lines, " VpcId: vpc-0b5  → lifeline-vpc.VpcId")
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
	assert.Equal(t, []string{"x", "⚠ unmanaged", "AWS::S3::Bucket", "arn:x"}, rows[0].cells)
	assert.Equal(t, []string{"y", "prod", "AWS::S3::Bucket", "arn:y"}, rows[1].cells)
	assert.NotNil(t, rows[0].detail)
	assert.NotNil(t, rows[1].detail)
}
