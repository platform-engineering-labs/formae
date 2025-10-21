// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build !local
// +build !local

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/apple/pkl-go/pkl"
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

	includes := []string{
		"pkl.formae@" + Version,
		"aws.aws@" + Version,
	}

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
		tempDir+"/generator",
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
