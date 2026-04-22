// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	pklgo "github.com/apple/pkl-go/pkl"
	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/schema"
	pklmodel "github.com/platform-engineering-labs/formae/internal/schema/pkl/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const ProjectFile = "PklProject"

type PKL struct{}

//go:embed assets
var assets embed.FS

//go:embed generator/*
var generator embed.FS

// Compile time check to satisfy protocol
var _ schema.SchemaPlugin = PKL{}

func init() {
	schema.DefaultRegistry.Register(PKL{})
}

// bundledPklCommand returns the sibling pkl binary next to the formae executable,
// or nil to let pkl-go fall back to PATH. Using PATH risks picking up a pkl
// version that doesn't support stdlib features our schemas rely on (e.g.
// `pkl.reflect.Property.allAnnotations` needs 0.31+).
func bundledPklCommand() []string {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	// Resolve symlinks so /usr/local/bin/formae -> /opt/pel/bin/formae finds
	// /opt/pel/bin/pkl rather than looking in /usr/local/bin.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	bundled := filepath.Join(filepath.Dir(exe), "pkl")
	if info, err := os.Stat(bundled); err == nil && !info.IsDir() {
		return []string{bundled}
	}
	return nil
}

func (p PKL) Name() string {
	return "pkl"
}

func (p PKL) FileExtension() string {
	return ".pkl"
}

func (p PKL) SupportsExtract() bool {
	return true
}

func (p PKL) FormaeConfig(path string) (*pkgmodel.Config, error) {
	formaeFs, err := fs.Sub(assets, "assets/formae")
	if err != nil {
		return nil, err
	}

	var configSource *pklgo.ModuleSource
	if path == "" {
		configSource = pklgo.TextSource(`amends "formae:/Config.pkl"`)
	} else {
		if FileExists(path) {
			configSource = pklgo.FileSource(path)
		} else {
			return nil, fmt.Errorf("config file: %s does not exist", path)
		}
	}

	// Build evaluator options: always include formae:/ scheme
	opts := []func(*pklgo.EvaluatorOptions){
		pklgo.WithFs(formaeFs, "formae"),
		pklgo.WithResourceReader(libExtension{}),
		pklgo.PreconfiguredOptions,
	}

	// If the plugin directory exists, generate wrappers and mount as plugins:/ scheme
	pluginDir := defaultPluginDir()
	if pluginDir != "" {
		if _, statErr := os.Stat(pluginDir); statErr == nil {
			if wrapErr := GeneratePluginWrappers(pluginDir); wrapErr != nil {
				slog.Warn("failed to generate plugin wrappers", "error", wrapErr)
			} else {
				opts = append(opts, pklgo.WithFs(os.DirFS(pluginDir), "plugins"))
			}
		}
	}

	var evaluator pklgo.Evaluator

	// Check if there's a PklProject in the directory tree
	var projectDir string
	if path != "" {
		projectDir = WalkForProjectFile(filepath.Dir(path))
	}

	var cleanup func()
	if projectDir != "" {
		evaluator, cleanup, err = newSafeProjectEvaluator(
			context.Background(),
			&url.URL{Scheme: "file", Path: projectDir},
			opts...,
		)
	} else {
		if cmd := bundledPklCommand(); cmd != nil {
			evaluator, err = pklgo.NewEvaluatorWithCommand(context.Background(), cmd, opts...)
		} else {
			evaluator, err = pklgo.NewEvaluator(context.Background(), opts...)
		}
		cleanup = func() { _ = evaluator.Close() }
	}

	if err != nil {
		return nil, err
	}

	defer cleanup()

	var config *pklmodel.Config
	err = evaluator.EvaluateModule(context.Background(), configSource, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate PKL configuration file '%s': %w", path, err)
	}

	return translateConfig(config), nil
}

