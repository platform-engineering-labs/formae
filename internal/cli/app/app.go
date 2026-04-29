// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/api"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/network"
	_ "github.com/platform-engineering-labs/formae/internal/network/all"
	"github.com/platform-engineering-labs/formae/internal/schema"
	_ "github.com/platform-engineering-labs/formae/internal/schema/all"
	"github.com/platform-engineering-labs/formae/internal/usage"
	"github.com/platform-engineering-labs/formae/internal/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/discovery"
	"github.com/tidwall/gjson"
)

type App struct {
	Config *pkgmodel.Config

	Plugins  Plugins
	Projects Projects

	Usage usage.Sender

	authClient *pkgauth.Client
}

// Close cleans up resources held by the App, including any auth plugin subprocess.
func (a *App) Close() {
	if a.authClient != nil {
		_ = a.authClient.Close()
	}
}

// NewClient creates a new API client using the App's configuration,
// auth, and network settings.
func (a *App) NewClient() (*api.Client, error) {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return nil, err
	}
	return api.NewClient(a.Config.Cli.API, auth, net), nil
}

type Plugins struct{}

type Projects struct{}

func NewApp() *App {
	u, err := usage.NewPostHogSender()
	if err != nil {
		fmt.Fprintln(os.Stderr, display.Red("Error: "+err.Error()))
		os.Exit(1)
	}

	app := &App{
		// Default PluginDir matches the PKL Config.pkl default so that CLI
		// commands invoked without --config still get sane plugin discovery.
		// LoadConfig overwrites this when a config file is present.
		Config:   &pkgmodel.Config{PluginDir: "~/.pel/formae/plugins"},
		Plugins:  Plugins{},
		Projects: Projects{},
		Usage:    u,
	}

	err = config.Config.EnsureClientID()
	if err != nil {
		fmt.Fprintln(os.Stderr, display.Red("Error: "+err.Error()))
		os.Exit(1)
	}

	return app
}

func (a *App) LoadConfig(path string, configPathPrefix string) error {
	// If complete path is provided attempt to load config and fail if not found
	if path != "" {
		contentType := filepath.Ext(path)

		schemaPlugin, err := schema.DefaultRegistry.GetByFileExtension(contentType)
		if err != nil {
			return err
		}

		a.Config, err = schemaPlugin.FormaeConfig(path)
		if err != nil {
			return fmt.Errorf("failed to load configuration from '%s': %s", path, err.Error())
		}

		// Config loaded successfully from provided path, don't look for other configs
		return nil
	}

	// Check for supported types first wins
	for _, fileExtension := range schema.DefaultRegistry.SupportedFileExtensions() {
		schemaPlugin, err := schema.DefaultRegistry.GetByFileExtension(fileExtension)
		if err != nil {
			return err
		}

		a.Config, err = schemaPlugin.FormaeConfig(util.ExpandHomePath(configPathPrefix + fileExtension))
		if err != nil {
			if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not supported") {
				continue
			} else {
				// As soon as we start supporting multiple configuration formats we need to move the
				// helpful links to the plugin.
				if strings.ToLower(fileExtension) == ".pkl" {
					return fmt.Errorf("%w\n%s %s\n%s %s",
						err,
						display.Gold("Pkl documentation:"),
						"https://pkl-lang.org/main/current/language-reference/index.html",
						display.Gold("Pkl primer:"),
						"https://pkl.platform.engineering",
					)
				}

				return err
			}
		} else {
			return nil
		}
	}

	// No config file found get the default from pkl
	schemaPlugin, err := schema.DefaultRegistry.GetByFileExtension(".pkl")
	if err != nil {
		return err
	}

	a.Config, err = schemaPlugin.FormaeConfig("")
	if err != nil {
		return err
	}

	return nil
}

