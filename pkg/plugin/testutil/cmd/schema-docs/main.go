// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// schema-docs generates documentation for a PKL schema.
//
// Usage:
//
//	schema-docs ./schema/pkl
//	schema-docs --namespace aws ./schema/pkl
//	schema-docs --format markdown ./schema/pkl
//
// Exit codes:
//
//	0 - success
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
	format := flag.String("format", "plain", "Output format: plain, markdown")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: schema-docs [--namespace NAME] [--format plain|markdown] SCHEMA_DIR")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "SCHEMA_DIR should be the path to schema/pkl directory containing PklProject")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		fmt.Fprintln(os.Stderr, "  --namespace NAME   Override namespace (default: auto-detect from formae-plugin.pkl)")
		fmt.Fprintln(os.Stderr, "  --format FORMAT    Output format: plain (default), markdown")
		os.Exit(2)
	}

	schemaDir := flag.Arg(0)

	var result *testutil.DocsResult
	var err error

	if *namespace != "" {
		result, err = testutil.GenerateDocsWithNamespace(schemaDir, *namespace)
	} else {
		result, err = testutil.GenerateDocs(schemaDir)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(2)
	}

	switch *format {
	case "markdown", "md":
		fmt.Print(result.FormatMarkdownTable())
	case "plain", "text":
		fmt.Print(result.FormatPlainTable())
	default:
		fmt.Fprintf(os.Stderr, "Unknown format: %s (use 'plain' or 'markdown')\n", *format)
		os.Exit(2)
	}
}