func translateConfig(config *pklmodel.Config) *pkgmodel.Config {
	translated := pkgmodel.Config{
		Agent: pkgmodel.AgentConfig{
			Server: pkgmodel.ServerConfig{
				Nodename:      config.Agent.Server.Nodename,
				Hostname:      config.Agent.Server.Hostname,
				Port:          int(config.Agent.Server.Port),
				ErgoPort:      int(config.Agent.Server.ErgoPort),
				RegistrarPort: int(config.Agent.Server.RegistrarPort),
				Secret:        config.Agent.Server.Secret,
				ObserverPort:  int(config.Agent.Server.ObserverPort),
				TLSCert:       config.Agent.Server.TLSCert,
				TLSKey:        config.Agent.Server.TLSKey,
			},
			Datastore: pkgmodel.DatastoreConfig{
				DatastoreType: config.Agent.Datastore.DatastoreType,
				Sqlite: pkgmodel.SqliteConfig{
					FilePath: config.Agent.Datastore.Sqlite.FilePath,
				},
				Postgres: pkgmodel.PostgresConfig{
					Host:             config.Agent.Datastore.Postgres.Host,
					Port:             int(config.Agent.Datastore.Postgres.Port),
					User:             config.Agent.Datastore.Postgres.User,
					Password:         config.Agent.Datastore.Postgres.Password,
					Database:         config.Agent.Datastore.Postgres.Database,
					Schema:           config.Agent.Datastore.Postgres.Schema,
					ConnectionParams: config.Agent.Datastore.Postgres.ConnectionParams,
				},
				AuroraDataAPI: pkgmodel.AuroraDataAPIConfig{
					ClusterARN: config.Agent.Datastore.AuroraDataAPI.ClusterArn,
					SecretARN:  config.Agent.Datastore.AuroraDataAPI.SecretArn,
					Database:   config.Agent.Datastore.AuroraDataAPI.Database,
					Region:     config.Agent.Datastore.AuroraDataAPI.Region,
				},
			},
			Retry: translateRetryConfig(config.Agent.Retry),
			Synchronization: pkgmodel.SynchronizationConfig{
				Enabled:  config.Agent.Synchronization.Enabled,
				Interval: config.Agent.Synchronization.Interval.GoDuration(),
			},
			Discovery: pkgmodel.DiscoveryConfig{
				Enabled:                 config.Agent.Discovery.Enabled,
				Interval:                config.Agent.Discovery.Interval.GoDuration(),
				LabelTagKeys:            config.Agent.Discovery.LabelTagKeys,
				ResourceTypesToDiscover: config.Agent.Discovery.ResourceTypesToDiscover,
			},
			Logging: pkgmodel.LoggingConfig{
				FilePath:        config.Agent.Logging.FilePath,
				FileLogLevel:    parseLogLevel(config.Agent.Logging.FileLogLevel),
				ConsoleLogLevel: parseLogLevel(config.Agent.Logging.ConsoleLogLevel),
			},
			StackExpirer: pkgmodel.StackExpirerConfig{
				Disabled: !config.Agent.StackExpirer.Enabled,
				Interval: config.Agent.StackExpirer.Interval.GoDuration(),
			},
			OTel: pkgmodel.OTelConfig{
				Enabled:     config.Agent.OTel.Enabled,
				ServiceName: config.Agent.OTel.ServiceName,
				OTLP: pkgmodel.OTLPConfig{
					Enabled:     config.Agent.OTel.OTLP.Enabled,
					Endpoint:    config.Agent.OTel.OTLP.Endpoint,
					Protocol:    config.Agent.OTel.OTLP.Protocol,
					Insecure:    config.Agent.OTel.OTLP.Insecure,
					Temporality: config.Agent.OTel.OTLP.Temporality,
				},
				Prometheus: pkgmodel.PrometheusConfig{
					Enabled: config.Agent.OTel.Prometheus.Enabled,
				},
			},
			Auth:            translateAuthConfig(&config.Agent.Auth),
			ResourcePlugins: translateResourcePluginConfigs(config.Agent.ResourcePlugins),
		},
		Artifacts: translateArtifactConfig(&config.Artifacts),
		Cli: pkgmodel.CliConfig{
			API: pkgmodel.APIConfig{
				URL:  config.Cli.API.URL,
				Port: int(config.Cli.API.Port),
			},
			DisableUsageReporting: config.Cli.DisableUsageReporting,
			Auth:                  translateAuthConfig(&config.Cli.Auth),
		},
	}

	translated.PluginDir = config.PluginDir
	translated.Network = translateNetworkConfig(config.Network)

	// Backwards compatibility: fall back to deprecated plugins block
	applyDeprecatedPluginsConfig(config.Plugins, &translated)

	// Warn when global settings conflict with per-plugin overrides
	checkResourcePluginDeprecations(&translated)

	// Synthesize Repositories from legacy flat fields and emit deprecation warnings
	emitArtifactDeprecationWarnings(&translated)

	return &translated
}