// PrintBanner prints the formae banner followed by any config warnings
// (e.g. deprecation notices for the old plugins block). Call this instead
// of display.PrintBanner() in human-readable command flows so that
// warnings are never emitted in machine-readable (JSON) output.
func (a *App) PrintBanner() {
	display.PrintBanner()
	if a.Config != nil && len(a.Config.Warnings) > 0 {
		for _, w := range a.Config.Warnings {
			fmt.Fprintf(os.Stderr, "%s %s\n", display.Gold("Warning:"), w)
		}
		fmt.Fprintln(os.Stderr)
	}
}

func (a *App) SupportedOutputSchemas() []string {
	supported := []string{}

	for _, schemaName := range schema.DefaultRegistry.SupportedSchemas() {
		schemaPlugin, err := schema.DefaultRegistry.Get(schemaName)
		if err == nil && schemaPlugin.SupportsExtract() {
			supported = append(supported, schemaName)
		}
	}

	return supported
}

func (a *App) IsSupportedOutputSchema(contentType string) bool {
	schemaPlugin, err := schema.DefaultRegistry.Get(contentType)
	if err != nil {
		return false
	}

	return schemaPlugin.SupportsExtract()
}

func (a *App) Apply(path string, props map[string]string, mode pkgmodel.FormaApplyMode, simulate bool, force bool) (*apimodel.SubmitCommandResponse, []string, error) {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return nil, nil, err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	compatible, _, nags, err := a.runBeforeCommand(client, true)
	if !compatible {
		return nil, nil, err
	}
	contentType := filepath.Ext(path)
	schemaPlugin, err := schema.DefaultRegistry.GetByFileExtension(contentType)
	if err != nil {
		return nil, nil, err
	}
	forma, err := schemaPlugin.Evaluate(path, pkgmodel.CommandApply, mode, props)
	if err != nil {
		return nil, nil, fmt.Errorf("%w\n%s %s\n%s %s",
			err,
			display.Gold("Pkl documentation:"),
			"https://pkl-lang.org/main/current/language-reference/index.html",
			display.Gold("Pkl primer:"),
			"https://pkl.platform.engineering",
		)
	}
	clientID, err := config.Config.ClientID()
	if err != nil {
		return nil, nil, err
	}
	resp, err := client.ApplyForma(forma, mode, simulate, clientID, force)
	if err != nil {
		return nil, nil, err
	}

	return resp, nags, nil
}

func (a *App) Destroy(path string, query string, props map[string]string, simulate bool) (*apimodel.SubmitCommandResponse, []string, error) {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return nil, nil, err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	compatible, _, nags, err := a.runBeforeCommand(client, true)
	if !compatible {
		return nil, nil, err
	}
	clientID, err := config.Config.ClientID()
	if err != nil {
		return nil, nil, err
	}
	var resp *apimodel.SubmitCommandResponse
	if path != "" {
		contentType := filepath.Ext(path)
		schemaPlugin, err := schema.DefaultRegistry.GetByFileExtension(contentType)
		if err != nil {
			return nil, nil, err
		}

		forma, err := schemaPlugin.Evaluate(path, pkgmodel.CommandDestroy, pkgmodel.FormaApplyModeReconcile, props)
		if err != nil {
			return nil, nil, fmt.Errorf("%w\n%s %s\n%s %s",
				err,
				display.Gold("Pkl documentation:"),
				"https://pkl-lang.org/main/current/language-reference/index.html",
				display.Gold("Pkl primer:"),
				"https://pkl.platform.engineering",
			)
		}

		resp, err = client.DestroyForma(forma, simulate, clientID)
		if err != nil {
			return nil, nil, err
		}
	} else {
		resp, err = client.DestroyByQuery(query, simulate, clientID)
		if err != nil {
			return nil, nil, err
		}
	}

	return resp, nags, nil
}

func (a *App) CancelCommand(query string) (*apimodel.CancelCommandResponse, error) {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return nil, err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	compatible, _, _, err := a.runBeforeCommand(client, true)
	if !compatible {
		return nil, err
	}

	clientID, err := config.Config.ClientID()
	if err != nil {
		return nil, err
	}

	res, err := client.CancelCommands(query, clientID)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (a *App) GetCommandsStatus(query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return nil, nil, err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	compatible, _, nags, err := a.runBeforeCommand(client, !fromWatch)
	if !compatible {
		return nil, nil, err
	}

	clientID, err := config.Config.ClientID()
	if err != nil {
		return nil, nil, err
	}

	res, err := client.GetFormaCommandsStatus(query, clientID, n)
	if err != nil {
		return nil, nil, err
	}

	if res == nil {
		res = &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{},
		}
	}

	return res, nags, nil
}

