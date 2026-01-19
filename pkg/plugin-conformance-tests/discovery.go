// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TestCase represents a single plugin resource test case
type TestCase struct {
	Name         string // e.g., "AWS::S3::Bucket"
	PKLFile      string // Absolute path to the base/create PKL file (required)
	UpdateFile   string // Absolute path to the update variant PKL file (optional)
	ReplaceFile  string // Absolute path to the replace variant PKL file (optional)
	PluginName   string // e.g., "aws"
	ResourceType string // e.g., "s3-bucket"
}

// DiscoverTestData finds all PKL files in a plugin's testdata directory
// and their optional -update.pkl and -replace.pkl variants
func DiscoverTestData(pluginPath string) ([]TestCase, error) {
	testDataDir := filepath.Join(pluginPath, "testdata")

	// Check if testdata directory exists
	if _, err := os.Stat(testDataDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("testdata directory not found at %s", testDataDir)
	}

	var testCases []TestCase

	// Walk the testdata directory to find all .pkl files
	err := filepath.Walk(testDataDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-PKL files
		if info.IsDir() || !strings.HasSuffix(path, ".pkl") {
			return nil
		}

		// Skip variant files (-update.pkl and -replace.pkl) as they will be discovered
		// alongside their base files
		basename := filepath.Base(path)
		if strings.HasSuffix(basename, "-update.pkl") || strings.HasSuffix(basename, "-replace.pkl") {
			return nil
		}

		// This is a base test case file
		// Extract resource type from filename (e.g., "s3-bucket.pkl" -> "s3-bucket")
		resourceType := strings.TrimSuffix(basename, ".pkl")

		// Check for variant files in the same directory
		dir := filepath.Dir(path)
		baseNameWithoutExt := strings.TrimSuffix(basename, ".pkl")

		updateFile := filepath.Join(dir, baseNameWithoutExt+"-update.pkl")
		replaceFile := filepath.Join(dir, baseNameWithoutExt+"-replace.pkl")

		// Check if variant files exist
		var updatePath, replacePath string
		if _, err := os.Stat(updateFile); err == nil {
			updatePath = updateFile
		}
		if _, err := os.Stat(replaceFile); err == nil {
			replacePath = replaceFile
		}

		// Extract plugin name from path
		pluginName := filepath.Base(pluginPath)

		// Create a readable test name
		testName := fmt.Sprintf("%s::%s", strings.ToUpper(pluginName), resourceType)

		testCases = append(testCases, TestCase{
			Name:         testName,
			PKLFile:      path,
			UpdateFile:   updatePath,
			ReplaceFile:  replacePath,
			PluginName:   pluginName,
			ResourceType: resourceType,
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk testdata directory: %w", err)
	}

	if len(testCases) == 0 {
		return nil, fmt.Errorf("no PKL test files found in %s", testDataDir)
	}

	return testCases, nil
}