// emitArtifactDeprecationWarnings synthesizes a canonical Repositories entry from
// the legacy flat URL field and appends deprecation warnings to translated.Warnings.
func emitArtifactDeprecationWarnings(translated *pkgmodel.Config) {
	a := &translated.Artifacts
	// When the user config uses the legacy flat fields and hasn't migrated
	// to repositories, synthesize a single binary repository entry and warn.
	if a.URL.String() != "" && len(a.Repositories) == 0 {
		a.Repositories = []pkgmodel.Repository{
			{URI: a.URL, Type: pkgmodel.RepositoryTypeBinary},
		}
		w := "artifacts.url is deprecated; migrate to artifacts.repositories. The URL has been loaded as a 'binary' repository for this release."
		slog.Warn(w)
		translated.Warnings = append(translated.Warnings, w)
	}
	if a.Username != "" {
		w := "artifacts.username is deprecated; repository credentials will be per-repo in a future release"
		slog.Warn(w)
		translated.Warnings = append(translated.Warnings, w)
	}
	if a.Password != "" {
		w := "artifacts.password is deprecated; repository credentials will be per-repo in a future release"
		slog.Warn(w)
		translated.Warnings = append(translated.Warnings, w)
	}
}

// checkResourcePluginDeprecations warns when global settings conflict with per-plugin overrides.
func checkResourcePluginDeprecations(translated *pkgmodel.Config) {
	if len(translated.Agent.ResourcePlugins) == 0 {
		return
	}

	hasPerPluginRetry := false
	hasPerPluginRTD := false

	for _, rpc := range translated.Agent.ResourcePlugins {
		if rpc.Retry != nil {
			hasPerPluginRetry = true
		}
		if len(rpc.ResourceTypesToDiscover) > 0 {
			hasPerPluginRTD = true
		}
	}

	// Default RetryConfig values as defined in Config.pkl
	defaultRetry := pkgmodel.RetryConfig{
		StatusCheckInterval: 20 * time.Second,
		MaxRetries:          9,
		RetryDelay:          10 * time.Second,
	}

	if hasPerPluginRetry && translated.Agent.Retry != defaultRetry {
		w := "Your configuration file uses per-plugin 'retry' — the global 'agent.retry' is deprecated in favor of per-plugin retry config"
		slog.Warn(w)
		translated.Warnings = append(translated.Warnings, w)
	}

	if hasPerPluginRTD && len(translated.Agent.Discovery.ResourceTypesToDiscover) > 0 {
		w := "Your configuration file uses per-plugin 'resourceTypesToDiscover' — the global 'agent.discovery.resourceTypesToDiscover' is deprecated in favor of per-plugin config"
		slog.Warn(w)
		translated.Warnings = append(translated.Warnings, w)
	}
}