func (a *App) ExtractResources(query string) (*pkgmodel.Forma, []string, error) {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return nil, nil, err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	compatible, _, nags, err := a.runBeforeCommand(client, true)
	if !compatible {
		return nil, nil, err
	}

	f, err := client.ExtractResources(query)

	if err != nil {
		return nil, nil, err
	}

	if f == nil {
		f = &pkgmodel.Forma{
			Targets:   []pkgmodel.Target{},
			Resources: []pkgmodel.Resource{},
		}
	}

	return f, nags, err
}

func (a *App) ForceSync() error {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	if compatible, _, _, err := a.runBeforeCommand(client, true); !compatible {
		return err
	}

	return client.ForceSync()
}

func (a *App) ForceDiscover() error {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	if compatible, _, _, err := a.runBeforeCommand(client, true); !compatible {
		return err
	}

	return client.ForceDiscover()
}

func (a *App) InstallPlugins(req apimodel.InstallPluginsRequest) (*apimodel.InstallPluginsResponse, error) {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return nil, err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	if compatible, _, _, err := a.runBeforeCommand(client, true); !compatible {
		return nil, err
	}

	return client.InstallPlugins(req)
}

func (a *App) UninstallPlugins(req apimodel.UninstallPluginsRequest) (*apimodel.UninstallPluginsResponse, error) {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return nil, err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	if compatible, _, _, err := a.runBeforeCommand(client, true); !compatible {
		return nil, err
	}

	return client.UninstallPlugins(req)
}

// InstalledResourcePluginVersions queries the agent for installed resource
// plugins and returns a map of lowercase namespace to installed version. Used
// by `formae extract` and `formae project init` to pin remote schema URIs
// without scanning local plugin directories — orbital-installed plugins live
// on the agent box, not on the CLI box, so the local-scan approach broke for
// any deployment where agent and CLI are separate.
func (a *App) InstalledResourcePluginVersions() (map[string]string, error) {
	plugins, err := a.installedResourcePlugins()
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(plugins))
	for ns, info := range plugins {
		if info.Version != "" {
			result[ns] = info.Version
		}
	}
	return result, nil
}

// PluginInfo is a CLI-side view of an installed plugin, combining the
// agent-reported version with its on-disk PklProject location (when the
// agent and CLI share a filesystem). Used by the --schema-location local
// flow to build local PKL import strings.
type PluginInfo struct {
	Version   string
	LocalPath string
}

// InstalledResourcePlugins returns the agent's view of installed
// resource plugins, keyed by lowercase namespace (falling back to
// lowercase name when namespace is empty). Includes both version and
// the agent-reported on-disk PklProject path so callers can pick
// local vs remote URI emission.
func (a *App) InstalledResourcePlugins() (map[string]PluginInfo, error) {
	return a.installedResourcePlugins()
}

func (a *App) installedResourcePlugins() (map[string]PluginInfo, error) {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return nil, err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	resp, err := client.ListPlugins("installed", "", "", "", "")
	if err != nil {
		return nil, err
	}

	result := make(map[string]PluginInfo, len(resp.Plugins))
	for _, p := range resp.Plugins {
		if p.Type != "resource" {
			continue
		}
		key := strings.ToLower(p.Namespace)
		if key == "" {
			key = strings.ToLower(p.Name)
		}
		result[key] = PluginInfo{
			Version:   p.InstalledVersion,
			LocalPath: p.LocalPath,
		}
	}
	return result, nil
}

func (a *App) UpgradePlugins(req apimodel.UpgradePluginsRequest) (*apimodel.UpgradePluginsResponse, error) {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return nil, err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	if compatible, _, _, err := a.runBeforeCommand(client, true); !compatible {
		return nil, err
	}

	return client.UpgradePlugins(req)
}

