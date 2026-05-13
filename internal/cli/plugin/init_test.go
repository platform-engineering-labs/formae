// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
)

func TestValidatePluginInitOptions(t *testing.T) {
	t.Run("no-input with all required flags succeeds", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:        "myplugin",
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.NoError(t, err)
	})

	t.Run("no-input missing name returns error", func(t *testing.T) {
		opts := &PluginInitOptions{
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "--name")
	})

	t.Run("no-input missing multiple flags lists all missing", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:    "myplugin",
			NoInput: true,
		}
		err := validatePluginInitOptions(opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "--namespace")
		assert.Contains(t, err.Error(), "--description")
		assert.Contains(t, err.Error(), "--author")
		assert.Contains(t, err.Error(), "--module-path")
	})

	t.Run("no-input applies default license", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:        "myplugin",
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.NoError(t, err)
		assert.Equal(t, "Apache-2.0", opts.License)
	})

	t.Run("no-input applies default output dir from name", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:        "myplugin",
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.NoError(t, err)
		assert.Equal(t, "./myplugin", opts.OutputDir)
	})

	t.Run("no-input preserves provided license", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:        "myplugin",
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			License:     "MIT",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.NoError(t, err)
		assert.Equal(t, "MIT", opts.License)
	})

	t.Run("no-input preserves provided output dir", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:        "myplugin",
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			OutputDir:   "./custom/path",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.NoError(t, err)
		assert.Equal(t, "./custom/path", opts.OutputDir)
	})

	t.Run("interactive mode does not require flags", func(t *testing.T) {
		opts := &PluginInitOptions{
			NoInput: false,
		}
		err := validatePluginInitOptions(opts)
		assert.NoError(t, err)
	})

	t.Run("invalid plugin name returns error", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:        "Invalid_Name",
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "plugin name")
	})

	t.Run("invalid namespace returns error", func(t *testing.T) {
		opts := &PluginInitOptions{
			Name:        "myplugin",
			Namespace:   "invalid_ns",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
			NoInput:     true,
		}
		err := validatePluginInitOptions(opts)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "namespace")
	})
}

func TestValidateOutputDir(t *testing.T) {
	t.Run("non-existent directory is valid", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "nonexistent")
		err := validateOutputDir(dir)
		if err != nil {
			t.Errorf("expected no error for non-existent directory, got: %v", err)
		}
	})

	t.Run("empty existing directory is valid", func(t *testing.T) {
		dir := t.TempDir() // Creates an empty directory
		err := validateOutputDir(dir)
		if err != nil {
			t.Errorf("expected no error for empty directory, got: %v", err)
		}
	})

	t.Run("current directory when empty is valid", func(t *testing.T) {
		// Create a temp directory and change to it
		dir := t.TempDir()
		oldWd, _ := os.Getwd()
		_ = os.Chdir(dir)
		defer func() { _ = os.Chdir(oldWd) }()

		err := validateOutputDir(".")
		if err != nil {
			t.Errorf("expected no error for empty current directory, got: %v", err)
		}
	})

	t.Run("non-empty directory is invalid", func(t *testing.T) {
		dir := t.TempDir()
		// Create a file in the directory
		f, _ := os.Create(filepath.Join(dir, "somefile.txt"))
		_ = f.Close()

		err := validateOutputDir(dir)
		if err == nil {
			t.Error("expected error for non-empty directory, got nil")
		}
	})

	t.Run("current directory when non-empty is invalid", func(t *testing.T) {
		dir := t.TempDir()
		// Create a file in the directory
		f, _ := os.Create(filepath.Join(dir, "somefile.txt"))
		_ = f.Close()

		oldWd, _ := os.Getwd()
		_ = os.Chdir(dir)
		defer func() { _ = os.Chdir(oldWd) }()

		err := validateOutputDir(".")
		if err == nil {
			t.Error("expected error for non-empty current directory, got nil")
		}
	})
}

func TestValidatePluginName_HubRegex(t *testing.T) {
	t.Run("lowercase + hyphen + digits accepted", func(t *testing.T) {
		assert.NoError(t, validatePluginName("my-plugin-2"))
	})

	t.Run("uppercase first letter rejected", func(t *testing.T) {
		err := validatePluginName("Foo")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "lowercase")
	})

	t.Run("internal uppercase rejected", func(t *testing.T) {
		err := validatePluginName("myPlugin")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "lowercase")
	})

	t.Run("all caps rejected", func(t *testing.T) {
		err := validatePluginName("FOO")
		assert.Error(t, err)
	})

	t.Run("underscore still rejected (regression)", func(t *testing.T) {
		err := validatePluginName("invalid_name")
		assert.Error(t, err)
	})
}

