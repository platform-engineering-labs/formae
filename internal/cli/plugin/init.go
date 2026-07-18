// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// pluginCategories is the closed set the formae Hub accepts for the
// `display.category` field. Mirrors the Zod enum on the Hub side; keep
// in sync when the Hub's allowlist changes.
var pluginCategories = []string{
	"cloud",
	"auth",
	"config",
	"observability",
	"cicd",
	"network",
	"data",
	"security",
	"containers",
	"other",
}

// PluginConfig holds the configuration for a new plugin
type PluginConfig struct {
	Name        string
	Namespace   string
	Description string
	Category    string
	Author      string
	License     string
	OutputDir   string
	ModulePath  string
}

// PluginInitOptions holds the command-line options for plugin init
type PluginInitOptions struct {
	Name        string
	Namespace   string
	Description string
	Category    string
	Author      string
	ModulePath  string
	License     string
	OutputDir   string
	NoInput     bool

	// Hub availability check
	Hub                 string
	NoAvailabilityCheck bool
	AllowConflict       bool

	// Test seams — nil in production paths
	HubClient          HubClient
	TemplateDownloader TemplateDownloader
}

// TemplateDownloader fetches the plugin template tarball and extracts
// it into outputDir. Production uses httpTemplateDownloader; tests
// inject a spy via PluginInitOptions.TemplateDownloader.
type TemplateDownloader interface {
	Download(ctx context.Context, outputDir string) error
}

type httpTemplateDownloader struct{}

func (httpTemplateDownloader) Download(ctx context.Context, outputDir string) error {
	branch := DefaultBranch
	url := getTemplateTarballURL(branch)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to build template request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download template: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download template: HTTP %d", resp.StatusCode)
	}

	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)

	rootPrefix := fmt.Sprintf("%s-%s/", TemplateRepoName, branch)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar entry: %w", err)
		}

		if !strings.HasPrefix(header.Name, rootPrefix) {
			continue
		}

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
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return fmt.Errorf("failed to create parent directory for %s: %w", targetPath, err)
			}
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

// validatePluginInitOptions validates the options and applies defaults.
// When NoInput is true, all required fields must be provided.
// When NoInput is false (interactive mode), validation is skipped as values will be prompted.
func validatePluginInitOptions(opts *PluginInitOptions) error {
	if opts.NoInput {
		// Check for missing required flags
		var missing []string
		if opts.Name == "" {
			missing = append(missing, "--name")
		}
		if opts.Namespace == "" {
			missing = append(missing, "--namespace")
		}
		if opts.Description == "" {
			missing = append(missing, "--description")
		}
		if opts.Author == "" {
			missing = append(missing, "--author")
		}
		if opts.ModulePath == "" {
			missing = append(missing, "--module-path")
		}

		if len(missing) > 0 {
			return cmd.FlagErrorf("missing required flags for --no-input mode: %s", strings.Join(missing, ", "))
		}

		// Validate provided values
		if err := validatePluginName(opts.Name); err != nil {
			return cmd.FlagErrorf("invalid plugin name: %s", err.Error())
		}
		if err := validateNamespace(opts.Namespace); err != nil {
			return cmd.FlagErrorf("invalid namespace: %s", err.Error())
		}
		if opts.Category != "" {
			if err := validatePluginCategory(opts.Category); err != nil {
				return cmd.FlagErrorf("invalid category: %s", err.Error())
			}
		}

		// Apply defaults for optional fields
		if opts.License == "" {
			opts.License = "Apache-2.0"
		}
		if opts.Category == "" {
			opts.Category = "other"
		}
		if opts.OutputDir == "" {
			opts.OutputDir = "./" + opts.Name
		}
	} else if opts.Category != "" {
		// Interactive mode still validates a --category passed on the
		// command line so the prompt is skipped for the user.
		if err := validatePluginCategory(opts.Category); err != nil {
			return cmd.FlagErrorf("invalid category: %s", err.Error())
		}
	}

	return nil
}