func (a *App) Stats() (*apimodel.Stats, []string, error) {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return nil, nil, err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	if compatible, stats, nags, err := a.runBeforeCommand(client, true); !compatible {
		return nil, nil, err
	} else {
		if err == syscall.ECONNREFUSED {
			return nil, nil, fmt.Errorf("agent is not running; please start the agent and try again\n\n%s %s", display.Gold("Getting started:"), display.DocRoot)

		} else if err != nil {
			return nil, nil, fmt.Errorf("error fetching stats from agent: %v", err)
		}

		return stats, nags, nil
	}
}

func (a *App) runBeforeCommand(client *api.Client, transmitStats bool) (bool, *apimodel.Stats, []string, error) {
	stats, err := client.Stats()
	if err != nil {
		if err == syscall.ECONNREFUSED {
			return false, nil, nil, fmt.Errorf("agent is not running; please start the agent and try again\n\n%s %s", display.Gold("Getting started:"), display.DocRoot)
		}
		if errors.Is(err, api.AuthenticationError{}) {
			return false, nil, nil, fmt.Errorf("%s\n\n%s",
				display.Red("authentication failed"),
				display.Gold("Check your cli.auth and agent.auth configuration."))
		}
		return false, nil, nil, fmt.Errorf("error fetching stats from agent: %v", err)
	}

	if stats.Version != formae.Version {
		return false, nil, nil, fmt.Errorf("incompatible agent version: expected %s, got %s\n\n%s %s", formae.Version, stats.Version, display.Gold("Configuration documentation:"), display.DocRoot)
	}

	if transmitStats && !a.Config.Cli.DisableUsageReporting {
		_ = a.Usage.SendStats(stats, !strings.HasSuffix(os.Args[0], "formae"))
	}

	return true, stats, a.calculateNags(stats), nil
}

func sumMapValues(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}

func (a *App) calculateNags(stats *apimodel.Stats) []string {
	nags := []string{}
	totalUnmanaged := sumMapValues(stats.UnmanagedResources)
	if totalUnmanaged > 0 {
		plural := "s"
		if totalUnmanaged == 1 {
			plural = ""
		}
		nags = append(nags, fmt.Sprintf("You have %d unmanaged resource%s. You can extract them using %s, adjust and apply the changes.", totalUnmanaged, plural, display.LightBlue("formae extract --query='managed:false'")))
	}

	return nags
}

func (a *App) getAuthAndNetHandlers() (http.Header, *http.Client, error) {
	var authHeader http.Header
	var net *http.Client

	if a.Config.Cli.Auth != nil {
		if a.authClient == nil {
			authType := gjson.GetBytes(a.Config.Cli.Auth, "type").String()
			devPluginDir := util.ExpandHomePath(a.Config.PluginDir)
			binPath, err := os.Executable()
			if err != nil {
				return nil, nil, fmt.Errorf("failed to determine binary path: %w", err)
			}
			systemPluginDir := discovery.SystemPluginDir(binPath)
			authPlugins := discovery.DiscoverPluginsMulti(
				[]string{devPluginDir, systemPluginDir}, discovery.Auth,
			)
			var matched *discovery.PluginInfo
			for i, p := range authPlugins {
				if p.Name == authType {
					matched = &authPlugins[i]
					break
				}
			}
			if matched == nil {
				return nil, nil, fmt.Errorf("auth plugin %q not installed", authType)
			}
			client, err := pkgauth.NewClient(matched.BinaryPath, a.Config.Cli.Auth)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to start auth plugin: %w", err)
			}
			a.authClient = client
		}

		resp, err := a.authClient.GetAuthHeader()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get auth header: %w", err)
		}
		authHeader = http.Header(resp.Headers)
	}

	if a.Config.Network != nil {
		netPlugin, err := network.DefaultRegistry.Get(a.Config.Network.Type)
		if err != nil {
			return nil, nil, err
		}

		var configJSON []byte
		if len(a.Config.Network.LegacyRawJSON) > 0 {
			configJSON = a.Config.Network.LegacyRawJSON
		} else {
			var marshalErr error
			configJSON, marshalErr = json.Marshal(a.Config.Network.Tailscale)
			if marshalErr != nil {
				return nil, nil, fmt.Errorf("failed to marshal network config: %w", marshalErr)
			}
		}

		net, err = netPlugin.Client(configJSON)
		if err != nil {
			return nil, nil, err
		}
	}

	return authHeader, net, nil
}