func TestTransformContent_ConfigPKL(t *testing.T) {
	config := &PluginConfig{
		Name:      "sftp",
		Namespace: "SFTP",
	}

	input := `/// Users import this via: import "plugins:/<PluginName>.pkl" as <PluginName>
///   new <PluginName>.PluginConfig {
open module example.Config
    type = "example"
`
	result := transformContent(input, config)

	// Verify <PluginName> is replaced with capitalized name
	if strings.Contains(result, "<PluginName>") {
		t.Error("expected <PluginName> to be replaced")
	}
	if !strings.Contains(result, "Sftp") {
		t.Error("expected capitalized plugin name 'Sftp'")
	}
	// Verify module name
	if !strings.Contains(result, "module sftp.Config") {
		t.Errorf("expected 'module sftp.Config', got: %s", result)
	}
	// Verify type
	if !strings.Contains(result, `type = "sftp"`) {
		t.Errorf("expected type = sftp")
	}
}

func TestResolveHubURL(t *testing.T) {
	t.Run("flag set with valid URL wins", func(t *testing.T) {
		t.Setenv("FORMAE_HUB_URL", "https://env.example.com")
		opts := &PluginInitOptions{Hub: "https://flag.example.com"}
		got, explicit, err := resolveHubURL(opts)
		require.NoError(t, err)
		assert.Equal(t, "https://flag.example.com", got)
		assert.True(t, explicit, "flag-provided URL must be explicit")
	})

	t.Run("flag empty falls back to env", func(t *testing.T) {
		t.Setenv("FORMAE_HUB_URL", "https://env.example.com")
		opts := &PluginInitOptions{}
		got, explicit, err := resolveHubURL(opts)
		require.NoError(t, err)
		assert.Equal(t, "https://env.example.com", got)
		assert.True(t, explicit, "env-provided URL must be explicit")
	})

	t.Run("flag and env empty falls back to default", func(t *testing.T) {
		t.Setenv("FORMAE_HUB_URL", "")
		opts := &PluginInitOptions{}
		got, explicit, err := resolveHubURL(opts)
		require.NoError(t, err)
		assert.Equal(t, DefaultHubURL, got)
		assert.False(t, explicit, "default URL must not be explicit")
	})

	t.Run("flag with bad scheme returns FlagError", func(t *testing.T) {
		opts := &PluginInitOptions{Hub: "ftp://hub.example.com"}
		_, _, err := resolveHubURL(opts)
		require.Error(t, err)
		var flagErr *cmd.FlagError
		assert.True(t, errors.As(err, &flagErr))
	})

	t.Run("env with bad scheme returns FlagError", func(t *testing.T) {
		t.Setenv("FORMAE_HUB_URL", "ftp://hub.example.com")
		opts := &PluginInitOptions{}
		_, _, err := resolveHubURL(opts)
		require.Error(t, err)
		var flagErr *cmd.FlagError
		assert.True(t, errors.As(err, &flagErr))
	})
}

type fakeHubClient struct {
	res   AvailabilityResult
	err   error
	calls int
}

func (f *fakeHubClient) CheckPluginAvailability(ctx context.Context, name string) (AvailabilityResult, error) {
	f.calls++
	return f.res, f.err
}

func TestRunAvailabilityCheck(t *testing.T) {
	ctx := context.Background()

	t.Run("conflict, no allowConflict, returns plain error mentioning name and url and --allow-conflict", func(t *testing.T) {
		fc := &fakeHubClient{res: AvailabilityResult{Available: false, GitHubRepoURL: "https://github.com/x/y"}}
		err := runAvailabilityCheck(ctx, fc, "foo", false, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "foo")
		assert.Contains(t, err.Error(), "https://github.com/x/y")
		assert.Contains(t, err.Error(), "--allow-conflict")
	})

	t.Run("conflict, allowConflict=true, returns nil", func(t *testing.T) {
		fc := &fakeHubClient{res: AvailabilityResult{Available: false, GitHubRepoURL: "https://github.com/x/y"}}
		err := runAvailabilityCheck(ctx, fc, "foo", true, false)
		assert.NoError(t, err)
	})

	t.Run("available returns nil", func(t *testing.T) {
		fc := &fakeHubClient{res: AvailabilityResult{Available: true}}
		err := runAvailabilityCheck(ctx, fc, "foo", false, false)
		assert.NoError(t, err)
	})

	t.Run("HubTransientError always downgrades to nil (default hub)", func(t *testing.T) {
		fc := &fakeHubClient{err: &HubTransientError{Cause: errors.New("hub returned HTTP 503")}}
		err := runAvailabilityCheck(ctx, fc, "foo", false, false)
		assert.NoError(t, err)
	})

	t.Run("HubTransientError always downgrades to nil (explicit hub)", func(t *testing.T) {
		fc := &fakeHubClient{err: &HubTransientError{Cause: errors.New("hub returned HTTP 503")}}
		err := runAvailabilityCheck(ctx, fc, "foo", false, true)
		assert.NoError(t, err)
	})

	t.Run("HubUnreachableError with default hub downgrades to nil", func(t *testing.T) {
		fc := &fakeHubClient{err: &HubUnreachableError{Cause: errors.New("dial tcp: connection refused")}}
		err := runAvailabilityCheck(ctx, fc, "foo", false, false)
		assert.NoError(t, err, "unreachable default hub must warn-and-continue")
	})

	t.Run("HubUnreachableError with explicit hub is hard-fail", func(t *testing.T) {
		fc := &fakeHubClient{err: &HubUnreachableError{Cause: errors.New("dial tcp: connection refused")}}
		err := runAvailabilityCheck(ctx, fc, "foo", false, true)
		require.Error(t, err, "unreachable explicit hub must hard-fail")
		assert.Contains(t, err.Error(), "hub availability check failed")
	})

	t.Run("plain error (trust/protocol) is hard-fail", func(t *testing.T) {
		fc := &fakeHubClient{err: errors.New("hub TLS validation failed: x")}
		err := runAvailabilityCheck(ctx, fc, "foo", false, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "TLS")
	})

	t.Run("context.Canceled propagates as-is (not wrapped in cmd.FlagError)", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel() // immediately cancel so ctx.Err() == context.Canceled
		fc := &fakeHubClient{err: context.Canceled}
		err := runAvailabilityCheck(canceledCtx, fc, "foo", false, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, context.Canceled))
		// Must not be wrapped under "hub availability check failed: ..."
		assert.NotContains(t, err.Error(), "hub availability check failed")
	})

	t.Run("context.DeadlineExceeded propagates as-is", func(t *testing.T) {
		expiredCtx, cancel := context.WithTimeout(context.Background(), 1)
		defer cancel()
		time.Sleep(1 * time.Millisecond) // ensure the deadline has passed
		fc := &fakeHubClient{err: context.DeadlineExceeded}
		err := runAvailabilityCheck(expiredCtx, fc, "foo", false, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, context.DeadlineExceeded))
	})
}

