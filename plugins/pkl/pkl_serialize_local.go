// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build local
// +build local

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/apple/pkl-go/pkl"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// serializeWithPKL is a generic helper function that can serialize any data structure
func (p PKL) serializeWithPKL(data any, options *plugin.SerializeOptions) (string, error) {
	// Marshal the input data to JSON
	input, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("error marshalling JSON: %w", err)
	}

	// Get current working directory
	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("error getting current working directory: %w", err)
	}

	// Use local generator directory
	generatorDir := filepath.Join(currentDir, "plugins/pkl/generator")
	if _, err := os.Stat(generatorDir); os.IsNotExist(err) {
		return "", fmt.Errorf("generator directory does not exist: %s", generatorDir)
	}

	// Verify generator.pkl exists
	generatorFile := filepath.Join(generatorDir, "runPklGenerator.pkl")
	if _, err := os.Stat(generatorFile); os.IsNotExist(err) {
		return "", fmt.Errorf("runPklGenerator.pkl does not exist: %s", generatorFile)
	}

	properties := map[string]string{
		"Json": string(input),
	}

	// Use the local generator directory directly with ProjectEvaluator
	evaluator, err := pkl.NewProjectEvaluator(
		context.Background(),
		generatorDir,
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

	// Evaluate generator.pkl directly from the local directory
	textOutput, err := evaluator.EvaluateOutputText(context.Background(), pkl.FileSource(generatorFile))
	if err != nil {
		return "", fmt.Errorf("error evaluating PKL: %w", err)
	}

	return Format(textOutput), nil
}