func (a *App) Evaluate(path string, props map[string]string, mode pkgmodel.FormaApplyMode) (*pkgmodel.Forma, error) {
	contentType := filepath.Ext(path)

	schemaPlugin, err := schema.DefaultRegistry.GetByFileExtension(contentType)
	if err != nil {
		return nil, err
	}

	forma, err := schemaPlugin.Evaluate(path, pkgmodel.CommandEval, mode, props)
	if err != nil {
		return nil, fmt.Errorf("%w\n%s %s\n%s %s",
			err,
			display.Gold("Pkl documentation:"),
			"https://pkl-lang.org/main/current/language-reference/index.html",
			display.Gold("Pkl primer:"),
			"https://pkl.platform.engineering",
		)
	}

	return forma, nil
}

func (a *App) SerializeForma(forma *pkgmodel.Forma, options *schema.SerializeOptions) (string, error) {
	schemaPlugin, err := schema.DefaultRegistry.Get(options.Schema)
	if err != nil {
		return "", err
	}

	deps, err := a.buildDependencyStrings(forma, options.SchemaLocation)
	if err != nil {
		return "", err
	}
	if options.SchemaLocation == "" {
		options.SchemaLocation = schema.SchemaLocationRemote
	}
	options.Dependencies = deps

	return schemaPlugin.SerializeForma(forma, options)
}

func (a *App) GenerateSourceCode(forma *pkgmodel.Forma, targetPath string, outputSchema string, schemaLocation schema.SchemaLocation) (schema.GenerateSourcesResult, error) {
	schemaPlugin, err := schema.DefaultRegistry.Get(outputSchema)
	if err != nil {
		return schema.GenerateSourcesResult{}, err
	}

	deps, err := a.buildDependencyStrings(forma, schemaLocation)
	if err != nil {
		return schema.GenerateSourcesResult{}, err
	}
	if schemaLocation == "" {
		schemaLocation = schema.SchemaLocationRemote
	}

	options := &schema.SerializeOptions{
		Schema:         outputSchema,
		SchemaLocation: schemaLocation,
		Dependencies:   deps,
	}
	return schemaPlugin.GenerateSourceCode(forma, targetPath, nil, options)
}

// buildDependencyStrings asks the agent for installed plugin info and
// emits PklProjectTemplate-formatted dep strings for every namespace
// present in the forma, plus formae core.
//
// SchemaLocationRemote (default) emits `<plugin>.<name>@<version>` strings;
// PKL fetches these from hub.platform.engineering. SchemaLocationLocal
// emits `local:<name>:<path>` strings pointing at the agent's on-disk
// PklProject; PKL imports them directly. Formae core is always remote
// (the agent does not surface its own PKL schema as a local path).
//
// SchemaLocationLocal requires the CLI and agent to share a filesystem.
// Each agent-reported localPath is statted; the first unreadable path
// (or first plugin missing from the agent's local view entirely) fails
// the call with a clear error pointing the operator at the same-box
// constraint.
func (a *App) buildDependencyStrings(forma *pkgmodel.Forma, location schema.SchemaLocation) ([]string, error) {
	plugins, err := a.InstalledResourcePlugins()
	if err != nil {
		return nil, fmt.Errorf("listing installed plugins: %w", err)
	}

	var deps []string
	if formae.Version != "0.0.0" {
		deps = append(deps, "pkl.formae@"+formae.Version)
	}

	seen := make(map[string]bool)
	for _, r := range forma.Resources {
		ns := strings.ToLower(r.Namespace())
		if ns == "" || seen[ns] {
			continue
		}
		seen[ns] = true

		info, ok := plugins[ns]
		if !ok || info.Version == "" {
			continue
		}

		if location == schema.SchemaLocationLocal {
			if info.LocalPath == "" {
				return nil, fmt.Errorf("--schema-location local requires plugin %q to be installed on the agent's local filesystem; the agent reports no on-disk path. Install with `formae plugin install %s` and retry, or omit --schema-location to use remote schemas", ns, ns)
			}
			if _, statErr := os.Stat(info.LocalPath); statErr != nil {
				return nil, fmt.Errorf("--schema-location local requires the CLI and agent to share a filesystem; the agent reports plugin %q at %s but that path is not readable from the CLI host (%v). Run the CLI on the agent's host, or omit --schema-location to use remote schemas", ns, info.LocalPath, statErr)
			}
			deps = append(deps, fmt.Sprintf("local:%s:%s", ns, info.LocalPath))
		} else {
			deps = append(deps, fmt.Sprintf("%s.%s@%s", ns, ns, info.Version))
		}
	}

	sort.Strings(deps)
	return deps, nil
}