// validatePluginCategory checks that the supplied category is in the
// closed allowlist the formae Hub accepts.
func validatePluginCategory(category string) error {
	for _, c := range pluginCategories {
		if c == category {
			return nil
		}
	}
	return fmt.Errorf("category must be one of: %s", strings.Join(pluginCategories, ", "))
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

// runPluginInitFn is the entrypoint cobra calls. Tests swap it to
// capture the constructed PluginInitOptions, then restore it via a
// t.Cleanup.
var runPluginInitFn = runPluginInit

func PluginInitCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new Formae plugin from template",
		Long: `Initialize a new Formae plugin from the GitHub plugin template.

This command interactively prompts for plugin configuration, clones the
template from GitHub, and customizes it for your plugin.

Use --no-input with all required flags for non-interactive mode (useful for
automation and LLM-assisted workflows).

Template repository: github.com/platform-engineering-labs/formae-plugin-template`,
		RunE: func(c *cobra.Command, args []string) error {
			opts := &PluginInitOptions{}
			opts.Name, _ = c.Flags().GetString("name")
			opts.Namespace, _ = c.Flags().GetString("namespace")
			opts.Description, _ = c.Flags().GetString("description")
			opts.Category, _ = c.Flags().GetString("category")
			opts.Author, _ = c.Flags().GetString("author")
			opts.ModulePath, _ = c.Flags().GetString("module-path")
			opts.License, _ = c.Flags().GetString("license")
			opts.OutputDir, _ = c.Flags().GetString("output-dir")
			opts.NoInput, _ = c.Flags().GetBool("no-input")
			opts.Hub, _ = c.Flags().GetString("hub")
			opts.NoAvailabilityCheck, _ = c.Flags().GetBool("no-availability-check")
			opts.AllowConflict, _ = c.Flags().GetBool("allow-conflict")

			if err := validatePluginInitOptions(opts); err != nil {
				return err
			}

			return runPluginInitFn(c.Context(), opts)
		},
		SilenceErrors: true,
	}

	command.Flags().String("name", "", "Plugin name (required for --no-input)")
	command.Flags().String("namespace", "", "Target technology namespace, e.g. AWS, GCP (required for --no-input)")
	command.Flags().String("description", "", "Plugin description (required for --no-input)")
	command.Flags().String("category", "",
		fmt.Sprintf("Plugin category (default: prompted in interactive mode, 'other' in --no-input). One of: %s", strings.Join(pluginCategories, ", ")))
	command.Flags().String("author", "", "Plugin author for license copyright (required for --no-input)")
	command.Flags().String("module-path", "", "Go module path, e.g. github.com/your-org/formae-plugin-foo (required for --no-input)")
	command.Flags().String("license", "", "SPDX license identifier (default: Apache-2.0)")
	command.Flags().String("output-dir", "", "Target directory (default: ./<name>)")
	command.Flags().Bool("no-input", false, "Disable interactive prompts; error if required flags are missing")
	command.Flags().String("hub", "",
		fmt.Sprintf("Hub base URL (default: $FORMAE_HUB_URL or %s)", DefaultHubURL))
	command.Flags().Bool("no-availability-check", false,
		"Skip the hub availability check (use for offline scaffolding or tests)")
	command.Flags().Bool("allow-conflict", false,
		"Scaffold even if the hub reports the plugin name is already registered")

	return command
}

// availabilityCheckResult carries the structured outcome of a single Hub
// availability check call. This allows the interactive loop to make
// distinct decisions (re-open form vs warn-and-proceed vs hard-fail)
// without re-interpreting errors.
type availabilityCheckResult struct {
	taken      bool   // name is registered by someone else
	registrant string // registrant URL when taken
	unchecked  bool   // check was skipped (transient/unreachable default hub / --no-availability-check)
	err        error  // hard error (explicit-hub unreachable, TLS/protocol, cancellation)
}

// classifyAvailability runs a single Hub availability check and returns a
// structured availabilityCheckResult. Progress and warning messages are written
// to w. This function is the unit-testable core of the interactive loop's
// availability step.
//
// Parameters:
//   - noCheck: when true, the hub is not called and the result is unchecked.
//   - explicitHub: when true, an unreachable hub is a hard error.
//   - allowConflict: when true, a taken name does NOT cause a nameErr in the
//     loop; the caller is responsible for emitting the ⚠ warning.
func classifyAvailability(
	ctx context.Context,
	client HubClient,
	name string,
	allowConflict bool,
	noCheck bool,
	explicitHub bool,
	w io.Writer,
) availabilityCheckResult {
	if noCheck {
		return availabilityCheckResult{unchecked: true}
	}

	res, err := client.CheckPluginAvailability(ctx, name)
	if err != nil {
		// Caller-driven cancellation: propagate as-is.
		if ctx.Err() != nil {
			return availabilityCheckResult{err: ctx.Err()}
		}
		// HubTransientError: always unchecked+continue.
		var transient *HubTransientError
		if errors.As(err, &transient) {
			return availabilityCheckResult{unchecked: true}
		}
		// HubUnreachableError: hard-fail only when hub was explicitly configured.
		var unreachable *HubUnreachableError
		if errors.As(err, &unreachable) {
			if explicitHub {
				return availabilityCheckResult{err: fmt.Errorf("hub availability check failed: %w", err)}
			}
			return availabilityCheckResult{unchecked: true}
		}
		// Any other error (TLS, protocol mismatch, unexpected HTTP code): hard-fail.
		return availabilityCheckResult{err: fmt.Errorf("hub availability check failed: %w", err)}
	}

	if !res.Available {
		return availabilityCheckResult{taken: true, registrant: res.GitHubRepoURL}
	}

	return availabilityCheckResult{}
}