// applyDeprecatedPluginsConfig copies values from the deprecated plugins block
// to their new locations, emitting deprecation warnings. New paths take precedence.
// Warnings are collected in translated.Warnings so callers (CLI) can display them.
func applyDeprecatedPluginsConfig(plugins *pklmodel.PluginConfig, translated *pkgmodel.Config) {
	if plugins == nil {
		return
	}

	if plugins.PluginDir != "" && plugins.PluginDir != "~/.pel/formae/plugins" && translated.PluginDir == "~/.pel/formae/plugins" {
		w := "Your configuration file uses deprecated 'plugins.pluginDir' — migrate to top-level 'pluginDir'"
		slog.Warn(w)
		translated.Warnings = append(translated.Warnings, w)
		translated.PluginDir = plugins.PluginDir
	}

	if plugins.Authentication != nil && translated.Agent.Auth == nil {
		w := "Your configuration file uses deprecated 'plugins.authentication' — migrate to 'agent.auth' and 'cli.auth'"
		slog.Warn(w)
		translated.Warnings = append(translated.Warnings, w)
		authJSON := translateDynamic(plugins.Authentication)
		translated.Agent.Auth = authJSON
		translated.Cli.Auth = authJSON
	}

	if plugins.Network != nil && translated.Network == nil {
		w := "Your configuration file uses deprecated 'plugins.network' — migrate to top-level 'network'"
		slog.Warn(w)
		translated.Warnings = append(translated.Warnings, w)
		// The old format was a Dynamic blob. Convert to typed NetworkConfig
		// by extracting the type field and passing the rest as-is via JSON.
		networkJSON := translateDynamic(plugins.Network)
		translated.Network = translateLegacyNetworkConfig(networkJSON)
	}
}

// translateLegacyNetworkConfig converts the old plugins.network json.RawMessage
// to a typed NetworkConfig. The old format was a flat JSON object with a Type field
// and all config properties mixed in. The new format nests provider config.
func translateLegacyNetworkConfig(networkJSON json.RawMessage) *pkgmodel.NetworkConfig {
	if networkJSON == nil {
		return nil
	}
	// For backwards compat, just store the raw JSON. The server's configureNetwork
	// handles both typed NetworkConfig and legacy raw JSON via the network registry.
	var raw map[string]any
	if err := json.Unmarshal(networkJSON, &raw); err != nil {
		return nil
	}
	netType, _ := raw["type"].(string)
	return &pkgmodel.NetworkConfig{
		Type:          netType,
		LegacyRawJSON: networkJSON,
	}
}

func translateNetworkConfig(nc *pklmodel.NetworkConfig) *pkgmodel.NetworkConfig {
	if nc == nil {
		return nil
	}
	result := &pkgmodel.NetworkConfig{Type: nc.Type}
	if nc.Tailscale != nil {
		result.Tailscale = &pkgmodel.TailscaleConfig{
			TLS:           nc.Tailscale.TLS,
			AuthKey:       nc.Tailscale.AuthKey,
			Hostname:      nc.Tailscale.Hostname,
			AdvertiseTags: nc.Tailscale.AdvertiseTags,
		}
	}
	return result
}

func (p PKL) Evaluate(path string, cmd pkgmodel.Command, mode pkgmodel.FormaApplyMode, props map[string]string) (*pkgmodel.Forma, error) {
	var evaluator pklgo.Evaluator
	var err error

	addSchemaContextProperties(cmd, mode, props)

	projectDir := WalkForProjectFile(filepath.Dir(path))

	var cleanup func()
	if projectDir != "" {
		evaluator, cleanup, err = newSafeProjectEvaluator(
			context.Background(),
			&url.URL{Scheme: "file", Path: projectDir},
			pklgo.PreconfiguredOptions,
			pklgo.WithResourceReader(libExtension{}),
			func(opts *pklgo.EvaluatorOptions) {
				opts.Properties = props
				opts.OutputFormat = "json"
			})

		if err != nil {
			return nil, err
		}
	} else {
		evalOpts := []func(*pklgo.EvaluatorOptions){
			pklgo.PreconfiguredOptions,
			pklgo.WithResourceReader(libExtension{}),
			func(opts *pklgo.EvaluatorOptions) {
				opts.Properties = props
				opts.OutputFormat = "json"
			},
		}
		if pklCmd := bundledPklCommand(); pklCmd != nil {
			evaluator, err = pklgo.NewEvaluatorWithCommand(context.Background(), pklCmd, evalOpts...)
		} else {
			evaluator, err = pklgo.NewEvaluator(context.Background(), evalOpts...)
		}
		cleanup = func() { _ = evaluator.Close() }

		if err != nil {
			return nil, err
		}
	}

	defer cleanup()

	// Render PKL
	result, err := evaluator.EvaluateOutputText(context.Background(), pklgo.FileSource(path))
	if err != nil {
		return nil, err
	}

	// Marshal to struct
	var forma *pkgmodel.Forma
	err = json.Unmarshal([]byte(result), &forma)
	if err != nil {
		return nil, err
	}

	return forma, nil
}

