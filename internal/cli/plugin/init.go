// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// PluginConfig holds the configuration for a new plugin
type PluginConfig struct {
	Name        string
	Namespace   string
	Description string
	License     string
	OutputDir   string
	ModulePath  string
}

// Template repository configuration
const (
	TemplateRepoOwner = "platform-engineering-labs"
	TemplateRepoName  = "formae-plugin-template"
	DefaultBranch     = "main"
)

// getTemplateTarballURL returns the GitHub tarball URL for the template
func getTemplateTarballURL(version string) string {
	if version == "" {
		version = DefaultBranch
	}
	return fmt.Sprintf(
		"https://github.com/%s/%s/archive/refs/heads/%s.tar.gz",
		TemplateRepoOwner, TemplateRepoName, version,
	)
}

func PluginInitCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new Formae plugin from template",
		Long: `Initialize a new Formae plugin from the GitHub plugin template.

This command interactively prompts for plugin configuration, clones the
template from GitHub, and customizes it for your plugin.

Template repository: github.com/platform-engineering-labs/formae-plugin-template`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginInit()
		},
		SilenceErrors: true,
	}

	return command
}

func runPluginInit() error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("Formae Plugin Initialization")
	fmt.Println("============================")
	fmt.Println()

	// Collect plugin configuration interactively
	config := &PluginConfig{}

	// Plugin name (required)
	name, err := promptRequired(reader, "Plugin name")
	if err != nil {
		return err
	}
	if err := validatePluginName(name); err != nil {
		return err
	}
	config.Name = name

	// Namespace (required)
	namespace, err := promptRequired(reader, "Namespace")
	if err != nil {
		return err
	}
	if err := validateNamespace(namespace); err != nil {
		return err
	}
	config.Namespace = namespace

	// Description (required)
	description, err := promptRequired(reader, "Description")
	if err != nil {
		return err
	}
	config.Description = description

	// License (optional, default: Apache-2.0)
	license, err := promptWithDefault(reader, "License", "Apache-2.0")
	if err != nil {
		return err
	}
	config.License = license

	// Output directory (optional, default: ./<name>)
	defaultDir := "./" + config.Name
	outputDir, err := promptWithDefault(reader, "Output directory", defaultDir)
	if err != nil {
		return err
	}
	config.OutputDir = outputDir

	// Module path (derived from name)
	config.ModulePath = fmt.Sprintf("github.com/platform-engineering-labs/formae-plugin-%s", config.Name)

	fmt.Println()

	// Check if output directory already exists
	if _, err := os.Stat(config.OutputDir); err == nil {
		return fmt.Errorf("output directory %q already exists", config.OutputDir)
	}

	// Download and extract the template
	fmt.Printf("Downloading template from GitHub...\n")
	if err := downloadAndExtractTemplate(config.OutputDir); err != nil {
		return fmt.Errorf("failed to download template: %w", err)
	}

	// Transform template files
	fmt.Printf("Customizing plugin '%s'...\n", config.Name)
	if err := transformTemplateFiles(config); err != nil {
		return fmt.Errorf("failed to customize template: %w", err)
	}

	// Print success message with next steps
	fmt.Println()
	fmt.Println("Done! Next steps:")
	fmt.Printf("  1. cd %s\n", config.OutputDir)
	fmt.Println("  2. Define your resources in schema/pkl/")
	fmt.Printf("  3. Implement ResourcePlugin interface in %s.go\n", config.Name)
	fmt.Println("  4. Run 'make build' to build the plugin")

	return nil
}

func promptRequired(reader *bufio.Reader, prompt string) (string, error) {
	for {
		fmt.Printf("%s: ", prompt)
		input, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		input = strings.TrimSpace(input)
		if input != "" {
			return input, nil
		}
		fmt.Printf("  %s is required\n", prompt)
	}
}

func promptWithDefault(reader *bufio.Reader, prompt, defaultValue string) (string, error) {
	fmt.Printf("%s [%s]: ", prompt, defaultValue)
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultValue, nil
	}
	return input, nil
}

func validatePluginName(name string) error {
	// Must be lowercase, start with letter, contain only letters/numbers/hyphens
	pattern := regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	if !pattern.MatchString(name) {
		return fmt.Errorf("plugin name must be lowercase, start with a letter, and contain only letters, numbers, and hyphens")
	}
	return nil
}

func validateNamespace(namespace string) error {
	// Must be uppercase start, contain only letters/numbers
	pattern := regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	if !pattern.MatchString(namespace) {
		return fmt.Errorf("namespace must start with uppercase letter and contain only letters and numbers")
	}
	return nil
}