// runOneAvailabilityIteration runs the Hub availability check for a single
// loop iteration and renders the step line to w. It returns (nameErr, hardErr):
//   - nameErr non-empty: the name is taken (without --allow-conflict); caller
//     should re-open the form with nameErr pre-marked.
//   - hardErr non-nil: a fatal error; caller should surface it and abort.
//   - both empty/nil: proceed to scaffolding.
//
// This function is extracted for unit-testability — the calling loop
// (runPluginInitInteractive) calls it after each form.Run().
func runOneAvailabilityIteration(
	ctx context.Context,
	client HubClient,
	name string,
	allowConflict bool,
	noCheck bool,
	explicitHub bool,
	w io.Writer,
) (nameErr string, hardErr error) {
	th := theme.New("formae")

	step := components.StartStep(w, th, "checking name availability…")

	res := classifyAvailability(ctx, client, name, allowConflict, noCheck, explicitHub, w)

	switch {
	case res.err != nil:
		step.Fail("availability check failed")
		return "", res.err

	case res.unchecked:
		step.Skip("○ unchecked — Hub will re-validate at registration time")
		return "", nil

	case res.taken && allowConflict:
		step.Warn(fmt.Sprintf("! '%s' already registered — continuing (--allow-conflict)", name))
		_, _ = fmt.Fprintln(w, "  ⚠ already registered — continuing; registration will fail at confirm time")
		return "", nil

	case res.taken:
		step.Fail(fmt.Sprintf("✗ '%s' taken — registered by %s", name, res.registrant))
		return fmt.Sprintf("'%s' is already registered by %s. Pick a different name (or pass --allow-conflict to scaffold anyway)",
			name, res.registrant), nil

	default:
		step.Done(fmt.Sprintf("✓ '%s' available", name))
		return "", nil
	}
}

// runPluginInit is the main entry point for the plugin init command.
// When NoInput is false, it runs the interactive huh form loop (D6).
// When NoInput is true, all values come from flags (unchanged path).
func runPluginInit(ctx context.Context, opts *PluginInitOptions) error {
	if opts.NoInput {
		return runPluginInitNoInput(ctx, opts)
	}

	// Validate a flag-supplied name before the TTY check so the user gets
	// a clear FlagError regardless of whether a TTY is present.
	if opts.Name != "" {
		if err := validatePluginName(opts.Name); err != nil {
			return cmd.FlagErrorf("invalid plugin name: %s", err.Error())
		}
	}

	// D8: interactive form requires a TTY on both stdin and stdout.
	if !tui.IsInteractive() {
		return fmt.Errorf("interactive mode requires a TTY; use --no-input with all required flags for non-interactive use")
	}

	return runPluginInitInteractive(ctx, opts)
}

// runPluginInitNoInput runs the non-interactive path (--no-input). All values
// are already validated and set. This function is UNCHANGED from the original
// behavior so existing tests keep passing.
func runPluginInitNoInput(ctx context.Context, opts *PluginInitOptions) error {
	config := &PluginConfig{
		Name:        opts.Name,
		Namespace:   opts.Namespace,
		Description: opts.Description,
		Category:    opts.Category,
		Author:      opts.Author,
		ModulePath:  opts.ModulePath,
		License:     opts.License,
		OutputDir:   expandTilde(opts.OutputDir),
	}

	// Normalize namespace.
	if normalized, changed := normalizeNamespace(config.Namespace); changed {
		fmt.Fprintf(os.Stderr, "Note: namespace %q normalized to %q for resource type IDs.\n", config.Namespace, normalized)
		config.Namespace = normalized
	} else {
		config.Namespace = normalized
	}

	// Hub availability check.
	if !opts.NoAvailabilityCheck {
		hubURL, explicit, err := resolveHubURL(opts)
		if err != nil {
			return err
		}
		client := opts.HubClient
		if client == nil {
			client = NewHubClient(hubURL)
		}
		if err := runAvailabilityCheck(ctx, client, config.Name, opts.AllowConflict, explicit); err != nil {
			return err
		}
	}

	// Validate output directory.
	if err := validateOutputDir(config.OutputDir); err != nil {
		return err
	}

	return runScaffold(ctx, opts, config, os.Stdout)
}

