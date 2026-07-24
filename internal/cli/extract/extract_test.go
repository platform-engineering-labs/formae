// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package extract

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/schema"
)

func TestValidateExtractOptions(t *testing.T) {
	t.Run("missing target path", func(t *testing.T) {
		opts := &ExtractOptions{
			TargetPath: "",
		}
		err := validateExtractOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "target file is required", err.Error())
	})

	t.Run("target path is a directory", func(t *testing.T) {
		dir := t.TempDir()
		opts := &ExtractOptions{
			TargetPath: dir,
			Query:      "type:AWS::S3::Bucket",
		}
		err := validateExtractOptions(opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "is a directory, not a file")
	})

	t.Run("missing query", func(t *testing.T) {
		opts := &ExtractOptions{
			TargetPath: "output.pkl",
			Query:      "",
		}
		err := validateExtractOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "query is required", err.Error())
	})

}

func TestSchemaVersionNag(t *testing.T) {
	u := &schema.SchemaVersionUpgrade{ProjectDir: "/tmp/proj", Current: "0.85.0", Target: "0.88.0"}
	msg := schemaVersionNag(u)
	assert.Contains(t, msg, "/tmp/proj/PklProject")
	assert.Contains(t, msg, "0.85.0")
	assert.Contains(t, msg, "formae@0.88.0")
	assert.Contains(t, msg, "pkl project resolve")
}
