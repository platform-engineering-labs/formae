// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit
// +build unit

package extract

import (
	"github.com/stretchr/testify/assert"
	"testing"
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