func (p PKL) GenerateSourceCode(forma *pkgmodel.Forma, path string, includes []string, schemaLocation schema.SchemaLocation) (schema.GenerateSourcesResult, error) {
	res := schema.GenerateSourcesResult{}

	code, err := p.SerializeForma(forma, &schema.SerializeOptions{Schema: "pkl", SchemaLocation: schemaLocation})
	if err != nil {
		slog.Error(err.Error())
		return schema.GenerateSourcesResult{}, schema.ErrFailedToGenerateSources
	}
	res.ResourceCount = len(forma.Resources)

	// add .pkl to path if not present
	if !strings.HasSuffix(path, ".pkl") {
		path = path + ".pkl"
	}
	res.TargetPath = path

	// Ensure parent directory exists
	parentDir := filepath.Dir(path)
	if err = os.MkdirAll(parentDir, 0755); err != nil {
		return schema.GenerateSourcesResult{}, fmt.Errorf("failed to create parent directory: %v", err)
	}

	projectFile := filepath.Join(parentDir, "PklProject")
	if _, err = os.Stat(projectFile); os.IsNotExist(err) {
		// Build package dependencies using PackageResolver
		resolver := NewPackageResolver()

		// Configure local schema resolution if requested
		if schemaLocation == schema.SchemaLocationLocal {
			homeDir, err := os.UserHomeDir()
			if err == nil {
				pluginsDir := filepath.Join(homeDir, ".pel", "formae", "plugins")
				resolver.WithLocalSchemas(pluginsDir)
			}
		}

		resolver.Add("formae", "pkl", formae.Version)

		// Extract namespaces from forma resources
		for _, res := range forma.Resources {
			ns := strings.ToLower(res.Namespace())
			resolver.Add(ns, ns, resolver.InstalledVersion(ns))
		}

		// No PklProject exists, initialize it with resolved packages
		err = p.ProjectInit(parentDir, resolver.GetPackageStrings(), schemaLocation)
		if err != nil {
			return schema.GenerateSourcesResult{}, fmt.Errorf("failed to initialize Pkl project: %v", err)
		}
		res.InitializedNewProject = true
		res.ProjectPath = parentDir
		fmt.Println("Initialized new Pkl project at", parentDir)
	}

	err = os.WriteFile(path, []byte(code), 0644)
	if err != nil {
		return schema.GenerateSourcesResult{}, fmt.Errorf("failed to write Pkl file: %v", err)
	}

	// Check if PklProject.deps.json exists after writing the Pkl file
	// Only warn for remote schema location since local schemas don't need resolution
	depsFile := filepath.Join(parentDir, "PklProject.deps.json")
	if _, err := os.Stat(depsFile); os.IsNotExist(err) && schemaLocation == schema.SchemaLocationRemote {
		res.Warnings = append(res.Warnings, fmt.Sprintf("Pkl dependencies not resolved. Run 'pkl project resolve' in '%s' to resolve Pkl dependencies.", parentDir))
	}

	return res, nil
}