// runPluginInitInteractive runs the interactive huh form loop (D6).
// The loop:
//  1. Builds the form from &v (pre-filled from CLI flags).
//  2. Runs form.Run().
//  3. Checks Hub availability via runOneAvailabilityIteration.
//  4. On taken+!allowConflict: sets nameErr and continues (re-opens form with
//     nameErr pre-marked; all other fields in v are preserved — R5).
//  5. On any other outcome: breaks and proceeds to scaffolding.
func runPluginInitInteractive(ctx context.Context, opts *PluginInitOptions) error {
	th := theme.New("formae")
	w := os.Stdout

	// Pre-fill form values from CLI flags.
	v := initFormValues{
		Name:        opts.Name,
		Namespace:   opts.Namespace,
		Description: opts.Description,
		Category:    opts.Category,
		Author:      opts.Author,
		ModulePath:  opts.ModulePath,
		License:     opts.License,
		OutputDir:   opts.OutputDir,
	}

	// Resolve Hub client once (so tests can inject via opts.HubClient).
	var hubClient HubClient
	var explicitHub bool
	if !opts.NoAvailabilityCheck {
		hubURL, expl, err := resolveHubURL(opts)
		if err != nil {
			return err
		}
		explicitHub = expl
		if opts.HubClient != nil {
			hubClient = opts.HubClient
		} else {
			hubClient = NewHubClient(hubURL)
		}
	}

	var nameErr string

	for {
		form := buildInitForm(th, &v, nameErr)
		if err := form.Run(); err != nil {
			// esc / ctrl-c → non-zero exit (D8).
			return err
		}

		// Normalize namespace in place (the form validator uppercases it
		// during validation; ensure it's normalized before checking).
		if normalized, _ := normalizeNamespace(v.Namespace); normalized != v.Namespace {
			v.Namespace = normalized
		}

		// Run availability check for this iteration.
		iterNameErr, hardErr := runOneAvailabilityIteration(
			ctx, hubClient, v.Name,
			opts.AllowConflict,
			opts.NoAvailabilityCheck,
			explicitHub,
			w,
		)
		if hardErr != nil {
			return hardErr
		}
		if iterNameErr != "" {
			// Name is taken and --allow-conflict not set: re-open form.
			nameErr = iterNameErr
			continue
		}
		break
	}

	// Resolve final license: use CustomLicense when "Other" was chosen.
	finalLicense := v.License
	if v.License == "Other" && v.CustomLicense != "" {
		finalLicense = v.CustomLicense
	}

	config := &PluginConfig{
		Name:        v.Name,
		Namespace:   v.Namespace,
		Description: v.Description,
		Category:    v.Category,
		Author:      v.Author,
		ModulePath:  v.ModulePath,
		License:     finalLicense,
		OutputDir:   expandTilde(v.OutputDir),
	}

	// Validate output directory.
	if err := validateOutputDir(config.OutputDir); err != nil {
		return err
	}

	fmt.Println()
	return runScaffold(ctx, opts, config, w)
}

