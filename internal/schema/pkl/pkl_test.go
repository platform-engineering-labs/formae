// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package pkl

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPkl_Evaluate(t *testing.T) {
	props := map[string]string{"name": "bacon.platform.engineering"}

	p := PKL{}
	forma, err := p.Evaluate("./testdata/forma/test.pkl", model.CommandApply, model.FormaApplyModeReconcile, props)
	assert.NoError(t, err)

	jsonString := forma.ToJSON()

	assert.Equal(t, props["name"], gjson.Get(jsonString, "Properties.name.Value").String())
	assert.Equal(t, "FakeAWS::Route53::HostedZone", gjson.Get(jsonString, "Resources.0.Type").String())
	assert.Equal(t, "A", gjson.Get(jsonString, "Resources.1.Properties.Type").String())
}

// TestPkl_Evaluate_AutoResolvesUnresolvedProject reproduces the reported failure:
// applying a forma whose directory has a PklProject but no resolved
// PklProject.deps.json used to fail with a NoSuchFileException and force a manual
// `pkl project resolve`. Evaluate now auto-resolves via pklrun. The deps file is
// gitignored and regenerated, so removing it needs no cleanup.
func TestPkl_Evaluate_AutoResolvesUnresolvedProject(t *testing.T) {
	depsPath := "./testdata/forma/PklProject.deps.json"
	if err := os.Remove(depsPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("removing deps.json to simulate unresolved project: %v", err)
	}

	props := map[string]string{"name": "bacon.platform.engineering"}
	p := PKL{}
	_, err := p.Evaluate("./testdata/forma/test.pkl", model.CommandApply, model.FormaApplyModeReconcile, props)
	require.NoError(t, err, "Evaluate must auto-run `pkl project resolve` when PklProject.deps.json is missing")

	if _, statErr := os.Stat(depsPath); statErr != nil {
		t.Fatalf("auto-resolve should have recreated PklProject.deps.json: %v", statErr)
	}
}

func TestPkl_EvaluateCommandAndMode(t *testing.T) {
	props := map[string]string{"name": "bacon.platform.engineering"}

	p := PKL{}
	forma, err := p.Evaluate("./testdata/forma/test.pkl", model.CommandDestroy, model.FormaApplyModePatch, props)
	assert.NoError(t, err)

	jsonString := forma.ToJSON()

	assert.Equal(t, string(model.CommandDestroy), gjson.Get(jsonString, "Properties.cmd.Value").String())
	assert.Equal(t, string(model.FormaApplyModePatch), gjson.Get(jsonString, "Properties.mode.Value").String())
}

func TestPkl_DefaultStackAndTarget(t *testing.T) {
	props := map[string]string{"Name": "bacon.platform.engineering"}

	p := PKL{}
	forma, err := p.Evaluate("./testdata/forma/test.pkl", model.CommandDestroy, model.FormaApplyModePatch, props)
	assert.NoError(t, err)

	jsonString := forma.ToJSON()
	assert.Equal(t, "pel-dns", gjson.Get(jsonString, "Resources.1.Stack").String())
	assert.Equal(t, "default-aws-target", gjson.Get(jsonString, "Resources.1.Target").String())
}

func TestPkl_DefaultStackAndTargetViaRes(t *testing.T) {
	props := map[string]string{"Name": "bacon.platform.engineering"}

	p := PKL{}
	forma, err := p.Evaluate("./testdata/forma/st_res_test.pkl", model.CommandDestroy, model.FormaApplyModePatch, props)
	assert.NoError(t, err)

	jsonString := forma.ToJSON()
	assert.Equal(t, "pel-dns", gjson.Get(jsonString, "Resources.1.Stack").String())
	assert.Equal(t, "default-aws-target", gjson.Get(jsonString, "Resources.1.Target").String())
}

func TestPkl_DefaultStackAndTargetViaResNotFound(t *testing.T) {
	p := PKL{}
	_, err := p.Evaluate("./testdata/forma/st_res_no_stack_test.pkl", model.CommandDestroy, model.FormaApplyModePatch, nil)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "no default stack found")
	}

	_, err = p.Evaluate("./testdata/forma/st_res_no_target_test.pkl", model.CommandDestroy, model.FormaApplyModePatch, nil)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "no default target found")
	}
}

func TestPkl_FormaeValue(t *testing.T) {
	props := map[string]string{
		"name":        "test-secret",
		"secret":      "l33ts3cr3t",
		"description": "the test secret",
	}

	p := PKL{}
	forma, err := p.Evaluate("./testdata/forma/value_test.pkl", model.CommandEval, model.FormaApplyModePatch, props)
	assert.NoError(t, err)

	jsonString := forma.ToJSON()
	assert.Equal(t, "Opaque", gjson.Get(jsonString, "Resources.0.Properties.SecretString.$visibility").String())
	assert.Equal(t, "SetOnce", gjson.Get(jsonString, "Resources.1.Properties.SecretString.$strategy").String())
	assert.Equal(t, props["secret"], gjson.Get(jsonString, "Resources.0.Properties.SecretString.$value").String())
}

