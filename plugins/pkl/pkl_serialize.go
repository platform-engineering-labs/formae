// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/apple/pkl-go/pkl"
	"github.com/platform-engineering-labs/formae/pkg/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// serializeWithPKL is a generic helper function that can serialize any data structure
func (p PKL) serializeWithPKL(data *pkgmodel.Forma, options *plugin.SerializeOptions) (string, error) {
	input, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("error marshalling JSON: %w", err)
	}

	properties := map[string]string{
		"Json": string(input),
	}
	// Create temporary directory and extract embedded files
	tempDir, err := os.MkdirTemp("", "pkl-generator-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Build package dependencies using PackageResolver
	resolver := NewPackageResolver()

	// Configure local schema resolution if requested
	schemaLocation := plugin.SchemaLocationRemote
	if options != nil && options.SchemaLocation != "" {
		schemaLocation = options.SchemaLocation
	}
	if schemaLocation == plugin.SchemaLocationLocal {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			pluginsDir := filepath.Join(homeDir, ".pel", "formae", "plugins")
			resolver.WithLocalSchemas(pluginsDir)
		}
	}

	resolver.Add("formae", "pkl", Version)

	// Extract namespaces from the data and add them as dependencies
	namespaces := extractNamespaces(data)
	for ns := range namespaces {
		// Each namespace uses itself as the plugin name (e.g., aws uses aws plugin)
		resolver.Add(ns, ns, Version)
	}

	includes := resolver.GetPackageStrings()

	err = fs.WalkDir(generator, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip files that will be generated dynamically
		if strings.Contains(path, "PklProject") ||
			path == "generator/resources.pkl" ||
			path == "generator/resolvables.pkl" {
			return nil
		}

		targetPath := filepath.Join(tempDir, path)

		if d.IsDir() {
			return os.MkdirAll(targetPath, 0755)
		}

		data, err := fs.ReadFile(generator, path)
		if err != nil {
			return err
		}

		return os.WriteFile(targetPath, data, 0644)
	})
	if err != nil {
		return "", fmt.Errorf("failed to extract generator files: %w", err)
	}

	generatorDir := filepath.Join(tempDir, "generator")

	// Step 1: Generate PklProject with correct dependencies
	err = p.ProjectInit(generatorDir, includes, schemaLocation)
	if err != nil {
		return "", fmt.Errorf("failed to initialize project: %w", err)
	}

	// Step 2: Generate imports.pkl from PklProject dependencies
	if err := p.generatePklFile(generatorDir, "ImportsGenerator.pkl", "imports.pkl"); err != nil {
		return "", fmt.Errorf("failed to generate imports.pkl: %w", err)
	}

	// Step 3: Generate resources.pkl with dynamic imports
	if err := p.generatePklFile(generatorDir, "ResourcesGenerator.pkl", "resources.pkl"); err != nil {
		return "", fmt.Errorf("failed to generate resources.pkl: %w", err)
	}

	// Step 4: Generate resolvables.pkl with dynamic imports
	if err := p.generatePklFile(generatorDir, "ResolvablesGenerator.pkl", "resolvables.pkl"); err != nil {
		return "", fmt.Errorf("failed to generate resolvables.pkl: %w", err)
	}

	// Step 5: Run the main generator
	evaluator, cleanup, err := newSafeProjectEvaluator(
		context.Background(),
		&url.URL{Scheme: "file", Path: tempDir + "/generator"},
		pkl.PreconfiguredOptions,
		pkl.WithResourceReader(libExtension{}),
		func(opts *pkl.EvaluatorOptions) {
			opts.Properties = properties
			opts.Logger = pkl.NoopLogger
		},
	)
	if err != nil {
		return "", err
	}

	defer cleanup()

	textOutput, err := evaluator.EvaluateOutputText(context.Background(), pkl.FileSource(filepath.Join(tempDir, "generator/runPklGenerator.pkl")))
	if err != nil {
		return "", fmt.Errorf("error evaluating PKL: %w", err)
	}

	return Format(textOutput), nil
}

// extractNamespaces extracts unique namespaces from the data.
// It handles both *model.Resource and *model.Forma types.
func extractNamespaces(data any) map[string]struct{} {
	namespaces := make(map[string]struct{})

	switch v := data.(type) {
	case *model.Resource:
		ns := strings.ToLower(v.Namespace())
		namespaces[ns] = struct{}{}
	case *model.Forma:
		for _, res := range v.Resources {
			ns := strings.ToLower(res.Namespace())
			namespaces[ns] = struct{}{}
		}
	}

	return namespaces
}

// generatePklFile evaluates a PKL generator file and writes the output to a target file.
// This is used in the multi-stage generation pipeline to create imports.pkl, resources.pkl, etc.
func (p PKL) generatePklFile(generatorDir, generatorName, outputName string) error {
	evaluator, cleanup, err := newSafeProjectEvaluator(
		context.Background(),
		&url.URL{Scheme: "file", Path: generatorDir},
		pkl.PreconfiguredOptions,
		func(opts *pkl.EvaluatorOptions) {
			opts.Logger = pkl.NoopLogger
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create evaluator for %s: %w", generatorName, err)
	}
	defer cleanup()

	result, err := evaluator.EvaluateOutputText(
		context.Background(),
		pkl.FileSource(filepath.Join(generatorDir, generatorName)),
	)
	if err != nil {
		return fmt.Errorf("failed to evaluate %s: %w", generatorName, err)
	}

	outputPath := filepath.Join(generatorDir, outputName)
	if err := os.WriteFile(outputPath, []byte(result), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputName, err)
	}

	return nil
}
