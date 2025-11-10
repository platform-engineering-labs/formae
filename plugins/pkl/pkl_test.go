// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package main

import (
	"log/slog"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

func TestPkl_Evaluate(t *testing.T) {
	props := map[string]string{"name": "bacon.platform.engineering"}

	forma, err := Plugin.Evaluate("./testdata/forma/test.pkl", model.CommandApply, model.FormaApplyModeReconcile, props)
	assert.NoError(t, err)

	jsonString := forma.ToJSON()

	assert.Equal(t, props["name"], gjson.Get(jsonString, "Properties.name.Value").String())
	assert.Equal(t, "AWS::Route53::HostedZone", gjson.Get(jsonString, "Resources.0.Type").String())
	assert.Equal(t, "A", gjson.Get(jsonString, "Resources.1.Properties.Type").String())
}

func TestPkl_EvaluateCommandAndMode(t *testing.T) {
	props := map[string]string{"name": "bacon.platform.engineering"}

	forma, err := Plugin.Evaluate("./testdata/forma/test.pkl", model.CommandDestroy, model.FormaApplyModePatch, props)
	assert.NoError(t, err)

	jsonString := forma.ToJSON()

	assert.Equal(t, string(model.CommandDestroy), gjson.Get(jsonString, "Properties.cmd.Value").String())
	assert.Equal(t, string(model.FormaApplyModePatch), gjson.Get(jsonString, "Properties.mode.Value").String())
}

func TestPkl_DefaultStackAndTarget(t *testing.T) {
	props := map[string]string{"Name": "bacon.platform.engineering"}

	forma, err := Plugin.Evaluate("./testdata/forma/test.pkl", model.CommandDestroy, model.FormaApplyModePatch, props)
	assert.NoError(t, err)

	jsonString := forma.ToJSON()
	assert.Equal(t, "pel-dns", gjson.Get(jsonString, "Resources.1.Stack").String())
	assert.Equal(t, "default-aws-target", gjson.Get(jsonString, "Resources.1.Target").String())
}

func TestPkl_DefaultStackAndTargetViaRes(t *testing.T) {
	props := map[string]string{"Name": "bacon.platform.engineering"}

	forma, err := Plugin.Evaluate("./testdata/forma/st_res_test.pkl", model.CommandDestroy, model.FormaApplyModePatch, props)
	assert.NoError(t, err)

	jsonString := forma.ToJSON()
	assert.Equal(t, "pel-dns", gjson.Get(jsonString, "Resources.1.Stack").String())
	assert.Equal(t, "default-aws-target", gjson.Get(jsonString, "Resources.1.Target").String())
}

func TestPkl_DefaultStackAndTargetViaResNotFound(t *testing.T) {
	_, err := Plugin.Evaluate("./testdata/forma/st_res_no_stack_test.pkl", model.CommandDestroy, model.FormaApplyModePatch, nil)

	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "no default stack found")
	}

	_, err = Plugin.Evaluate("./testdata/forma/st_res_no_target_test.pkl", model.CommandDestroy, model.FormaApplyModePatch, nil)

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

	forma, err := Plugin.Evaluate("./testdata/forma/value_test.pkl", model.CommandEval, model.FormaApplyModePatch, props)
	assert.NoError(t, err)

	jsonString := forma.ToJSON()
	assert.Equal(t, "Opaque", gjson.Get(jsonString, "Resources.0.Properties.SecretString.$visibility").String())
	assert.Equal(t, "SetOnce", gjson.Get(jsonString, "Resources.1.Properties.SecretString.$strategy").String())
	assert.Equal(t, props["secret"], gjson.Get(jsonString, "Resources.0.Properties.SecretString.$value").String())
}

func TestPkl_FormaeConfig(t *testing.T) {
	config, err := Plugin.FormaeConfig("./testdata/config/test_config.pkl")
	assert.NoError(t, err)
	assert.Equal(t, "formae", config.Agent.Server.Nodename)
	assert.Equal(t, "formae.example.com", config.Agent.Server.Hostname)
	assert.Equal(t, 1234, config.Agent.Server.Port)
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

	assert.JSONEq(t, `{"Type": "tailscale", "Tls": false, "AuthKey": "someAuthKey"}`, string(config.Plugins.Network))
}