func TestPkl_TFVarsIntegration(t *testing.T) {
	p := PKL{}
	forma, err := p.Evaluate("./testdata/forma/tfvars_test.pkl", model.CommandApply, model.FormaApplyModeReconcile, nil)
	assert.NoError(t, err)

	jsonString := forma.ToJSON()

	// The target config region should come from example.tfvars
	assert.Equal(t, "us-west-2", gjson.Get(jsonString, "Targets.0.Config.Region").String())
	// The queue name should interpolate the region from tfvars
	assert.Equal(t, "demo-us-west-2-queue", gjson.Get(jsonString, "Resources.0.Properties.QueueName").String())
}

func TestPkl_TFVarsIntegration_AbsolutePathOutsidePklFolder(t *testing.T) {
	// Place a .tfvars file at an absolute path OUTSIDE the testdata/forma
	// directory (where the .pkl lives) and outside the internal/schema/pkl
	// tree entirely. This verifies that tfvarsReader.Read accepts absolute
	// paths and does not reject them or re-anchor them on baseDir.
	tmpDir := t.TempDir()
	absTfvars := filepath.Join(tmpDir, "external.tfvars")
	content := []byte(`region         = "eu-west-1"
instance_count = 2
enable_logging = false
instance_type  = "t3.small"

tags = {
  Name        = "abs-path"
  Environment = "test"
}

availability_zones = ["eu-west-1a"]
`)
	require.NoError(t, os.WriteFile(absTfvars, content, 0644))

	p := PKL{}
	props := map[string]string{"tfvarsAbsPath": absTfvars}
	forma, err := p.Evaluate("./testdata/forma/tfvars_absolute_test.pkl", model.CommandApply, model.FormaApplyModeReconcile, props)
	require.NoError(t, err)

	jsonString := forma.ToJSON()
	assert.Equal(t, "eu-west-1", gjson.Get(jsonString, "Targets.0.Config.Region").String())
	assert.Equal(t, "demo-eu-west-1-queue", gjson.Get(jsonString, "Resources.0.Properties.QueueName").String())
}

func TestPkl_FormaeConfig(t *testing.T) {
	p := PKL{}
	config, err := p.FormaeConfig("./testdata/config/test_config.pkl")
	assert.NoError(t, err)
	assert.Equal(t, "formae", config.Agent.Server.Nodename)
	assert.Equal(t, "formae.example.com", config.Agent.Server.Hostname)
	assert.Equal(t, 1234, config.Agent.Server.Port)
	assert.Equal(t, 15100, config.Agent.Server.ErgoPort)
	assert.Equal(t, 15200, config.Agent.Server.RegistrarPort)
	assert.Equal(t, "secret", config.Agent.Server.Secret)
	assert.Equal(t, 0, config.Agent.Server.ObserverPort)

	assert.Equal(t, model.SqliteDatastore, config.Agent.Datastore.DatastoreType)
	assert.Equal(t, "db.example.com", config.Agent.Datastore.Postgres.Host)
	assert.Equal(t, 5432, config.Agent.Datastore.Postgres.Port)
	assert.Equal(t, "formae_user", config.Agent.Datastore.Postgres.User)
	assert.Equal(t, "formae_password", config.Agent.Datastore.Postgres.Password)
	assert.Equal(t, "formae_db", config.Agent.Datastore.Postgres.Database)
	assert.Equal(t, "public", config.Agent.Datastore.Postgres.Schema)
	assert.Equal(t, "", config.Agent.Datastore.Postgres.ConnectionParams)
	assert.Equal(t, "formae.db", config.Agent.Datastore.Sqlite.FilePath)

	assert.Equal(t, 5*time.Second, config.Agent.Retry.StatusCheckInterval)
	assert.Equal(t, 3, config.Agent.Retry.MaxRetries)
	assert.Equal(t, 9*time.Second, config.Agent.Retry.RetryDelay)

	assert.False(t, config.Agent.Synchronization.Enabled)
	assert.Equal(t, 10*time.Minute, config.Agent.Synchronization.Interval)

	assert.True(t, config.Agent.Discovery.Enabled)
	assert.Equal(t, 20*time.Minute, config.Agent.Discovery.Interval)
	assert.Equal(t, []string{"Name", "Environment"}, config.Agent.Discovery.LabelTagKeys)

	assert.Equal(t, "/var/log/formae.log", config.Agent.Logging.FilePath)
	assert.Equal(t, slog.LevelDebug, config.Agent.Logging.FileLogLevel)
	assert.Equal(t, slog.LevelInfo, config.Agent.Logging.ConsoleLogLevel)

	assert.Equal(t, "thedude", config.Artifacts.Username)
	assert.Equal(t, "takeiteasy", config.Artifacts.Password)

	assert.NotNil(t, config.Network)
	assert.Equal(t, "tailscale", config.Network.Type)
	assert.NotNil(t, config.Network.Tailscale)
	assert.False(t, config.Network.Tailscale.TLS)
	assert.Equal(t, "someAuthKey", config.Network.Tailscale.AuthKey)
}

