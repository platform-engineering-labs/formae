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

	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/api"
	"github.com/platform-engineering-labs/formae/internal/cli/authmsg"
	"github.com/platform-engineering-labs/formae/internal/cli/banner"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
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

	// authClientFactory returns the auth plugin client used to obtain and,
	// on a 401, force-refresh the credential attached to outgoing API
	// requests. Nil in the real constructor, where it defaults to
	// a.AuthClient; tests inject a stub so they can drive withAuthRetry
	// without spawning a plugin subprocess.
	authClientFactory func() (authHeaderProvider, error)

	// newAPIClient constructs the API client used for a single retried
	// operation. Nil in the real constructor, where it defaults to
	// api.NewClient against a.Config.Cli.API; tests inject a stub pointed at
	// an httptest server.
	newAPIClient func(authHeader http.Header, net *http.Client) *api.Client
}

// authHeaderProvider is the subset of *pkgauth.Client withAuthRetry needs to
// obtain (and force-refresh) the header attached to outgoing API requests.
// Depending on this narrow interface, rather than the concrete client, lets
// tests exercise the retry logic against a stub with no plugin subprocess.
type authHeaderProvider interface {
	GetAuthHeader(forceRefresh bool) (*pkgauth.GetAuthHeaderResponse, error)
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

// Theme resolves the active CLI theme from config, falling back to quiet when
// config is absent. It is the single source of truth for command theming.
func (a *App) Theme() *theme.Theme {
	name := ""
	if a != nil && a.Config != nil {
		name = a.Config.Cli.Theme
	}
	return theme.New(name)
}

type Plugins struct{}

type Projects struct{}

func NewApp() *App {
	u, err := usage.NewPostHogSender()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, lipgloss.NewStyle().Foreground(theme.New("formae").Palette.Error).Render("Error: "+err.Error()))
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
		_, _ = fmt.Fprintln(os.Stderr, lipgloss.NewStyle().Foreground(theme.New("formae").Palette.Error).Render("Error: "+err.Error()))
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
						docLabelStyle().Render("Pkl documentation:"),
						"https://pkl-lang.org/main/current/language-reference/index.html",
						docLabelStyle().Render("Pkl primer:"),
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

// docLabelStyle is the shared style for doc-link / callout labels (e.g.
// "Getting started:", "Pkl documentation:", "Configuration documentation:") —
// brand orange (SecondaryAccent), consistent with the banner "Docs:" and the
// cmd help. Distinct from Warning (gold), which is reserved for genuine cautions.
func docLabelStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(theme.New("formae").Palette.SecondaryAccent)
}

// PrintBanner prints the formae banner followed by any config warnings
// (e.g. deprecation notices for the old plugins block). Call this instead
// of banner.PrintBanner() in human-readable command flows so that
// warnings are never emitted in machine-readable (JSON) output.
func (a *App) PrintBanner() {
	banner.SetTheme(a.Theme())
	banner.PrintBanner()
	if a.Config != nil && len(a.Config.Warnings) > 0 {
		th := theme.New("formae")
		goldStyle := lipgloss.NewStyle().Foreground(th.Palette.Warning)
		for _, w := range a.Config.Warnings {
			_, _ = fmt.Fprintf(os.Stderr, "%s %s\n", goldStyle.Render("Warning:"), w)
		}
		_, _ = fmt.Fprintln(os.Stderr)
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
	compatible, _, nags, err := a.runBeforeCommand(true)
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
			docLabelStyle().Render("Pkl documentation:"),
			"https://pkl-lang.org/main/current/language-reference/index.html",
			docLabelStyle().Render("Pkl primer:"),
			"https://pkl.platform.engineering",
		)
	}
	clientID, err := config.Config.ClientID()
	if err != nil {
		return nil, nil, err
	}

	// This is its own withAuthRetry closure, separate from the Stats
	// preflight above: wrapping Apply as a whole would replay the preflight
	// and re-submit the mutation on retry. The forma is []byte-marshaled
	// fresh by ApplyForma on every call, so replaying just this closure is
	// safe.
	var resp *apimodel.SubmitCommandResponse
	err = a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		r, err := client.ApplyForma(forma, mode, simulate, clientID, force)
		if err != nil {
			return err
		}
		resp = r
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return resp, nags, nil
}