func downloadAndExtractTemplate(outputDir string) error {
	branch := DefaultBranch
	url := getTemplateTarballURL(branch)

	// Download the tarball
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download template: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download template: HTTP %d", resp.StatusCode)
	}

	// Create a gzip reader
	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer func() { _ = gzr.Close() }()

	// Create a tar reader
	tr := tar.NewReader(gzr)

	// GitHub tarballs have a root directory named "{repo}-{branch}/"
	// We need to strip this prefix when extracting
	rootPrefix := fmt.Sprintf("%s-%s/", TemplateRepoName, branch)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar entry: %w", err)
		}

		// Skip entries that don't start with the expected prefix
		if !strings.HasPrefix(header.Name, rootPrefix) {
			continue
		}

		// Strip the root directory prefix
		relPath := strings.TrimPrefix(header.Name, rootPrefix)
		if relPath == "" {
			continue
		}

		targetPath := filepath.Join(outputDir, relPath)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", targetPath, err)
			}
		case tar.TypeReg:
			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return fmt.Errorf("failed to create parent directory for %s: %w", targetPath, err)
			}

			// Create the file
			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create file %s: %w", targetPath, err)
			}

			if _, err := io.Copy(outFile, tr); err != nil {
				_ = outFile.Close()
				return fmt.Errorf("failed to write file %s: %w", targetPath, err)
			}
			if err := outFile.Close(); err != nil {
				return fmt.Errorf("failed to close file %s: %w", targetPath, err)
			}
		}
	}

	return nil
}

func transformTemplateFiles(config *PluginConfig) error {
	// Walk the output directory and transform files
	return filepath.Walk(config.OutputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(config.OutputDir, path)
		if err != nil {
			return err
		}

		// Check if file needs renaming
		newRelPath := transformPath(relPath, config)
		newPath := filepath.Join(config.OutputDir, newRelPath)

		// Read file content
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		// Transform content
		transformed := transformContent(string(content), config)

		// If path changed, remove old file
		if newPath != path {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("failed to remove %s: %w", path, err)
			}
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory for %s: %w", newPath, err)
		}

		// Write transformed content
		if err := os.WriteFile(newPath, []byte(transformed), info.Mode()); err != nil {
			return fmt.Errorf("failed to write %s: %w", newPath, err)
		}

		if newPath != path {
			fmt.Printf("  %s -> %s\n", relPath, newRelPath)
		}

		return nil
	})
}

func transformPath(path string, config *PluginConfig) string {
	// Rename files based on plugin name
	// plugin.go -> <name>.go
	// plugin_test.go -> <name>_test.go
	// example.pkl -> <name>.pkl
	path = strings.ReplaceAll(path, "plugin.go", config.Name+".go")
	path = strings.ReplaceAll(path, "plugin_test.go", config.Name+"_test.go")
	path = strings.ReplaceAll(path, "example.pkl", config.Name+".pkl")
	return path
}

func transformContent(content string, config *PluginConfig) string {
	// Replace template placeholders
	// The template uses hardcoded values that we need to replace

	// Module path
	content = strings.ReplaceAll(content, "github.com/your-org/formae-plugin-example", config.ModulePath)

	// Plugin name (lowercase)
	content = strings.ReplaceAll(content, `name = "example"`, fmt.Sprintf(`name = "%s"`, config.Name))

	// Namespace (uppercase)
	content = strings.ReplaceAll(content, `namespace = "EXAMPLE"`, fmt.Sprintf(`namespace = "%s"`, config.Namespace))
	content = strings.ReplaceAll(content, "EXAMPLE::", config.Namespace+"::")

	// Description
	content = strings.ReplaceAll(content, `description = "Example Formae plugin template"`, fmt.Sprintf(`description = "%s"`, config.Description))

	// License
	content = strings.ReplaceAll(content, `license = "Apache-2.0"`, fmt.Sprintf(`license = "%s"`, config.License))

	// Repository URL
	content = strings.ReplaceAll(content,
		"https://github.com/your-org/formae-plugin-example",
		fmt.Sprintf("https://github.com/platform-engineering-labs/formae-plugin-%s", config.Name))

	// Copyright header
	content = strings.ReplaceAll(content, "© 2025 Your Name", "© 2025 Platform Engineering Labs Inc.")
	content = strings.ReplaceAll(content, "SPDX-License-Identifier: Apache-2.0", fmt.Sprintf("SPDX-License-Identifier: %s", config.License))

	return content
}
