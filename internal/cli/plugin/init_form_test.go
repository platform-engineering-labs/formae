// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

func TestBuildInitForm_FlagsPrefill(t *testing.T) {
	th := theme.New("formae")
	v := &initFormValues{
		Name:      "cloudflare",
		Category:  "network",
		License:   "Apache-2.0",
		Namespace: "CLOUDFLARE",
	}
	form := buildInitForm(th, v, "")
	require.NotNil(t, form)
	// Values are bound via pointers; the struct must retain the pre-filled values.
	assert.Equal(t, "cloudflare", v.Name)
	assert.Equal(t, "network", v.Category)
	assert.Equal(t, "CLOUDFLARE", v.Namespace)
	// OutputDir is auto-derived from Name at build time when left empty.
	assert.Equal(t, "./cloudflare", v.OutputDir)
}

func TestInitFormValidators_NameCharset(t *testing.T) {
	t.Run("space in name rejected", func(t *testing.T) {
		err := validatePluginName("my plugin")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lowercase")
	})

	t.Run("valid lowercase hyphenated name accepted", func(t *testing.T) {
		assert.NoError(t, validatePluginName("cloudflare"))
		assert.NoError(t, validatePluginName("my-plugin-2"))
	})
}

func TestInitFormValidators_NamespaceNormalize(t *testing.T) {
	t.Run("validateNamespace allows mixed case", func(t *testing.T) {
		assert.NoError(t, validateNamespace("cloudflare"))
		assert.NoError(t, validateNamespace("CLOUDFLARE"))
	})

	t.Run("normalizeNamespace upcases and reports change", func(t *testing.T) {
		got, changed := normalizeNamespace("cloudflare")
		assert.Equal(t, "CLOUDFLARE", got)
		assert.True(t, changed)
	})

	t.Run("normalizeNamespace already-upper reports no change", func(t *testing.T) {
		got, changed := normalizeNamespace("CLOUDFLARE")
		assert.Equal(t, "CLOUDFLARE", got)
		assert.False(t, changed)
	})
}

func TestInitFormValidators_ModulePath(t *testing.T) {
	t.Run("empty path rejected", func(t *testing.T) {
		err := validateModulePath("")
		require.Error(t, err)
	})

	t.Run("plain word without dot rejected", func(t *testing.T) {
		// module.CheckPath requires a dot in the first path element.
		err := validateModulePath("notamodule")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing dot")
	})

	t.Run("valid module path accepted", func(t *testing.T) {
		assert.NoError(t, validateModulePath("github.com/acme/formae-plugin-cloudflare"))
	})

	t.Run("path with spaces rejected", func(t *testing.T) {
		err := validateModulePath("github.com/acme/my plugin")
		require.Error(t, err)
	})
}

func TestBuildInitForm_LicenseOther_GroupHidden(t *testing.T) {
	th := theme.New("formae")
	v := &initFormValues{License: "Apache-2.0"}
	buildInitForm(th, v, "")
	// When license is not "Other", the custom-license group is hidden.
	assert.True(t, isCustomLicenseGroupHidden(v))
}

func TestBuildInitForm_LicenseOther_GroupShown(t *testing.T) {
	th := theme.New("formae")
	v := &initFormValues{License: "Other"}
	buildInitForm(th, v, "")
	// When license is "Other", the custom-license group is NOT hidden.
	assert.False(t, isCustomLicenseGroupHidden(v))
	// The not-publishable warning copy is present for the custom-SPDX field.
	assert.Contains(t, customLicenseWarning, "Not publishable to hub.platform.engineering")
}

func TestBuildInitForm_OutputDir_LiveFollow(t *testing.T) {
	v := &initFormValues{Name: "cloudflare"}
	dirTouched := false
	applyNameToOutputDir(v, &dirTouched)
	assert.Equal(t, "./cloudflare", v.OutputDir)
}

func TestBuildInitForm_OutputDir_StopsFollowing(t *testing.T) {
	v := &initFormValues{Name: "cloudflare", OutputDir: "./custom"}
	dirTouched := true
	applyNameToOutputDir(v, &dirTouched)
	// With dirTouched=true, OutputDir must not be overwritten.
	assert.Equal(t, "./custom", v.OutputDir)
}

func TestBuildInitForm_OutputDir_NameChange(t *testing.T) {
	v := &initFormValues{Name: "cloudflare"}
	dirTouched := false
	applyNameToOutputDir(v, &dirTouched)
	assert.Equal(t, "./cloudflare", v.OutputDir)

	// Simulate user changing name.
	v.Name = "other-plugin"
	applyNameToOutputDir(v, &dirTouched)
	assert.Equal(t, "./other-plugin", v.OutputDir)
}

func TestBuildInitForm_NameError_PreMarked(t *testing.T) {
	th := theme.New("formae")
	v := &initFormValues{Name: "taken-name", License: "Apache-2.0"}
	form := buildInitForm(th, v, "plugin name 'taken-name' is already registered")
	require.NotNil(t, form)
	// The form was built without panic; nameError wiring is verified via the
	// nameOriginal mechanism tested through validatePluginNameWithError.
	nameOriginal := v.Name
	err := validatePluginNameWithError(v.Name, nameOriginal, "plugin name 'taken-name' is already registered")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "taken-name")
}
