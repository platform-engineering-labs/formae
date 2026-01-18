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
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// serializeWithPKL is a generic helper function that can serialize any data structure
func (p PKL) serializeWithPKL(data any, options *plugin.SerializeOptions) (string, error) {
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

		// Skip the PklProject file which is only necessary for local development of Pkl generator
		if strings.Contains(path, "PklProject") {
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

	// Initialize project in tempDir to overwrite PklProject with correct dependencies
	err = p.ProjectInit(tempDir+"/generator", includes)
	if err != nil {
		return "", fmt.Errorf("failed to initialize project: %w", err)
	}

	evaluator, err := pkl.NewProjectEvaluator(
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

	defer func(evaluator pkl.Evaluator) {
		_ = evaluator.Close()
	}(evaluator)

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
