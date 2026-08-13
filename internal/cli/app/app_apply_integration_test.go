// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	formae "github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// pklFixture is a real forma file (with a pre-resolved PklProject.deps.json,
// so evaluation needs no network) borrowed from the pkl schema plugin's own
// tests, used here purely to drive a real Apply() call end to end.
const pklFixture = "../../schema/pkl/testdata/forma/test.pkl"

// TestApply_PreflightAndSubmissionAreSeparateHits drives a real Apply() call
// through a real forma evaluation against an httptest server, and asserts
// that the Stats preflight and the command submission each hit the agent
// exactly once, on their own endpoints. Apply wraps these as two independent
// withAuthRetry closures specifically so a retry never replays the
// preflight alongside a resubmission of the mutation; this is the boundary
// that would silently break if Apply were ever refactored to wrap the whole
// command in one retryable operation instead of two.
func TestApply_PreflightAndSubmissionAreSeparateHits(t *testing.T) {
	require.NoError(t, config.Config.EnsureDataDirectory())
	require.NoError(t, config.Config.EnsureClientID())

	var statsHits, commandHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&statsHits, 1)
		w.Header().Set("Content-Type", "application/json")
		b, _ := json.Marshal(apimodel.Stats{Version: formae.Version, AgentID: "test-agent"})
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/api/v1/commands", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&commandHits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	a := &App{
		Config: &pkgmodel.Config{
			Cli: pkgmodel.CliConfig{
				Connection:            &pkgmodel.ClassicConnection{URL: ts.URL, Port: 80},
				DisableUsageReporting: true,
			},
		},
	}

	props := map[string]string{"name": "bacon.platform.engineering"}
	_, _, err := a.Apply(pklFixture, props, pkgmodel.FormaApplyModeReconcile, false, false)
	require.NoError(t, err)

	assert.Equal(t, int32(1), atomic.LoadInt32(&statsHits), "the Stats preflight must hit the agent exactly once")
	assert.Equal(t, int32(1), atomic.LoadInt32(&commandHits), "the submission must hit the agent exactly once, on its own endpoint separate from the preflight")
}
