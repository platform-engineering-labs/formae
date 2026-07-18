// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"golang.org/x/mod/module"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// initFormValues holds all field values for the plugin init interactive form.
// Callers pre-fill from CLI flags before calling buildInitForm; fields are
// bound by pointer so the form mutates v in place.
type initFormValues struct {
	Name          string
	Namespace     string
	Description   string
	Category      string
	Author        string
	ModulePath    string
	License       string
	CustomLicense string
	OutputDir     string
}

// hubLicenses is the closed set the formae Hub accepts for plugin registration.
var hubLicenses = []string{
	"Apache-2.0",
	"BSD-3-Clause",
	"MIT",
	"MPL-2.0",
	"Other",
}

// validateModulePath checks that path is a valid Go module path.
func validateModulePath(path string) error {
	if path == "" {
		return fmt.Errorf("module path is required")
	}
	if err := module.CheckPath(path); err != nil {
		return fmt.Errorf("invalid Go module path: %s", err)
	}
	return nil
}

// validatePluginNameWithError extends validatePluginName with an extra
// nameError that is returned when the name has not changed from nameOriginal.
// Task 14 uses this to pre-mark a name that failed the Hub availability check.
func validatePluginNameWithError(name, nameOriginal, nameError string) error {
	if err := validatePluginName(name); err != nil {
		return err
	}
	if nameError != "" && name == nameOriginal {
		return fmt.Errorf("%s", nameError)
	}
	return nil
}

// applyNameToOutputDir sets v.OutputDir to "./<v.Name>" when dirTouched is
// false, enabling the live-follow behaviour. Extracted so tests can drive it
// directly without running the full huh form.
func applyNameToOutputDir(v *initFormValues, dirTouched *bool) {
	if !*dirTouched {
		v.OutputDir = "./" + v.Name
	}
}

// isCustomLicenseGroupHidden reports whether the custom-license group should
// be hidden (i.e. License is not "Other"). Exposed for unit tests.
func isCustomLicenseGroupHidden(v *initFormValues) bool {
	return v.License != "Other"
}

// buildInitForm constructs the single-page huh form for plugin init.
//
// All fields are bound to v via pointers; the caller must allocate v (and
// pre-fill any flag-supplied values) before calling buildInitForm.
//
// nameError, when non-empty, pre-marks the name field invalid so that
// Task 14's check-on-submit loop can display the Hub rejection message
// without running a separate validation step.
func buildInitForm(th *theme.Theme, v *initFormValues, nameError string) *huh.Form {
	// Capture the name at form-build time so we can detect whether the user
	// has changed it (used to gate the pre-seeded nameError, Task 14).
	nameOriginal := v.Name

	// dirTouched tracks whether the user has manually edited OutputDir.
	// While false, OutputDir shadows v.Name automatically.
	dirTouched := false

	// Apply the default once at build time so the field shows the right value
	// before the user navigates to it.
	if v.OutputDir == "" && v.Name != "" {
		v.OutputDir = "./" + v.Name
	}

	// Apply category and license defaults.
	if v.Category == "" {
		v.Category = "other"
	}
	if v.License == "" {
		v.License = "Apache-2.0"
	}

	// --- category options ---
	catOpts := make([]huh.Option[string], len(pluginCategories))
	for i, c := range pluginCategories {
		catOpts[i] = huh.NewOption(c, c)
	}

	// --- license options ---
	licOpts := make([]huh.Option[string], len(hubLicenses))
	for i, l := range hubLicenses {
		licOpts[i] = huh.NewOption(l, l)
	}

	// --- namespace description: shows normalization note when applicable ---
	nsDesc := func() string {
		if upper := strings.ToUpper(v.Namespace); upper != v.Namespace && v.Namespace != "" {
			return fmt.Sprintf("Will be normalized to %q for resource type IDs", upper)
		}
		return "e.g. AWS, GCP, OCI — normalized to uppercase for resource type IDs"
	}

	// --- main group ---
	mainGroup := huh.NewGroup(
		huh.NewInput().
			Title("Plugin name").
			Value(&v.Name).
			Validate(func(s string) error {
				applyNameToOutputDir(v, &dirTouched)
				return validatePluginNameWithError(s, nameOriginal, nameError)
			}),

		huh.NewInput().
			Title("Namespace").
			DescriptionFunc(nsDesc, &v.Namespace).
			Value(&v.Namespace).
			Validate(func(s string) error {
				if err := validateNamespace(s); err != nil {
					return err
				}
				// Normalize in place so the value is uppercase on submit.
				if upper := strings.ToUpper(s); upper != s {
					v.Namespace = upper
				}
				return nil
			}),

		huh.NewInput().
			Title("Description").
			Value(&v.Description).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return fmt.Errorf("description is required")
				}
				return nil
			}),

		huh.NewSelect[string]().
			Title("Category").
			Options(catOpts...).
			Value(&v.Category),

		huh.NewInput().
			Title("Author").
			Description("Used for license copyright headers").
			Value(&v.Author).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return fmt.Errorf("author is required")
				}
				return nil
			}),

		huh.NewInput().
			Title("Go module path").
			Description("e.g. github.com/your-org/formae-plugin-name — used in go.mod and imports").
			Value(&v.ModulePath).
			Validate(validateModulePath),

		huh.NewSelect[string]().
			Title("License").
			Description("The formae Hub only accepts plugins under one of these licenses. Pick 'Other' to use a different license — your plugin will still build and run locally, but will not be publishable to the Hub.").
			Options(licOpts...).
			Value(&v.License),

		huh.NewInput().
			Title("Target directory").
			Description("Defaults to ./<name>; edit to override").
			Value(&v.OutputDir).
			Validate(func(s string) error {
				// Mark as touched once the user sets a value different from
				// the auto-derived default.
				if s != "" && s != "./"+v.Name {
					dirTouched = true
				}
				return nil
			}),
	)

	// --- custom license group (shown only when License == "Other") ---
	customLicenseGroup := huh.NewGroup(
		huh.NewInput().
			Title("Custom license identifier (SPDX)").
			Description("⚠ Not publishable to hub.platform.engineering. Switch to Apache-2.0, BSD-3-Clause, MIT, or MPL-2.0 to publish.").
			Value(&v.CustomLicense),
	).WithHideFunc(func() bool { return isCustomLicenseGroupHidden(v) })

	return components.NewThemedForm(th, mainGroup, customLicenseGroup)
}
