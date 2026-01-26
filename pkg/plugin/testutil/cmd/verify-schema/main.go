// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// verify-schema verifies a PKL schema for duplicate files and resource types.
//
// Usage:
//
//	verify-schema ./schema/pkl
//	verify-schema --namespace aws ./schema/pkl
//
// Exit codes:
//
//	0 - verification passed
//	1 - verification failed (duplicates found)
//	2 - error (invalid path, missing files, etc.)
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/platform-engineering-labs/formae/pkg/plugin/testutil"
)

func main() {
	namespace := flag.String("namespace", "", "Override namespace (default: auto-detect from formae-plugin.pkl)")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: verify-schema [--namespace NAME] SCHEMA_DIR")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "SCHEMA_DIR should be the path to schema/pkl directory containing PklProject")
		os.Exit(2)
	}

	schemaDir := flag.Arg(0)

	var result *testutil.VerifyResult
	var err error
	var ns string

	if *namespace != "" {
		ns = *namespace
		result, err = testutil.VerifySchemaWithNamespace(schemaDir, ns)
	} else {
		result, err = testutil.VerifySchema(schemaDir)
		// Extract namespace for report
		ns = "SCHEMA"
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(2)
	}

	// Print human-readable report
	fmt.Print(result.FormatReport(ns))

	if result.HasErrors {
		os.Exit(1)
	}
	os.Exit(0)
}