// runScaffold downloads the template, transforms files, finalizes the license,
// and prints the "Done! Next steps:" block. Used by both the interactive and
// --no-input paths.
func runScaffold(ctx context.Context, opts *PluginInitOptions, config *PluginConfig, w io.Writer) error {
	th := theme.New("formae")

	// Download template.
	dlStep := components.StartStep(w, th, "Downloading template from GitHub…")
	downloader := opts.TemplateDownloader
	if downloader == nil {
		downloader = httpTemplateDownloader{}
	}
	if err := downloader.Download(ctx, config.OutputDir); err != nil {
		dlStep.Fail("download failed")
		return fmt.Errorf("failed to download template: %w", err)
	}
	dlStep.Done("Downloading template from GitHub…")

	// Transform template.
	initStep := components.StartStep(w, th, fmt.Sprintf("Initializing plugin '%s' from template…", config.Name))
	if err := transformTemplateFiles(config); err != nil {
		initStep.Fail("initialization failed")
		return fmt.Errorf("failed to customize template: %w", err)
	}
	if err := finalizeLicense(config); err != nil {
		initStep.Fail("license finalization failed")
		return fmt.Errorf("failed to finalize license: %w", err)
	}
	initStep.Done(fmt.Sprintf("Initializing plugin '%s' from template…", config.Name))

	// Done! Next steps.
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, components.SectionHeader(th, "Done! Next steps:"))
	_, _ = fmt.Fprintf(w, "  1. cd %s\n", config.OutputDir)
	_, _ = fmt.Fprintln(w, "  2. Define your resources in schema/pkl/")
	_, _ = fmt.Fprintf(w, "  3. Implement ResourcePlugin interface in %s.go\n", config.Name)
	_, _ = fmt.Fprintln(w, "  4. Run 'make build' to build the plugin")

	return nil
}

func validatePluginName(name string) error {
	// Must start with lowercase letter, contain only lowercase letters/digits/hyphens (hub manifest rule)
	pattern := regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	if !pattern.MatchString(name) {
		return fmt.Errorf("plugin name must start with a lowercase letter and contain only lowercase letters, digits, and hyphens (hub manifest rule)")
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

// normalizeNamespace returns the namespace uppercased, plus a flag indicating
// whether the input differed from the normalized form (used to emit a notice).
func normalizeNamespace(input string) (normalized string, changed bool) {
	upper := strings.ToUpper(input)
	return upper, upper != input
}

// validateOutputDir checks if the output directory is valid for plugin initialization.
// It allows:
// - Non-existent directories (will be created)
// - Existing empty directories (including "." for current directory)
// It rejects:
// - Existing non-empty directories (to prevent overwriting)
func validateOutputDir(dir string) error {
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		// Directory doesn't exist, that's fine - it will be created
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to check directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("target path %q exists but is not a directory", dir)
	}

	// Directory exists, check if it's empty
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory %q: %w", dir, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("target directory %q already exists and is not empty", dir)
	}

	return nil
}

// resolveHubURL returns the normalized hub URL and whether it was set
// explicitly by the caller (via --hub flag or FORMAE_HUB_URL env var).
// When explicit is false the URL is the built-in default.
func resolveHubURL(opts *PluginInitOptions) (hubURL string, explicit bool, err error) {
	candidate := opts.Hub
	if candidate != "" {
		explicit = true
	} else {
		candidate = os.Getenv("FORMAE_HUB_URL")
		if candidate != "" {
			explicit = true
		}
	}
	if candidate == "" {
		candidate = DefaultHubURL
	}
	normalized, valErr := validateHubURL(candidate)
	if valErr != nil {
		return "", false, cmd.FlagErrorf("invalid hub URL: %s", valErr)
	}
	return normalized, explicit, nil
}