func (a *App) Destroy(path string, query string, props map[string]string, simulate bool, onDependents string) (*apimodel.SubmitCommandResponse, []string, error) {
	compatible, _, nags, err := a.runBeforeCommand(true)
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
				docLabelStyle().Render("Pkl documentation:"),
				"https://pkl-lang.org/main/current/language-reference/index.html",
				docLabelStyle().Render("Pkl primer:"),
				"https://pkl.platform.engineering",
			)
		}

		err = a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
			client := a.apiClient(authHeader, net)
			r, err := client.DestroyForma(forma, simulate, onDependents, clientID)
			if err != nil {
				return err
			}
			resp = r
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	} else {
		err = a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
			client := a.apiClient(authHeader, net)
			r, err := client.DestroyByQuery(query, simulate, onDependents, clientID)
			if err != nil {
				return err
			}
			resp = r
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}

	return resp, nags, nil
}

func (a *App) CancelCommand(query string, force bool) (*apimodel.CancelCommandResponse, error) {
	compatible, _, _, err := a.runBeforeCommand(true)
	if !compatible {
		return nil, err
	}

	clientID, err := config.Config.ClientID()
	if err != nil {
		return nil, err
	}

	var res *apimodel.CancelCommandResponse
	err = a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		r, err := client.CancelCommands(query, force, clientID)
		if err != nil {
			return err
		}
		res = r
		return nil
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (a *App) GetCommandsStatus(query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error) {
	compatible, _, nags, err := a.runBeforeCommand(!fromWatch)
	if !compatible {
		return nil, nil, err
	}

	clientID, err := config.Config.ClientID()
	if err != nil {
		return nil, nil, err
	}

	var res *apimodel.ListCommandStatusResponse
	err = a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		r, err := client.GetFormaCommandsStatus(query, clientID, n)
		if err != nil {
			return err
		}
		res = r
		return nil
	})
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

func (a *App) ExtractResources(query string, fromTUI bool) (*pkgmodel.Forma, []string, error) {
	compatible, _, nags, err := a.runBeforeCommand(!fromTUI)
	if !compatible {
		return nil, nil, err
	}

	var f *pkgmodel.Forma
	err = a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		res, err := client.ExtractResources(query)
		if err != nil {
			return err
		}
		f = res
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	if f == nil {
		f = &pkgmodel.Forma{
			Targets:   []pkgmodel.Target{},
			Resources: []pkgmodel.Resource{},
		}
	}

	return f, nags, nil
}

// ListResourceSummaries fetches lightweight resource summaries from the agent,
// alongside any nag messages from the compatibility gate. Detail is fetched
// lazily by ksuid via ResourceDetailByKsuid.
func (a *App) ListResourceSummaries(query string, fromTUI bool) ([]pkgmodel.ResourceSummary, []string, error) {
	compatible, _, nags, err := a.runBeforeCommand(!fromTUI)
	if !compatible {
		return nil, nil, err
	}

	var summaries []pkgmodel.ResourceSummary
	err = a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		s, err := client.ListResourceSummaries(query)
		if err != nil {
			return err
		}
		summaries = s
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if summaries == nil {
		summaries = []pkgmodel.ResourceSummary{}
	}
	return summaries, nags, nil
}

// ResourceDetailByKsuid fetches a single resource by its ksuid from the agent.
// Returns (nil, nags, nil) when the agent reports no resource for the ksuid.
func (a *App) ResourceDetailByKsuid(ksuid string, fromTUI bool) (*pkgmodel.Resource, []string, error) {
	compatible, _, nags, err := a.runBeforeCommand(!fromTUI)
	if !compatible {
		return nil, nil, err
	}

	var resource *pkgmodel.Resource
	err = a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		r, err := client.GetResourceByKsuid(ksuid)
		if err != nil {
			return err
		}
		resource = r
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return resource, nags, nil
}