func (p PKL) ProjectInit(path string, include []string, schemaLocation schema.SchemaLocation) error {
	var err error

	if path == "" {
		path, err = os.Getwd()
		if err != nil {
			return err
		}
	} else {
		path, err = filepath.Abs(path)
		if err != nil {
			return err
		}
	}

	if stat, err := os.Stat(path); os.IsNotExist(err) {
		err = os.Mkdir(path, 0750)
		if err != nil {
			return err
		}
	} else {
		if !stat.IsDir() {
			return fmt.Errorf("%s is not a directory", path)
		}
	}

	projOpts := []func(*pklgo.EvaluatorOptions){
		pklgo.PreconfiguredOptions,
		pklgo.WithFs(assets, "assets"),
		func(opts *pklgo.EvaluatorOptions) {
			opts.Properties = map[string]string{
				"packages": strings.Join(include, ","),
			}
		},
	}
	var evaluator pklgo.Evaluator
	if pklCmd := bundledPklCommand(); pklCmd != nil {
		evaluator, err = pklgo.NewEvaluatorWithCommand(context.Background(), pklCmd, projOpts...)
	} else {
		evaluator, err = pklgo.NewEvaluator(context.Background(), projOpts...)
	}
	if err != nil {
		return err
	}

	projectFile, err := evaluator.EvaluateOutputText(context.Background(), pklgo.UriSource("assets:/assets/PklProjectTemplate.pkl"))
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(path, "PklProject"), []byte(projectFile), 0640)
	if err != nil {
		return err
	}

	entryFile, err := fs.ReadFile(assets, "assets/EntryPoint.pkl")
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(path, "main.pkl"), entryFile, 0640)
	if err != nil {
		return err
	}

	// Run pkl project resolve if there are any remote packages
	// Local packages use import() syntax and don't need resolution
	hasRemotePackages := false
	for _, pkg := range include {
		if !strings.HasPrefix(pkg, "local:") {
			hasRemotePackages = true
			break
		}
	}

	if hasRemotePackages {
		cmd := exec.Command("pkl", "project", "resolve", path)
		if errors.Is(cmd.Err, exec.ErrDot) {
			cmd.Err = nil
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("project resolve failed: %w\nOutput: %s", err, string(output))
		}
	}

	return nil
}

func (p PKL) ProjectProperties(path string) (map[string]pkgmodel.Prop, error) {
	forma, err := p.Evaluate(path, pkgmodel.CommandEval, pkgmodel.FormaApplyModeProperties, make(map[string]string))
	if err != nil {
		return nil, err
	}

	return forma.Properties, nil
}

func (p PKL) SerializeForma(forma *pkgmodel.Forma, options *schema.SerializeOptions) (string, error) {
	return p.serializeWithPKL(forma, options)
}

func sanitizeConfig(v any) any {
	return sanitizeValue(reflect.ValueOf(v))
}

func sanitizeValue(rv reflect.Value) any {
	if !rv.IsValid() {
		return nil
	}
	switch rv.Kind() {
	case reflect.Interface, reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		return sanitizeValue(rv.Elem())

	case reflect.Map:
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[fmt.Sprintf("%v", iter.Key().Interface())] = sanitizeValue(iter.Value())
		}
		return out

	case reflect.Array, reflect.Slice:
		n := rv.Len()
		out := make([]any, n)
		for i := range n {
			out[i] = sanitizeValue(rv.Index(i))
		}
		return out

	case reflect.Struct:
		// pkl.Object instances (from Dynamic or typed objects decoded as
		// pkl.Object) are flattened to their properties/elements so the
		// resulting JSON matches what plugins expect. Without this the
		// raw pkl.Object struct fields (ModuleUri, Name, Properties, etc.)
		// would leak into the JSON.
		if obj, ok := rv.Interface().(pklgo.Object); ok {
			if len(obj.Elements) > 0 {
				return sanitizeValue(reflect.ValueOf(obj.Elements))
			}
			return sanitizeConfig(obj.Properties)
		}

		out := make(map[string]any, rv.NumField())
		rt := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			f := rt.Field(i)
			if f.PkgPath != "" {
				continue
			}
			out[f.Name] = sanitizeValue(rv.Field(i))
		}
		return out

	default:
		return rv.Interface()
	}
}

// translateAuthConfig converts a PKL auth config object to json.RawMessage.
// Returns nil when the auth field is unset (zero-value pkl.Object with no properties).
func translateAuthConfig(obj *pklgo.Object) json.RawMessage {
	if obj == nil || len(obj.Properties) == 0 {
		return nil
	}
	return translateDynamic(obj)
}