func TestValidatePluginCategory(t *testing.T) {
	t.Run("each Hub-allowed category is accepted", func(t *testing.T) {
		for _, c := range pluginCategories {
			assert.NoError(t, validatePluginCategory(c), "expected %q to be accepted", c)
		}
	})

	t.Run("an off-allowlist category is rejected", func(t *testing.T) {
		err := validatePluginCategory("infrastructure")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "category must be one of")
		assert.Contains(t, err.Error(), "cloud")
	})
}

func TestValidatePluginInitOptions_Category(t *testing.T) {
	baseOpts := func() *PluginInitOptions {
		return &PluginInitOptions{
			Name:        "myplugin",
			Namespace:   "MYNS",
			Description: "A test plugin",
			Author:      "Test Author",
			ModulePath:  "github.com/test/formae-plugin-myplugin",
		}
	}

	t.Run("no-input with no --category defaults to 'other'", func(t *testing.T) {
		opts := baseOpts()
		opts.NoInput = true
		require.NoError(t, validatePluginInitOptions(opts))
		assert.Equal(t, "other", opts.Category)
	})

	t.Run("no-input preserves a valid --category", func(t *testing.T) {
		opts := baseOpts()
		opts.NoInput = true
		opts.Category = "cloud"
		require.NoError(t, validatePluginInitOptions(opts))
		assert.Equal(t, "cloud", opts.Category)
	})

	t.Run("no-input rejects an off-allowlist --category", func(t *testing.T) {
		opts := baseOpts()
		opts.NoInput = true
		opts.Category = "infrastructure"
		err := validatePluginInitOptions(opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid category")
		var flagErr *cmd.FlagError
		assert.True(t, errors.As(err, &flagErr), "category validation error should be a FlagError so cobra prints usage")
	})

	t.Run("interactive mode validates --category if supplied", func(t *testing.T) {
		opts := baseOpts()
		opts.NoInput = false
		opts.Category = "bogus"
		err := validatePluginInitOptions(opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid category")
	})

	t.Run("interactive mode leaves category empty when not supplied", func(t *testing.T) {
		opts := baseOpts()
		opts.NoInput = false
		require.NoError(t, validatePluginInitOptions(opts))
		assert.Equal(t, "", opts.Category, "category prompt should still run in runPluginInit; validate must not preempt it")
	})
}

func TestTransformContent_Category(t *testing.T) {
	template := `display {
    category = "other"
    kind = "plugin"
}`
	transformed := transformContent(template, &PluginConfig{
		Name:        "myplugin",
		Namespace:   "MYNS",
		Description: "A test plugin",
		Category:    "cloud",
		License:     "Apache-2.0",
		ModulePath:  "github.com/test/formae-plugin-myplugin",
	})
	assert.Contains(t, transformed, `category = "cloud"`)
	assert.NotContains(t, transformed, `category = "other"`)
}

func TestRunPluginInit_FlagNameValidatedInInteractiveMode(t *testing.T) {
	opts := &PluginInitOptions{
		Name:                "Foo", // uppercase: violates the lowercase-only rule
		NoInput:             false, // interactive mode
		NoAvailabilityCheck: true,  // skip hub call for this test
	}
	err := runPluginInit(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lowercase")
	var flagErr *cmd.FlagError
	assert.True(t, errors.As(err, &flagErr),
		"flag-supplied name validation error should be a *cmd.FlagError so cobra prints usage")
}
