// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package app

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/api"
	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/usage"
	"github.com/platform-engineering-labs/formae/internal/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

type App struct {
	PluginManager *plugin.Manager

	Config *pkgmodel.Config

	Plugins  Plugins
	Projects Projects

	Usage usage.Sender
}

type Plugins struct {
	pluginManager *plugin.Manager
}

type Projects struct {
	pluginManager *plugin.Manager
}

func NewApp() *App {
	mgr := plugin.NewManager(util.ExpandHomePath("~/.pel/formae/plugins"))
	u, err := usage.NewPostHogSender()
	if err != nil {
		fmt.Println(display.Red("Error: " + err.Error()))
		os.Exit(1)
	}

	app := &App{
		PluginManager: mgr,
		Config:        &pkgmodel.Config{},
		Plugins:       Plugins{mgr},
		Projects:      Projects{mgr},
		Usage:         u,
	}

	app.PluginManager.Load()

	err = config.Config.EnsureClientID()
	if err != nil {
		fmt.Println(display.Red("Error: " + err.Error()))
		os.Exit(1)
	}

	return app
}

func (a *App) LoadConfig(path string, configPathPrefix string) error {
	// If complete path is provided attempt to load config and fail if not found
	if path != "" {
		contentType := filepath.Ext(path)

		schemaPlugin, err := a.PluginManager.SchemaPluginByFileExtension(contentType)
		if err != nil {
			return err
		}

		a.Config, err = (*schemaPlugin).FormaeConfig(path)
		if err != nil {
			return fmt.Errorf("failed to load configuration from '%s': %s", path, err.Error())
		}

		// Config loaded successfully from provided path, don't look for other configs
		return nil
	}

	// Check for supported types first wins
	for _, fileExtension := range a.PluginManager.SupportedFileExtensions() {
		schemaPlugin, err := a.PluginManager.SchemaPluginByFileExtension(fileExtension)
		if err != nil {
			return err
		}

		a.Config, err = (*schemaPlugin).FormaeConfig(util.ExpandHomePath(configPathPrefix + fileExtension))
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
	schemaPlugin, err := a.PluginManager.SchemaPluginByFileExtension(".pkl")
	if err != nil {
		return err
	}

	a.Config, err = (*schemaPlugin).FormaeConfig("")
	if err != nil {
		return err
	}

	return nil
}

func (a *App) SupportedOutputSchemas() []string {
	supported := []string{}

	for _, schemaName := range a.PluginManager.SupportedSchemas() {
		schemaPlugin, err := a.PluginManager.SchemaPlugin(schemaName)
		if err == nil && (*schemaPlugin).SupportsExtract() {
			supported = append(supported, schemaName)
		}
	}

	return supported
}

func (a *App) IsSupportedOutputSchema(contentType string) bool {
	schemaPlugin, err := a.PluginManager.SchemaPlugin(contentType)
	if err != nil {
		return false
	}

	return (*schemaPlugin).SupportsExtract()
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
	schemaPlugin, err := a.PluginManager.SchemaPluginByFileExtension(contentType)
	if err != nil {
		return nil, nil, err
	}
	forma, err := (*schemaPlugin).Evaluate(path, pkgmodel.CommandApply, mode, props)
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
		schemaPlugin, err := a.PluginManager.SchemaPluginByFileExtension(contentType)
		if err != nil {
			return nil, nil, err
		}

		forma, err := (*schemaPlugin).Evaluate(path, pkgmodel.CommandDestroy, pkgmodel.FormaApplyModeReconcile, props)
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
		} else {
			return false, nil, nil, fmt.Errorf("error fetching stats from agent: %v", err)
		}
	}

	if stats.Version != formae.Version {
		return false, nil, nil, fmt.Errorf("incompatible agent version: expected %s, got %s\n\n%s %s%s", formae.Version, stats.Version, display.Gold("Configuration documentation:"), display.DocRoot, "operations")
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
	var auth http.Header
	var net *http.Client

	if a.Config.Plugins.Authentication != nil {
		authPlugin, err := a.PluginManager.AuthPlugin(a.Config.Plugins.Authentication)
		if err != nil {
			return nil, nil, err
		}

		auth, err = (*authPlugin).Authorization(a.Config.Plugins.Authentication)
		if err != nil {
			return nil, nil, err
		}
	}

	if a.Config.Plugins.Network != nil {
		netPlugin, err := a.PluginManager.NetworkPlugin(a.Config.Plugins.Network)
		if err != nil {
			return nil, nil, err
		}

		net, err = (*netPlugin).Client(a.Config.Plugins.Network)
		if err != nil {
			return nil, nil, err
		}
	}

	return auth, net, nil
}

