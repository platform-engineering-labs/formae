// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/schema"
	"github.com/platform-engineering-labs/formae/pkg/model"
)

func TestResolveIncludes_PreResolvedDepsTakePrecedence(t *testing.T) {
	forma := &model.Forma{Resources: []model.Resource{{Type: "AWS::S3::Bucket"}}}
	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: "/this/path/should/not/be/touched",
		Dependencies: []string{
			"pkl.formae@0.85.0",
			"local:aws:/some/path/PklProject",
		},
	}

	got := resolveIncludes(forma, options)

	assert.ElementsMatch(t, []string{
		"pkl.formae@0.85.0",
		"local:aws:/some/path/PklProject",
	}, got)
}

func TestResolveIncludes_RemoteOnlyWhenNoDirAndNoDeps(t *testing.T) {
	forma := &model.Forma{Resources: []model.Resource{{Type: "AWS::S3::Bucket"}}}
	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationRemote,
	}

	got := resolveIncludes(forma, options)

	// formae version is the binary's compile-time version (test build = "0.0.0"),
	// so it's filtered out of the includes by the resolver. We expect only the
	// remote aws entry — and since aws version is "" (no installed version), it's
	// added as a plain namespace.
	assert.Contains(t, got, "aws.aws@")
}

func TestFormatSchemaVersions_Nil(t *testing.T) {
	assert.Equal(t, "", formatSchemaVersions(nil))
	assert.Equal(t, "", formatSchemaVersions(&schema.SerializeOptions{}))
}

func TestFormatSchemaVersions_SingleEntry(t *testing.T) {
	options := &schema.SerializeOptions{
		SchemaVersions: map[string]string{"k8s": "v1.30"},
	}
	assert.Equal(t, "k8s=v1.30", formatSchemaVersions(options))
}

func TestFormatSchemaVersions_StableOrderAcrossKeys(t *testing.T) {
	options := &schema.SerializeOptions{
		SchemaVersions: map[string]string{
			"k8s": "v1.30",
			"aws": "v2024-01-01",
		},
	}
	// Sorted ascending so the property string is deterministic for caching
	// and reproducible test output.
	assert.Equal(t, "aws=v2024-01-01,k8s=v1.30", formatSchemaVersions(options))
}

func TestFormatSchemaVersions_LowercasesNamespace(t *testing.T) {
	options := &schema.SerializeOptions{
		SchemaVersions: map[string]string{"K8S": "v1.30"},
	}
	assert.Equal(t, "k8s=v1.30", formatSchemaVersions(options),
		"namespace is lowercased so ImportsGenerator's pkg-name comparison hits regardless of casing in the source map")
}

func TestFormatSchemaVersions_DropsBlankEntries(t *testing.T) {
	options := &schema.SerializeOptions{
		SchemaVersions: map[string]string{
			"k8s": "v1.30",
			"":    "v9",
			"aws": "",
		},
	}
	assert.Equal(t, "k8s=v1.30", formatSchemaVersions(options))
}