func (a *App) ForceSync() error {
	if compatible, _, _, err := a.runBeforeCommand(true); !compatible {
		return err
	}

	return a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		return client.ForceSync()
	})
}

func (a *App) ForceDiscover() error {
	if compatible, _, _, err := a.runBeforeCommand(true); !compatible {
		return err
	}

	return a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		return client.ForceDiscover()
	})
}

func (a *App) InstallPlugins(req apimodel.InstallPluginsRequest) (*apimodel.InstallPluginsResponse, error) {
	if compatible, _, _, err := a.runBeforeCommand(true); !compatible {
		return nil, err
	}

	var resp *apimodel.InstallPluginsResponse
	err := a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		r, err := client.InstallPlugins(req)
		if err != nil {
			return err
		}
		resp = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (a *App) UninstallPlugins(req apimodel.UninstallPluginsRequest) (*apimodel.UninstallPluginsResponse, error) {
	if compatible, _, _, err := a.runBeforeCommand(true); !compatible {
		return nil, err
	}

	var resp *apimodel.UninstallPluginsResponse
	err := a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		r, err := client.UninstallPlugins(req)
		if err != nil {
			return err
		}
		resp = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
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
	var resp *apimodel.ListPluginsResponse
	err := a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		r, err := client.ListPlugins("installed", "", "", "", "")
		if err != nil {
			return err
		}
		resp = r
		return nil
	})
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

func (a *App) UpdatePlugins(req apimodel.UpdatePluginsRequest) (*apimodel.UpdatePluginsResponse, error) {
	if compatible, _, _, err := a.runBeforeCommand(true); !compatible {
		return nil, err
	}

	var resp *apimodel.UpdatePluginsResponse
	err := a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		r, err := client.UpdatePlugins(req)
		if err != nil {
			return err
		}
		resp = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// Preflight verifies the agent is reachable and version-compatible before an
// interactive (TUI) command takes over the screen, so connection, auth, and
// version-mismatch errors surface as ordinary CLI errors instead of being
// rendered inside the alt-screen TUI. transmitStats is false so a preflight
// check doesn't double-report usage stats.
func (a *App) Preflight() error {
	if compatible, _, _, err := a.runBeforeCommand(false); !compatible {
		return err
	}
	return nil
}

func (a *App) Stats() (*apimodel.Stats, []string, error) {
	compatible, stats, nags, err := a.runBeforeCommand(true)
	if !compatible {
		return nil, nil, err
	}
	return stats, nags, nil
}

// runBeforeCommand fetches Stats as its own withAuthRetry closure — the one
// enumerated conversion point that every command runs before its own work —
// then checks version compatibility and reports usage. It no longer takes a
// pre-built client: retrying Stats independently means a stale credential
// caught here does not also poison whichever client the caller goes on to
// build for its own operation.
func (a *App) runBeforeCommand(transmitStats bool) (bool, *apimodel.Stats, []string, error) {
	var stats *apimodel.Stats
	err := a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		s, err := client.Stats()
		if err != nil {
			return err
		}
		stats = s
		return nil
	})
	if err != nil {
		th := theme.New("formae")
		goldStyle := lipgloss.NewStyle().Foreground(th.Palette.Warning)
		errStyle := lipgloss.NewStyle().Foreground(th.Palette.Error)
		if errors.Is(err, syscall.ECONNREFUSED) {
			return false, nil, nil, fmt.Errorf("agent is not running; please start the agent and try again\n\n%s %s", docLabelStyle().Render("Getting started:"), banner.DocRoot)
		}
		var denied api.AuthorizationDeniedError
		if errors.As(err, &denied) {
			return false, nil, nil, denied
		}
		if errors.Is(err, api.AuthenticationError{}) {
			return false, nil, nil, fmt.Errorf("%s\n\n%s",
				errStyle.Render("authentication failed"),
				goldStyle.Render("Check your cli.auth and agent.auth configuration."))
		}
		return false, nil, nil, fmt.Errorf("error fetching stats from agent: %v", err)
	}

	if stats.Version != formae.Version {
		return false, nil, nil, fmt.Errorf("incompatible agent version: expected %s, got %s\n\n%s %s", formae.Version, stats.Version, docLabelStyle().Render("Configuration documentation:"), banner.DocRoot)
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
		th := theme.New("formae")
		nags = append(nags, fmt.Sprintf("You have %d unmanaged resource%s. You can extract them using %s, adjust and apply the changes.", totalUnmanaged, plural, lipgloss.NewStyle().Foreground(th.Palette.PrimaryAccent).Render("formae extract --query='managed:false'")))
	}

	return nags
}

