// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"context"
	"path/filepath"
	"runtime"

	"github.com/platform-engineering-labs/formae/pkg/plugin/descriptors"
)

func init() {
	deps := []descriptors.Dependency{}

	if Version == "0.0.0" {
		// Development mode: use local PKL files relative to source
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			panic("Failed to get current file path for AWS plugin")
		}
		pluginDir := filepath.Dir(thisFile)

		// Define dependencies using local paths
		deps = []descriptors.Dependency{
			{Name: "formae", Value: filepath.Join(pluginDir, "../pkl/schema/PklProject")},
			{Name: "aws", Value: filepath.Join(pluginDir, "schema/pkl/PklProject")},
		}
	} else {
		deps = []descriptors.Dependency{
			{Name: "formae", Value: "package://hub.platform.engineering/plugins/pkl/schema/pkl/formae/formae@" + Version},
			{Name: "aws", Value: "package://hub.platform.engineering/plugins/aws/schema/pkl/aws/aws@" + Version},
		}
	}

	ctx := context.Background()
	descriptorSlice, err := descriptors.ExtractSchemaFromDependencies(ctx, deps)
	if err != nil {
		panic("Failed to extract resource descriptors: " + err.Error())
	}
	initDescriptors(descriptorSlice)
}