func (a *App) ExtractTargets(query string) ([]*pkgmodel.Target, []string, error) {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return nil, nil, err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	compatible, _, nags, err := a.runBeforeCommand(client, true)
	if !compatible {
		return nil, nil, err
	}

	targets, err := client.ListTargets(query)
	if err != nil {
		return nil, nil, err
	}

	if targets == nil {
		targets = []*pkgmodel.Target{}
	}

	return targets, nags, nil
}

func (a *App) ExtractStacks() ([]*pkgmodel.Stack, []string, error) {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return nil, nil, err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	compatible, _, nags, err := a.runBeforeCommand(client, true)
	if !compatible {
		return nil, nil, err
	}

	stacks, err := client.ListStacks()
	if err != nil {
		return nil, nil, err
	}

	if stacks == nil {
		stacks = []*pkgmodel.Stack{}
	}

	return stacks, nags, nil
}

func (a *App) ExtractPolicies() ([]apimodel.PolicyInventoryItem, []string, error) {
	auth, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return nil, nil, err
	}
	client := api.NewClient(a.Config.Cli.API, auth, net)

	compatible, _, nags, err := a.runBeforeCommand(client, true)
	if !compatible {
		return nil, nil, err
	}

	policies, err := client.ListPolicies()
	if err != nil {
		return nil, nil, err
	}

	if policies == nil {
		policies = []apimodel.PolicyInventoryItem{}
	}

	return policies, nags, nil
}

// Plugins

func (p *Plugins) SupportedSchemas() []string {
	return schema.DefaultRegistry.SupportedSchemas()
}

// Projects

func (p *Projects) Init(path string, format string, include []string, pluginsDir string, installedVersions map[string]string) error {
	// TODO(discount-elf) think about this namespace issue, since different packages can be included in plugins we currently
	// need plugin.package for download delivery
	switch format {
	case "pkl":
		includes, err := p.formatIncludes(format, include, pluginsDir, installedVersions)
		if err != nil {
			return err
		}

		// Determine schema location: if all packages are local, use local; otherwise remote
		// The PKL plugin will run 'pkl project resolve' only for remote packages
		location := schema.SchemaLocationRemote
		allLocal := true
		for _, inc := range includes {
			if !strings.HasPrefix(inc, "local:") {
				allLocal = false
				break
			}
		}
		if allLocal {
			location = schema.SchemaLocationLocal
		}

		schemaPlugin, err := schema.DefaultRegistry.GetByFileExtension(".pkl")
		if err != nil {
			return err
		}

		err = schemaPlugin.ProjectInit(path, includes, location)
		if err != nil {
			return err
		}

	default:
		return fmt.Errorf("format not yet supported: %s", format)
	}

	return nil
}