// NoAuthPluginError indicates the active profile carries no cli.auth block,
// so there is no auth plugin to discover or start.
type NoAuthPluginError struct{}

func (*NoAuthPluginError) Error() string {
	return "no auth plugin configured for the active profile"
}

// AuthClient returns the App's auth plugin client, discovering and starting
// the plugin subprocess on first use and caching it for subsequent calls.
// It returns a *NoAuthPluginError when the active profile has no cli.auth
// block, so callers that need a plugin outright — such as `formae login` —
// can fail with a clear message instead of dereferencing a nil client.
func (a *App) AuthClient() (*pkgauth.Client, error) {
	if a.Config.Cli.Auth == nil {
		return nil, &NoAuthPluginError{}
	}

	if a.authClient == nil {
		authType := gjson.GetBytes(a.Config.Cli.Auth, "type").String()
		devPluginDir := util.ExpandHomePath(a.Config.PluginDir)
		binPath, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("failed to determine binary path: %w", err)
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
			return nil, fmt.Errorf("auth plugin %q not installed", authType)
		}
		client, err := pkgauth.NewClient(matched.BinaryPath, a.Config.Cli.Auth)
		if err != nil {
			return nil, fmt.Errorf("failed to start auth plugin: %w", err)
		}
		a.authClient = client
	}

	return a.authClient, nil
}

// authProvider returns the auth plugin client used to obtain the request
// header, preferring the injectable authClientFactory (set by tests) and
// falling back to the real AuthClient.
func (a *App) authProvider() (authHeaderProvider, error) {
	if a.authClientFactory != nil {
		return a.authClientFactory()
	}
	return a.AuthClient()
}

// apiClient constructs an API client for a single operation from the given
// auth header and network client, preferring the injectable newAPIClient
// (set by tests) and falling back to api.NewClient against a.Config.Cli.API.
func (a *App) apiClient(authHeader http.Header, net *http.Client) *api.Client {
	if a.newAPIClient != nil {
		return a.newAPIClient(authHeader, net)
	}
	return api.NewClient(a.Config.Cli.API, authHeader, net)
}

