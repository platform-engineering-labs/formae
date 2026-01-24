// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/prompter"
)

// PluginConfig holds the configuration for a new plugin
type PluginConfig struct {
	Name        string
	Namespace   string
	Description string
	Author      string
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
	p := prompter.NewBasicPrompter()

	fmt.Println(display.Gold("Formae Plugin Initialization"))
	fmt.Println()
	fmt.Println("Plugin initialization requires several parameters that will appear in the")
	fmt.Println("plugin's manifest file (formae-plugin.pkl). You can change them at any time later.")
	fmt.Println()
	fmt.Println("Please provide the following information:")
	fmt.Println()

	// Collect plugin configuration interactively
	config := &PluginConfig{}

	// Plugin name (required)
	name, err := p.PromptString("Plugin name")
	if err != nil {
		return err
	}
	if err := validatePluginName(name); err != nil {
		return err
	}
	config.Name = name

	// Namespace (required)
	namespace, err := p.PromptString("Namespace that uniquely identifies the target technology (e.g. AWS, GCP, OCI)")
	if err != nil {
		return err
	}
	if err := validateNamespace(namespace); err != nil {
		return err
	}
	config.Namespace = namespace

	// Description (required)
	description, err := p.PromptString("Plugin description")
	if err != nil {
		return err
	}
	config.Description = description

	// Author (required, for license copyright)
	author, err := p.PromptString("Plugin author")
	if err != nil {
		return err
	}
	config.Author = author

	// License (select from common SPDX identifiers or enter custom)
	licenseOptions := []string{
		"Apache-2.0",
		"MIT",
		"GPL-3.0-only",
		"BSD-3-Clause",
		"MPL-2.0",
		"AGPL-3.0-only",
		"FSL-1.1-ALv2",
		"Other",
	}
	license, err := p.PromptChoice("Plugin license", licenseOptions, 0)
	if err != nil {
		return err
	}
	if license == "Other" {
		license, err = p.PromptString("Enter license identifier")
		if err != nil {
			return err
		}
	}
	config.License = license

	// Target directory (optional, default: ./<name>)
	defaultDir := "./" + config.Name
	outputDir, err := p.PromptStringWithDefault("Target directory", defaultDir)
	if err != nil {
		return err
	}
	config.OutputDir = expandTilde(outputDir)

	// Module path (derived from name)
	config.ModulePath = fmt.Sprintf("github.com/platform-engineering-labs/formae-plugin-%s", config.Name)

	fmt.Println()

	// Check if output directory already exists
	if _, err := os.Stat(config.OutputDir); err == nil {
		return fmt.Errorf("target directory %q already exists", config.OutputDir)
	}

	// Download and extract the template
	fmt.Printf("%s\n", display.Grey("Downloading template from GitHub..."))
	if err := downloadAndExtractTemplate(config.OutputDir); err != nil {
		return fmt.Errorf("failed to download template: %w", err)
	}

	// Transform template files
	fmt.Printf("%s\n", display.Grey(fmt.Sprintf("Initializing plugin '%s' from template...", config.Name)))
	if err := transformTemplateFiles(config); err != nil {
		return fmt.Errorf("failed to customize template: %w", err)
	}

	// Set up the LICENSE file and clean up
	if err := finalizeLicense(config); err != nil {
		return fmt.Errorf("failed to finalize license: %w", err)
	}

	// Print success message with next steps
	fmt.Println()
	fmt.Println(display.Green("Done!") + " Next steps:")
	fmt.Printf(display.Grey("  1. cd %s\n"), config.OutputDir)
	fmt.Println(display.Grey("  2. Define your resources in schema/pkl/"))
	fmt.Printf(display.Grey("  3. Implement ResourcePlugin interface in %s.go\n"), config.Name)
	fmt.Println(display.Grey("  4. Run 'make build' to build the plugin"))

	return nil
}

func validatePluginName(name string) error {
	// Must start with letter, contain only letters/numbers/hyphens (no spaces)
	pattern := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]*$`)
	if !pattern.MatchString(name) {
		return fmt.Errorf("plugin name must start with a letter and contain only letters, numbers, and hyphens (no spaces)")
	}
	return nil
}

func validateNamespace(namespace string) error {
	// Must start with letter, contain only letters/numbers (no hyphens, no spaces)
	pattern := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*$`)
	if !pattern.MatchString(namespace) {
		return fmt.Errorf("namespace must start with a letter and contain only letters and numbers (no spaces or hyphens)")
	}
	return nil
}

// expandTilde expands ~ to the user's home directory
func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return home
	}
	return path
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

// finalizeLicense copies the selected license to LICENSE and removes the licenses folder
func finalizeLicense(config *PluginConfig) error {
	licensesDir := filepath.Join(config.OutputDir, "licenses")
	licenseFile := filepath.Join(config.OutputDir, "LICENSE")
	selectedLicensePath := filepath.Join(licensesDir, config.License+".txt")

	// Try to read the selected license template
	licenseText, err := os.ReadFile(selectedLicensePath)
	if err != nil {
		// License template not found - create a simple LICENSE file for custom licenses
		year := fmt.Sprintf("%d", time.Now().Year())
		content := fmt.Sprintf("Copyright %s %s\n\nLicense: %s\n", year, config.Author, config.License)
		if err := os.WriteFile(licenseFile, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write LICENSE file: %w", err)
		}
	} else {
		// Replace placeholders with author and year
		year := fmt.Sprintf("%d", time.Now().Year())
		content := string(licenseText)
		content = strings.ReplaceAll(content, "[year]", year)
		content = strings.ReplaceAll(content, "[yyyy]", year)
		content = strings.ReplaceAll(content, "[fullname]", config.Author)
		content = strings.ReplaceAll(content, "[name of copyright owner]", config.Author)
		// FSL format placeholders
		content = strings.ReplaceAll(content, "${year}", year)
		content = strings.ReplaceAll(content, "${licensor name}", config.Author)

		// Write to LICENSE
		if err := os.WriteFile(licenseFile, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write LICENSE file: %w", err)
		}
	}

	// Remove the licenses directory
	if err := os.RemoveAll(licensesDir); err != nil {
		return fmt.Errorf("failed to remove licenses directory: %w", err)
	}

	return nil
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

	// Namespace (uppercase in resource types)
	upperNamespace := strings.ToUpper(config.Namespace)
	content = strings.ReplaceAll(content, `namespace = "EXAMPLE"`, fmt.Sprintf(`namespace = "%s"`, upperNamespace))
	content = strings.ReplaceAll(content, "EXAMPLE::", upperNamespace+"::")

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
	// Note: String split to avoid REUSE tool misinterpreting this as a license declaration
	content = strings.ReplaceAll(content, "SPDX-"+"License-Identifier: Apache-2.0", fmt.Sprintf("SPDX-"+"License-Identifier: %s", config.License))

	return content
}
