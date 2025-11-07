// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

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

	"github.com/apple/pkl-go/pkl"
	"github.com/masterminds/semver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	pklmodel "github.com/platform-engineering-labs/formae/plugins/pkl/model"
)

const ProjectFile = "PklProject"

type PKL struct{}

// Version set at compile time
var Version = "0.0.0"

//go:embed assets
var assets embed.FS

//go:embed generator/*
var generator embed.FS

// Compile time checks to satisfy protocol
var _ plugin.Plugin = PKL{}

var _ plugin.SchemaPlugin = PKL{}

func (p PKL) Name() string {
	return "pkl"
}

func (p PKL) Version() *semver.Version {
	return semver.MustParse(Version)
}

func (p PKL) Type() plugin.Type {
	return plugin.Schema
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

	var configSource *pkl.ModuleSource
	if path == "" {
		configSource = pkl.TextSource(`amends "formae:/Config.pkl"`)
	} else {
		if FileExists(path) {
			configSource = pkl.FileSource(path)
		} else {
			return nil, fmt.Errorf("config file: %s does not exist", path)
		}
	}

	evaluator, err := pkl.NewEvaluator(
		context.Background(),
		pkl.WithFs(formaeFs, "formae"),
		pkl.PreconfiguredOptions,
	)
	if err != nil {
		return nil, err
	}

	defer func(evaluator pkl.Evaluator) {
		_ = evaluator.Close()
	}(evaluator)

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
				Nodename:     config.Agent.Server.Nodename,
				Hostname:     config.Agent.Server.Hostname,
				Port:         int(config.Agent.Server.Port),
				Secret:       config.Agent.Server.Secret,
				ObserverPort: int(config.Agent.Server.ObserverPort),
				TLSCert:      config.Agent.Server.TLSCert,
				TLSKey:       config.Agent.Server.TLSKey,
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
			},
			Retry: pkgmodel.RetryConfig{
				StatusCheckInterval: config.Agent.Retry.StatusCheckInterval.GoDuration(),
				MaxRetries:          int(config.Agent.Retry.MaxRetries),
				RetryDelay:          config.Agent.Retry.RetryDelay.GoDuration(),
			},
			Synchronization: pkgmodel.SynchronizationConfig{
				Enabled:  config.Agent.Synchronization.Enabled,
				Interval: config.Agent.Synchronization.Interval.GoDuration(),
			},
			Discovery: pkgmodel.DiscoveryConfig{
				Enabled:                 config.Agent.Discovery.Enabled,
				ScanTargets:             translateTargets(config.Agent.Discovery.ScanTargets),
				Interval:                config.Agent.Discovery.Interval.GoDuration(),
				LabelTagKeys:            config.Agent.Discovery.LabelTagKeys,
				ResourceTypesToDiscover: config.Agent.Discovery.ResourceTypesToDiscover,
			},
			Logging: pkgmodel.LoggingConfig{
				FilePath:        config.Agent.Logging.FilePath,
				FileLogLevel:    parseLogLevel(config.Agent.Logging.FileLogLevel),
				ConsoleLogLevel: parseLogLevel(config.Agent.Logging.ConsoleLogLevel),
			},
			OTel: pkgmodel.OTelConfig{
				Enabled:     config.Agent.OTel.Enabled,
				ServiceName: config.Agent.OTel.ServiceName,
				OTLP: pkgmodel.OTLPConfig{
					Endpoint: config.Agent.OTel.OTLP.Endpoint,
					Protocol: config.Agent.OTel.OTLP.Protocol,
					Insecure: config.Agent.OTel.OTLP.Insecure,
				},
			},
		},
		Cli: pkgmodel.CliConfig{
			API: pkgmodel.APIConfig{
				URL:  config.Cli.API.URL,
				Port: int(config.Cli.API.Port),
			},
			DisableUsageReporting: config.Cli.DisableUsageReporting,
		},
		Plugins: pkgmodel.PluginConfig{
			Network:        translateDynamic(config.Plugins.Network),
			Authentication: translateDynamic(config.Plugins.Authentication),
		},
	}

	return &translated
}