func (a *App) Evaluate(path string, props map[string]string, mode pkgmodel.FormaApplyMode) (*pkgmodel.Forma, error) {
	contentType := filepath.Ext(path)

	schemaPlugin, err := a.PluginManager.SchemaPluginByFileExtension(contentType)
	if err != nil {
		return nil, err
	}

	forma, err := (*schemaPlugin).Evaluate(path, pkgmodel.CommandEval, mode, props)
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

func (a *App) Serialize(forma *pkgmodel.Resource, options *plugin.SerializeOptions) (string, error) {
	schemaPlugin, err := a.PluginManager.SchemaPlugin(options.Schema)
	if err != nil {
		return "", err
	}

	return (*schemaPlugin).Serialize(forma, options)
}

func (a *App) SerializeForma(forma *pkgmodel.Forma, options *plugin.SerializeOptions) (string, error) {
	schemaPlugin, err := a.PluginManager.SchemaPlugin(options.Schema)
	if err != nil {
		return "", err
	}

	return (*schemaPlugin).SerializeForma(forma, options)
}

func (a *App) GenerateSourceCode(forma *pkgmodel.Forma, targetPath string, outputSchema string) (plugin.GenerateSourcesResult, error) {
	schemaPlugin, err := a.PluginManager.SchemaPlugin(outputSchema)
	if err != nil {
		return plugin.GenerateSourcesResult{}, err
	}
	includes := a.Projects.formatIncludes(outputSchema, []string{"aws"})

	return (*schemaPlugin).GenerateSourceCode(forma, targetPath, includes)
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

// Plugins

func (p *Plugins) List() []*plugin.Plugin {
	return p.pluginManager.List()
}

func (p *Plugins) SupportedSchemas() []string {
	return p.pluginManager.SupportedSchemas()
}

// Projects

func (p *Projects) Init(path string, format string, include []string) error {
	// TODO(discount-elf) think about this namespace issue, since different packages can be included in plugins we currently
	// need plugin.package for download delivery
	var includes []string

	switch format {
	case "pkl":
		includes = p.formatIncludes(format, include)

		schemaPlugin, err := p.pluginManager.SchemaPluginByFileExtension(".pkl")
		if err != nil {
			return err
		}

		err = (*schemaPlugin).ProjectInit(path, includes)
		if err != nil {
			return err
		}

	default:
		return fmt.Errorf("format not yet supported: %s", format)
	}

	return nil
}

func (p *Projects) formatIncludes(format string, include []string) []string {
	var includes []string
	switch format {
	case "pkl":
		if pklVersion := p.pluginManager.PluginVersion("pkl"); pklVersion != nil {
			includes = append(includes, "pkl.formae@"+pklVersion.String())
		}

		if slices.Contains(include, "aws") {
			if awsVersion := p.pluginManager.PluginVersion("aws"); awsVersion != nil {
				includes = append(includes, "aws.aws@"+awsVersion.String())
			}
		}
	default:
		return []string{}
	}

	return includes
}

func (p *Projects) Properties(path string) (map[string]pkgmodel.Prop, error) {
	contentType := filepath.Ext(path)

	schemaPlugin, err := p.pluginManager.SchemaPluginByFileExtension(contentType)
	if err != nil {
		return nil, err
	}

	return (*schemaPlugin).ProjectProperties(path)
}