func translateDynamic(dyn *pklgo.Object) json.RawMessage {
	if dyn == nil {
		return nil
	}
	safe := sanitizeConfig(dyn.Properties)
	configJson, err := json.Marshal(safe)
	if err != nil {
		slog.Error("Failed to marshal target config", "error", err)
	}

	return configJson
}

func translateRetryConfig(rc *pklmodel.RetryConfig) pkgmodel.RetryConfig {
	if rc == nil {
		return pkgmodel.RetryConfig{}
	}
	return pkgmodel.RetryConfig{
		StatusCheckInterval: rc.StatusCheckInterval.GoDuration(),
		MaxRetries:          int(rc.MaxRetries),
		RetryDelay:          rc.RetryDelay.GoDuration(),
	}
}

func translateArtifactConfig(ac *pklmodel.ArtifactConfig) pkgmodel.ArtifactConfig {
	result := pkgmodel.ArtifactConfig{
		URL:      ac.URL,
		Username: ac.Username,
		Password: ac.Password,
	}
	for _, r := range ac.Repositories {
		result.Repositories = append(result.Repositories, pkgmodel.Repository{
			URI:  r.URI,
			Type: pkgmodel.RepositoryType(r.Type),
		})
	}
	return result
}

func translateResourcePluginConfigs(objects []pklgo.Object) []pkgmodel.ResourcePluginUserConfig {
	if len(objects) == 0 {
		return nil
	}
	var configs []pkgmodel.ResourcePluginUserConfig
	for i := range objects {
		configs = append(configs, translateResourcePluginConfig(&objects[i]))
	}
	return configs
}

func translateResourcePluginConfig(obj *pklgo.Object) pkgmodel.ResourcePluginUserConfig {
	props := obj.Properties

	cfg := pkgmodel.ResourcePluginUserConfig{
		Type:    pklString(props, "type"),
		Enabled: pklBool(props, "enabled", true),
	}

	// rateLimit
	if rl, ok := props["rateLimit"]; ok && rl != nil {
		if rlObj, ok := rl.(*pklmodel.RateLimitConfig); ok {
			cfg.RateLimit = &pkgmodel.RateLimitConfig{
				Scope:                            pkgmodel.RateLimitScope(rlObj.Scope),
				MaxRequestsPerSecondForNamespace: int(rlObj.MaxRequestsPerSecondForNamespace),
			}
		}
	}

	// labelConfig
	if lc, ok := props["labelConfig"]; ok && lc != nil {
		if lcObj, ok := lc.(*pklmodel.LabelConfig); ok {
			cfg.LabelConfig = &pkgmodel.LabelConfig{
				DefaultQuery:      lcObj.DefaultQuery,
				ResourceOverrides: pklMappingToStringMap(lcObj.ResourceOverrides),
			}
		}
	}

	// discoveryFilters
	if df, ok := props["discoveryFilters"]; ok && df != nil {
		cfg.DiscoveryFilters = translateDiscoveryFilters(df)
	}

	// resourceTypesToDiscover
	if rtd, ok := props["resourceTypesToDiscover"]; ok && rtd != nil {
		cfg.ResourceTypesToDiscover = pklListingToStringSlice(rtd)
	}

	// retry
	if r, ok := props["retry"]; ok && r != nil {
		if rObj, ok := r.(*pklmodel.RetryConfig); ok {
			cfg.Retry = &pkgmodel.RetryConfig{
				StatusCheckInterval: rObj.StatusCheckInterval.GoDuration(),
				MaxRetries:          int(rObj.MaxRetries),
				RetryDelay:          rObj.RetryDelay.GoDuration(),
			}
		}
	}

	// Marshal remaining unknown properties into PluginConfig for plugin-specific fields
	baseKeys := map[string]bool{
		"type": true, "enabled": true, "rateLimit": true, "labelConfig": true,
		"discoveryFilters": true, "resourceTypesToDiscover": true, "retry": true,
	}
	extra := make(map[string]any)
	for k, v := range props {
		if !baseKeys[k] {
			extra[k] = v
		}
	}
	if len(extra) > 0 {
		cfg.PluginConfig, _ = json.Marshal(sanitizeConfig(extra))
	}

	return cfg
}