// withAuthRetry runs op once with the current auth header. When op fails
// with api.AuthenticationError and an auth plugin is configured, it asks the
// plugin to force-refresh the credential and retries op exactly once with
// the refreshed header — recovering from a credential that looks fresh to
// the CLI but is actually stale (e.g. a backward clock jump masking an
// expired token, or a token signed by a just-revoked key) without treating a
// genuine denial as a transient error. A non-auth error, or an auth error
// with no auth plugin configured, is returned unchanged with no retry.
//
// op must perform exactly one HTTP operation with a replayable, []byte-backed
// request body — never a whole command (e.g. a preflight followed by a
// mutating submission). Retrying a multi-step sequence would replay every
// step, which for a mutation means submitting it twice.
func (a *App) withAuthRetry(op func(authHeader http.Header, net *http.Client) error) error {
	authHeader, net, err := a.getAuthAndNetHandlers()
	if err != nil {
		return err
	}

	err = op(authHeader, net)
	if err == nil {
		return nil
	}

	var authErr api.AuthenticationError
	if !errors.As(err, &authErr) || a.Config.Cli.Auth == nil {
		return err
	}

	provider, provErr := a.authProvider()
	if provErr != nil {
		return err
	}

	resp, headerErr := provider.GetAuthHeader(true)
	if headerErr != nil {
		return err
	}
	if resp.ErrorCode != "" || resp.Error != "" {
		return errors.New(authmsg.DescribeAuthError(resp.ErrorCode, resp.Error))
	}

	err = op(http.Header(resp.Headers), net)
	if err == nil {
		return nil
	}
	if errors.As(err, &authErr) {
		return api.AuthorizationDeniedError{}
	}
	return err
}

func (a *App) getAuthAndNetHandlers() (http.Header, *http.Client, error) {
	var authHeader http.Header
	var net *http.Client

	if a.Config.Cli.Auth != nil {
		client, err := a.authProvider()
		if err != nil {
			return nil, nil, err
		}

		resp, err := client.GetAuthHeader(false)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get auth header: %w", err)
		}
		if resp.ErrorCode != "" || resp.Error != "" {
			return nil, nil, errors.New(authmsg.DescribeAuthError(resp.ErrorCode, resp.Error))
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
			docLabelStyle().Render("Pkl documentation:"),
			"https://pkl-lang.org/main/current/language-reference/index.html",
			docLabelStyle().Render("Pkl primer:"),
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

	// Plain data renders (json, yaml) don't need the agent's installed-plugin
	// list — they only walk the forma struct. Skip the agent round-trip so
	// `formae eval --output-consumer machine --output-schema json` works
	// without an agent (used by conformance discovery tests where eval runs
	// before the agent is up).
	if options.Schema == "pkl" {
		deps, err := a.buildDependencyStrings(forma, options.SchemaLocation)
		if err != nil {
			return "", err
		}
		options.Dependencies = deps
	}
	if options.SchemaLocation == "" {
		options.SchemaLocation = schema.SchemaLocationRemote
	}

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
			return nil, fmt.Errorf("resource type %q requires plugin namespace %q, but the agent does not report it installed. Install it with `formae plugin install %s` and retry", r.Type, ns, ns)
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

func (a *App) ExtractTargets(query string, fromTUI bool) ([]*pkgmodel.Target, []string, error) {
	compatible, _, nags, err := a.runBeforeCommand(!fromTUI)
	if !compatible {
		return nil, nil, err
	}

	var targets []*pkgmodel.Target
	err = a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		t, err := client.ListTargets(query)
		if err != nil {
			return err
		}
		targets = t
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	if targets == nil {
		targets = []*pkgmodel.Target{}
	}

	return targets, nags, nil
}

func (a *App) ExtractStacks(fromTUI bool) ([]*pkgmodel.Stack, []string, error) {
	compatible, _, nags, err := a.runBeforeCommand(!fromTUI)
	if !compatible {
		return nil, nil, err
	}

	var stacks []*pkgmodel.Stack
	err = a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		s, err := client.ListStacks()
		if err != nil {
			return err
		}
		stacks = s
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	if stacks == nil {
		stacks = []*pkgmodel.Stack{}
	}

	return stacks, nags, nil
}

func (a *App) ExtractPolicies(fromTUI bool) ([]apimodel.PolicyInventoryItem, []string, error) {
	compatible, _, nags, err := a.runBeforeCommand(!fromTUI)
	if !compatible {
		return nil, nil, err
	}

	var policies []apimodel.PolicyInventoryItem
	err = a.withAuthRetry(func(authHeader http.Header, net *http.Client) error {
		client := a.apiClient(authHeader, net)
		p, err := client.ListPolicies()
		if err != nil {
			return err
		}
		policies = p
		return nil
	})
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
