// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/pkg/credential"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

// The HTTP mint's cancellation is not equivalent to IdentityToken returning.
// Hold the actual broker method's defer, correlated to this callback's audience
// and request ID, and require its owning callback to remain blocked.
func TestPackagedHelmBrokerWaitsForOwningMethodCompletion(t *testing.T) {
	for _, audience := range []string{packagedAudience, packagedOtherAudience} {
		t.Run(audience, func(t *testing.T) {
			stage, _ := stagePackagedPlugins(t)
			f := newPackagedFixture(t, packagedKubeconfig(t))
			var enabled atomic.Bool
			entered := make(chan credential.OidcIdentityTokenRequest, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			completion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req credential.OidcIdentityTokenRequest
				if json.NewDecoder(r.Body).Decode(&req) != nil {
					w.WriteHeader(400)
					return
				}
				if enabled.Load() {
					entered <- req
					select {
					case <-release:
					case <-r.Context().Done():
					}
				}
				w.WriteHeader(200)
			}))
			t.Cleanup(completion.Close)
			a := startPackagedAgent(t, stage, f, 30*time.Second, func(cfg *model.Config) {
				cfg.Agent.OidcCredentialPlugins[0].PluginConfig, _ = json.Marshal(map[string]string{"controlUrl": f.broker.URL, "completionUrl": completion.URL})
			})
			target := json.RawMessage(bytes.ReplaceAll(f.target(), []byte(packagedAudience), []byte(audience)))
			var cfg map[string]any
			require.NoError(t, json.Unmarshal(target, &cfg))
			cfg["KubernetesVersion"] = "1.36"
			target, _ = json.Marshal(cfg)
			f.mu.Lock()
			f.mintGate = func(ctx context.Context) bool { <-ctx.Done(); return false }
			f.mu.Unlock()
			enabled.Store(true)
			request := packagedChartRequest(t, f, "unwind-"+randomSuffix(), 300)
			request.TargetConfig = target
			owner := independentPackagedCall(t, a, "Create", request)
			select {
			case req := <-entered:
				require.Equal(t, audience, req.Audience)
				require.NotEmpty(t, req.RequestID)
				t.Logf("owning callback audience=%s requestID=%s: method canceled but return is held", req.Audience, req.RequestID)
			case <-time.After(12 * time.Second):
				t.Fatal("method completion barrier was not reached")
			}
			select {
			case result := <-owner:
				t.Fatalf("owning callback returned before its credential method: %+v", result)
			case <-time.After(50 * time.Millisecond):
			}
			releaseOnce.Do(func() { close(release) })
			result := receivePackaged(t, owner, 2*time.Second)
			require.Equal(t, resource.OperationStatusFailure, result.Value.(plugin.TrackedProgress).OperationStatus)
			assertPackagedReadOnly(t, f)
		})
	}
}