func (p *Projects) formatIncludes(format string, include []string, pluginsDir string, installedVersions map[string]string) ([]string, error) {
	var includes []string
	switch format {
	case "pkl":
		// Formae core PKL package version matches the formae binary version.
		if formae.Version != "0.0.0" {
			includes = append(includes, "pkl.formae@"+formae.Version)
		}

		// Add included packages
		for _, inc := range include {
			ns, isLocal := parseIncludeSpec(inc)

			// @local: must resolve locally — pluginsDir is the dev plugin
			// install dir (typically ~/.pel/formae/plugins, populated by
			// `make install` in plugin repos).
			if isLocal {
				localPath, _ := p.findInstalledPlugin(ns, pluginsDir)
				if localPath == "" {
					return nil, fmt.Errorf("plugin %q not installed locally for @local resolution. Install it from a plugin repo with `make install`", ns)
				}
				includes = append(includes, fmt.Sprintf("local:%s:%s", ns, localPath))
				continue
			}

			// Default: resolve from hub (remote). Version comes from the
			// agent's installed-plugins view rather than scanning local
			// disk, since orbital-installed plugins live with the agent
			// and may not be present on the CLI box.
			version, ok := installedVersions[ns]
			if !ok || version == "" {
				return nil, fmt.Errorf("plugin %q not installed on the agent. Install it with: formae plugin install %s", ns, ns)
			}
			includes = append(includes, fmt.Sprintf("%s.%s@%s", ns, ns, version))
		}
	default:
		return nil, nil
	}

	return includes, nil
}

// parseIncludeSpec parses an include specification and returns the namespace and whether it should resolve locally.
// Format: "namespace" for remote or "namespace@local" for local resolution.
// The parsing is case-insensitive for the @local suffix.
func parseIncludeSpec(include string) (namespace string, isLocal bool) {
	include = strings.ToLower(include)
	if strings.HasSuffix(include, "@local") {
		return strings.TrimSuffix(include, "@local"), true
	}
	return include, false
}

// findInstalledPlugin looks for an installed plugin at pluginsDir/<namespace>/v*/schema/pkl/PklProject.
// It performs case-insensitive directory lookup.
// Returns (schemaPath, version) where schemaPath is the path to PklProject (empty if no schema),
// and version is the highest installed version (empty if plugin not installed).
func (p *Projects) findInstalledPlugin(namespace, pluginsDir string) (schemaPath string, version string) {
	if pluginsDir == "" {
		return "", ""
	}

	// Case-insensitive lookup: list plugins dir and find matching name
	pluginEntries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return "", ""
	}

	var pluginDir string
	nsLower := strings.ToLower(namespace)
	for _, entry := range pluginEntries {
		if entry.IsDir() && strings.ToLower(entry.Name()) == nsLower {
			pluginDir = filepath.Join(pluginsDir, entry.Name())
			break
		}
	}

	if pluginDir == "" {
		return "", ""
	}

	// Find version directories
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return "", ""
	}

	// Collect version directories
	var versions []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Version directories start with 'v' (e.g., v0.1.0)
		if strings.HasPrefix(name, "v") {
			versions = append(versions, name)
		}
	}

	if len(versions) == 0 {
		return "", ""
	}

	// Sort by version string descending (highest first)
	sort.Slice(versions, func(i, j int) bool {
		return versions[i] > versions[j]
	})

	// Use highest version
	highestVersion := versions[0]
	// Strip the 'v' prefix for the version string
	version = strings.TrimPrefix(highestVersion, "v")

	// Check if schema exists
	pklProjectPath := filepath.Join(pluginDir, highestVersion, "schema", "pkl", "PklProject")
	if _, err := os.Stat(pklProjectPath); err == nil {
		schemaPath = pklProjectPath
	}

	return schemaPath, version
}

func (p *Projects) Properties(path string) (map[string]pkgmodel.Prop, error) {
	contentType := filepath.Ext(path)

	schemaPlugin, err := schema.DefaultRegistry.GetByFileExtension(contentType)
	if err != nil {
		return nil, err
	}

	return schemaPlugin.ProjectProperties(path)
}