func (p PKL) Evaluate(path string, cmd pkgmodel.Command, mode pkgmodel.FormaApplyMode, props map[string]string) (*pkgmodel.Forma, error) {
	var evaluator pkl.Evaluator
	var err error

	addSchemaContextProperties(cmd, mode, props)

	projectDir := WalkForProjectFile(filepath.Dir(path))

	if projectDir != "" {
		evaluator, err = pkl.NewProjectEvaluator(
			context.Background(),
			&url.URL{Scheme: "file", Path: projectDir},
			pkl.PreconfiguredOptions,
			pkl.WithResourceReader(libExtension{}),
			func(opts *pkl.EvaluatorOptions) {
				opts.Properties = props
				opts.OutputFormat = "json"
			})

		if err != nil {
			return nil, err
		}
	} else {
		evaluator, err = pkl.NewEvaluator(
			context.Background(),
			pkl.PreconfiguredOptions,
			pkl.WithResourceReader(libExtension{}),
			func(opts *pkl.EvaluatorOptions) {
				opts.Properties = props
				opts.OutputFormat = "json"
			})

		if err != nil {
			return nil, err
		}
	}

	defer func(evaluator pkl.Evaluator) {
		_ = evaluator.Close()
	}(evaluator)

	// Render PKL
	result, err := evaluator.EvaluateOutputText(context.Background(), pkl.FileSource(path))
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

func (p PKL) GenerateSourceCode(forma *pkgmodel.Forma, path string, includes []string) (plugin.GenerateSourcesResult, error) {
	res := plugin.GenerateSourcesResult{}

	code, err := p.SerializeForma(forma, &plugin.SerializeOptions{Schema: "pkl"})
	if err != nil {
		slog.Error(err.Error())
		return plugin.GenerateSourcesResult{}, plugin.ErrFailedToGenerateSources
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
		return plugin.GenerateSourcesResult{}, fmt.Errorf("failed to create parent directory: %v", err)
	}

	projectFile := filepath.Join(parentDir, "PklProject")
	if _, err = os.Stat(projectFile); os.IsNotExist(err) {
		// No PklProject exists, initialize it
		err = p.ProjectInit(parentDir, includes)
		if err != nil {
			return plugin.GenerateSourcesResult{}, fmt.Errorf("failed to initialize Pkl project: %v", err)
		}
		res.InitializedNewProject = true
		res.ProjectPath = parentDir
		fmt.Println("Initialized new Pkl project at", parentDir)
	}

	err = os.WriteFile(path, []byte(code), 0644)
	if err != nil {
		return plugin.GenerateSourcesResult{}, fmt.Errorf("failed to write Pkl file: %v", err)
	}

	// Check if PklProject.deps.json exists after writing the Pkl file
	depsFile := filepath.Join(parentDir, "PklProject.deps.json")
	if _, err := os.Stat(depsFile); os.IsNotExist(err) {
		res.Warnings = append(res.Warnings, fmt.Sprintf("Pkl dependencies not resolved. Run 'pkl project resolve' in '%s' to resolve Pkl dependencies.", parentDir))
	}

	return res, nil
}

func (p PKL) ProjectInit(path string, include []string) error {
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

	evaluator, err := pkl.NewEvaluator(context.Background(), pkl.PreconfiguredOptions, pkl.WithFs(assets, "assets"), func(opts *pkl.EvaluatorOptions) {
		opts.Properties = map[string]string{
			"packages": strings.Join(include, ","),
		}
	})
	if err != nil {
		return err
	}

	projectFile, err := evaluator.EvaluateOutputText(context.Background(), pkl.UriSource("assets:/assets/PklProjectTemplate.pkl"))
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

	cmd := exec.Command("pkl", "project", "resolve", path)
	if errors.Is(cmd.Err, exec.ErrDot) {
		cmd.Err = nil
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("project resolve failed: %v", err)
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

func (p PKL) Serialize(resource *pkgmodel.Resource, options *plugin.SerializeOptions) (string, error) {
	return p.serializeWithPKL(resource, options)
}

func (p PKL) SerializeForma(forma *pkgmodel.Forma, options *plugin.SerializeOptions) (string, error) {
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

func translateTargets(targets []pklmodel.Target) []pkgmodel.Target {
	var translated []pkgmodel.Target
	for _, target := range targets {
		safe := sanitizeConfig(target.Config.Properties)
		configJson, err := json.Marshal(safe)
		if err != nil {
			slog.Error("Failed to marshal target config", "error", err)
			continue
		}
		translated = append(translated, pkgmodel.Target{
			Label:        target.Label,
			Namespace:    target.Namespace,
			Config:       configJson,
			Discoverable: target.Discoverable,
		})
	}
	return translated
}

func translateDynamic(dyn *pkl.Object) json.RawMessage {
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

var Plugin = PKL{}