func TestTranslateResourcePluginConfig(t *testing.T) {
	p := PKL{}
	config, err := p.FormaeConfig("./testdata/config/test_resource_plugin_config.pkl")
	require.NoError(t, err)

	require.Len(t, config.Agent.ResourcePlugins, 1)
	rpc := config.Agent.ResourcePlugins[0]
	assert.Equal(t, "fake-aws", rpc.Type)
	assert.True(t, rpc.Enabled)
	assert.NotNil(t, rpc.RateLimit)
	assert.Equal(t, model.RateLimitScopeNamespace, rpc.RateLimit.Scope)
	assert.Equal(t, 5, rpc.RateLimit.MaxRequestsPerSecondForNamespace)
	assert.Equal(t, []string{"FakeAWS::S3::Bucket"}, rpc.ResourceTypesToDiscover)
	assert.NotNil(t, rpc.Retry)
	assert.Equal(t, 3, rpc.Retry.MaxRetries)
	assert.Equal(t, 5*time.Second, rpc.Retry.RetryDelay)
	assert.Equal(t, 2*time.Second, rpc.Retry.StatusCheckInterval)
}

func TestTranslateResourcePluginConfig_Disabled(t *testing.T) {
	p := PKL{}
	config, err := p.FormaeConfig("./testdata/config/test_resource_plugin_disabled.pkl")
	require.NoError(t, err)

	require.Len(t, config.Agent.ResourcePlugins, 1)
	assert.Equal(t, "fake-aws", config.Agent.ResourcePlugins[0].Type)
	assert.False(t, config.Agent.ResourcePlugins[0].Enabled)
}

func TestTranslateResourcePluginConfig_Dynamic(t *testing.T) {
	p := PKL{}
	config, err := p.FormaeConfig("./testdata/config/test_resource_plugin_dynamic.pkl")
	require.NoError(t, err)
	require.NotNil(t, config)

	require.Len(t, config.Agent.ResourcePlugins, 1)
	rpc := config.Agent.ResourcePlugins[0]
	assert.Equal(t, "sftp", rpc.Type)
	assert.True(t, rpc.Enabled)
	require.NotNil(t, rpc.RateLimit)
	assert.Equal(t, 3, rpc.RateLimit.MaxRequestsPerSecondForNamespace)
}

func TestTranslateResourcePluginConfig_CustomFields(t *testing.T) {
	p := PKL{}
	config, err := p.FormaeConfig("./testdata/config/test_resource_plugin_custom.pkl")
	require.NoError(t, err)
	require.NotNil(t, config)

	require.Len(t, config.Agent.ResourcePlugins, 1)
	rpc := config.Agent.ResourcePlugins[0]
	assert.Equal(t, "sftp", rpc.Type)
	require.NotNil(t, rpc.PluginConfig)
	assert.Contains(t, string(rpc.PluginConfig), "defaultTimeoutSeconds")
	assert.Contains(t, string(rpc.PluginConfig), "60")
	assert.Contains(t, string(rpc.PluginConfig), "defaultFilePermissions")
	assert.Contains(t, string(rpc.PluginConfig), "0755")
}

func TestDeprecationWarning_GlobalRetryWithPerPlugin(t *testing.T) {
	p := PKL{}
	config, err := p.FormaeConfig("./testdata/config/test_deprecation_retry.pkl")
	require.NoError(t, err)

	require.Len(t, config.Warnings, 1)
	assert.Contains(t, config.Warnings[0], "agent.retry")
	assert.Contains(t, config.Warnings[0], "deprecated")
}

func TestDeprecationWarning_LegacyArtifactsURL(t *testing.T) {
	p := PKL{}
	config, err := p.FormaeConfig("./testdata/config/test_deprecation_artifacts.pkl")
	require.NoError(t, err)

	// Expect three warnings: url, username, password
	require.Len(t, config.Warnings, 3)
	assert.Contains(t, config.Warnings[0], "artifacts.url")
	assert.Contains(t, config.Warnings[0], "deprecated")
	assert.Contains(t, config.Warnings[1], "artifacts.username")
	assert.Contains(t, config.Warnings[1], "deprecated")
	assert.Contains(t, config.Warnings[2], "artifacts.password")
	assert.Contains(t, config.Warnings[2], "deprecated")

	// The URL should have been synthesized into Repositories as a binary entry
	require.Len(t, config.Artifacts.Repositories, 1)
	assert.Equal(t, "example.org", config.Artifacts.Repositories[0].URI.Host)
	assert.Equal(t, model.RepositoryTypeBinary, config.Artifacts.Repositories[0].Type)
}