func runAvailabilityCheck(
	ctx context.Context,
	client HubClient,
	name string,
	allowConflict bool,
	explicitHub bool,
) error {
	res, err := client.CheckPluginAvailability(ctx, name)
	if err != nil {
		// Propagate cancellation as-is so cobra surfaces the right exit.
		// The hub client returns ctx.Err() directly for caller-driven
		// cancellation, so checking ctx.Err() here correctly distinguishes
		// caller cancellation from hub-client-internal timeouts.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// HubTransientError: definitely temporary (timeout, 408, 429, 5xx).
		// Always warn-and-continue regardless of whether the hub was explicit.
		var transient *HubTransientError
		if errors.As(err, &transient) {
			fmt.Fprintln(os.Stderr, display.Grey(fmt.Sprintf(
				"Hub availability check skipped: %s. Hub will re-validate at registration time.",
				transient.Cause)))
			return nil
		}
		// HubUnreachableError: transport failure that is not definitively
		// temporary (DNS NXDOMAIN, connection refused). Fail closed when
		// the caller explicitly configured the hub URL — they likely have a
		// typo. Warn-and-continue only for the built-in default.
		var unreachable *HubUnreachableError
		if errors.As(err, &unreachable) {
			if explicitHub {
				return fmt.Errorf("hub availability check failed: %w", err)
			}
			fmt.Fprintln(os.Stderr, display.Grey(fmt.Sprintf(
				"Hub availability check skipped: %s. Hub will re-validate at registration time.",
				unreachable.Cause)))
			return nil
		}
		return fmt.Errorf("hub availability check failed: %w", err)
	}
	if !res.Available {
		if allowConflict {
			fmt.Fprintln(os.Stderr, display.Grey(fmt.Sprintf(
				"Warning: plugin name %q is already registered by %s. Continuing because --allow-conflict was set; registration will fail at confirm time.",
				name, res.GitHubRepoURL)))
			return nil
		}
		return fmt.Errorf(
			"plugin name %q is already registered by %s. Pick a different name (or pass --allow-conflict to scaffold anyway)",
			name, res.GitHubRepoURL)
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

	// Plugin name and type (lowercase)
	content = strings.ReplaceAll(content, `name = "example"`, fmt.Sprintf(`name = "%s"`, config.Name))
	content = strings.ReplaceAll(content, `type = "example"`, fmt.Sprintf(`type = "%s"`, config.Name))

	// Namespace (uppercase in resource types)
	content = strings.ReplaceAll(content, `namespace = "EXAMPLE"`, fmt.Sprintf(`namespace = "%s"`, config.Namespace))
	content = strings.ReplaceAll(content, "EXAMPLE::", config.Namespace+"::")

	// Description
	content = strings.ReplaceAll(content, `description = "Example Formae plugin template"`, fmt.Sprintf(`description = "%s"`, config.Description))

	// Category (the template ships with "other"; replace with the chosen value).
	content = strings.ReplaceAll(content, `category = "other"`, fmt.Sprintf(`category = "%s"`, config.Category))

	// License
	content = strings.ReplaceAll(content, `license = "Apache-2.0"`, fmt.Sprintf(`license = "%s"`, config.License))

	// Repository URL
	content = strings.ReplaceAll(content,
		"https://github.com/your-org/formae-plugin-example",
		fmt.Sprintf("https://github.com/platform-engineering-labs/formae-plugin-%s", config.Name))

	// Copyright header - use dynamic year and configured author
	currentYear := time.Now().Year()
	content = strings.ReplaceAll(content, "© 2025 Your Name", fmt.Sprintf("© %d %s", currentYear, config.Author))
	// Note: String split to avoid REUSE tool misinterpreting this as a license declaration
	content = strings.ReplaceAll(content, "SPDX-"+"License-Identifier: Apache-2.0", fmt.Sprintf("SPDX-"+"License-Identifier: %s", config.License))

	// PKL dependency alias - must match package name from schema's PklProject
	content = strings.ReplaceAll(content, `["example"]`, fmt.Sprintf(`["%s"]`, config.Name))

	// PKL import paths - longer match first to avoid partial replacement
	// @<packageName>/core/<packageName>.pkl (Phase 1b convention: resource in core/ subdir)
	content = strings.ReplaceAll(content,
		`@example/core/example.pkl`,
		fmt.Sprintf(`@%s/core/%s.pkl`, config.Name, config.Name))
	// @<packageName>/<packageName>.pkl (top-level, backwards compat with older templates)
	content = strings.ReplaceAll(content, `@example/example.pkl`, fmt.Sprintf(`@%s/%s.pkl`, config.Name, config.Name))

	// PKL project URIs (longer match first to avoid partial replacement)
	content = strings.ReplaceAll(content,
		"plugins/example/schema/pkl/example",
		fmt.Sprintf("plugins/%s/schema/pkl/%s", config.Name, config.Name))

	// PKL module name in Config.pkl (longer match first, before bare module example)
	content = strings.ReplaceAll(content, "module example.Config", fmt.Sprintf("module %s.Config", config.Name))

	// PKL bare module declaration (must come after the "module example.Config" rule)
	content = strings.ReplaceAll(content, "module example\n", fmt.Sprintf("module %s\n", config.Name))
	content = strings.ReplaceAll(content, "module example\r\n", fmt.Sprintf("module %s\r\n", config.Name))

	// PKL module-qualifier prefix (e.g. example.ExampleResource -> sftp.ExampleResource)
	content = regexp.MustCompile(`\bexample\.`).ReplaceAllString(content, config.Name+".")

	// <PluginName> placeholder in doc comments (capitalize first letter for display)
	displayName := strings.ToUpper(config.Name[:1]) + config.Name[1:]
	content = strings.ReplaceAll(content, "<PluginName>", displayName)

	return content
}
