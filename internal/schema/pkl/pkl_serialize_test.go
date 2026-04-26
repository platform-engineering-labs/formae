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