func translateDiscoveryFilters(v any) []pkgmodel.MatchFilter {
	switch filters := v.(type) {
	case []any:
		var result []pkgmodel.MatchFilter
		for _, f := range filters {
			if mf, ok := f.(*pklmodel.MatchFilter); ok {
				result = append(result, translateMatchFilter(mf))
			}
		}
		return result
	default:
		return nil
	}
}

func translateMatchFilter(mf *pklmodel.MatchFilter) pkgmodel.MatchFilter {
	result := pkgmodel.MatchFilter{
		ResourceTypes: mf.ResourceTypes,
	}
	for _, c := range mf.Conditions {
		result.Conditions = append(result.Conditions, pkgmodel.FilterCondition{
			PropertyPath:  c.PropertyPath,
			PropertyValue: c.PropertyValue,
		})
	}
	return result
}

// pklString extracts a string from a pkl.Object properties map.
func pklString(props map[string]any, key string) string {
	if v, ok := props[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// pklBool extracts a bool from a pkl.Object properties map with a default.
func pklBool(props map[string]any, key string, defaultVal bool) bool {
	if v, ok := props[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return defaultVal
}

// pklListingToStringSlice converts a PKL Listing value (typically []any) to []string.
func pklListingToStringSlice(v any) []string {
	switch val := v.(type) {
	case []any:
		var result []string
		for _, item := range val {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case []string:
		return val
	default:
		return nil
	}
}

// pklMappingToStringMap converts a PKL Mapping (pkl.Object with Entries) to map[string]string.
func pklMappingToStringMap(obj *pklgo.Object) map[string]string {
	if obj == nil || len(obj.Entries) == 0 {
		return nil
	}
	result := make(map[string]string, len(obj.Entries))
	for k, v := range obj.Entries {
		if ks, ok := k.(string); ok {
			if vs, ok := v.(string); ok {
				result[ks] = vs
			}
		}
	}
	return result
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// newSafeProjectEvaluator creates a project-aware PKL evaluator without the race
// condition in pkl-go's NewProjectEvaluator. That function internally creates two
// evaluators on the same manager and defer-closes the first one. If the pkl subprocess
// sends a late message for the closed evaluator, the manager's listen loop exits
// entirely (calls return instead of continue), killing all message processing.
// See: https://github.com/apple/pkl-go/blob/v0.12.0/pkl/evaluator_exec.go#L57-L84
//
// This function keeps both evaluators alive until the returned cleanup function is
// called, which closes the entire manager.
func newSafeProjectEvaluator(ctx context.Context, projectBaseURL *url.URL, opts ...func(*pklgo.EvaluatorOptions)) (pklgo.Evaluator, func(), error) {
	var manager pklgo.EvaluatorManager
	if cmd := bundledPklCommand(); cmd != nil {
		manager = pklgo.NewEvaluatorManagerWithCommand(cmd)
	} else {
		manager = pklgo.NewEvaluatorManager()
	}

	projectEvaluator, err := manager.NewEvaluator(ctx, opts...)
	if err != nil {
		_ = manager.Close()
		return nil, nil, fmt.Errorf("failed to create project evaluator: %w", err)
	}

	projectPath := projectBaseURL.JoinPath("PklProject")
	project, err := pklgo.LoadProjectFromEvaluator(ctx, projectEvaluator, &pklgo.ModuleSource{Uri: projectPath})
	if err != nil {
		_ = manager.Close()
		return nil, nil, fmt.Errorf("failed to load project: %w", err)
	}

	newOpts := []func(*pklgo.EvaluatorOptions){pklgo.WithProject(project)}
	newOpts = append(newOpts, opts...)
	evaluator, err := manager.NewEvaluator(ctx, newOpts...)
	if err != nil {
		_ = manager.Close()
		return nil, nil, fmt.Errorf("failed to create evaluator: %w", err)
	}

	return evaluator, func() { _ = manager.Close() }, nil
}
