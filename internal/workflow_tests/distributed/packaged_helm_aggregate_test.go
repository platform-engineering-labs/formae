// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

func TestPackagedHelmAggregatePreparationBudget(t *testing.T) {
	kubeconfig := packagedKubeconfig(t)
	for _, path := range []string{"index-archive", "initial-prefetch", "initial-prefetch-slow", "storage"} {
		t.Run(path, func(t *testing.T) {
			stage, _ := stagePackagedPlugins(t)
			f := newPackagedFixture(t, kubeconfig)
			f.lifetime = time.Hour
			if strings.HasPrefix(path, "initial-prefetch") {
				f.lifetime = 16 * time.Second
			}
			reached := make(chan struct{})
			var reachedOnce sync.Once
			var preflightOnce sync.Once
			delay := 15 * time.Second
			if strings.HasPrefix(path, "initial-prefetch") {
				delay = 25 * time.Second
			}
			if path == "storage" {
				delay = 25 * time.Second
			}
			f.gate = func(w http.ResponseWriter, r *http.Request) bool {
				if path != "index-archive" && r.URL.Path == "/version" {
					preflightOnce.Do(func() {
						select {
						case <-time.After(delay):
						case <-r.Context().Done():
						}
					})
				}
				if path != "index-archive" && r.URL.Path == "/api/v1/namespaces/kube-system" {
					uidDelay := 10 * time.Second
					if path == "initial-prefetch-slow" {
						uidDelay = 7 * time.Second
					}
					if path == "initial-prefetch" {
						uidDelay = 20 * time.Second
					}
					select {
					case <-time.After(uidDelay):
					case <-r.Context().Done():
						return true
					}
				}
				if path == "storage" && strings.HasSuffix(r.URL.Path, "/secrets") {
					reachedOnce.Do(func() { close(reached) })
					<-r.Context().Done()
					return true
				}
				return false
			}
			if strings.HasPrefix(path, "initial-prefetch") {
				var calls atomic.Int32
				f.mintGate = func(ctx context.Context) bool {
					if calls.Add(1) == 3 {
						reachedOnce.Do(func() { close(reached) })
						<-ctx.Done()
						return false
					}
					return true
				}
			}
			a := startPackagedAgent(t, stage, f, 30*time.Second)
			request := packagedChartRequest(t, f, "aggregate-"+randomSuffix(), 300)
			if path == "index-archive" {
				var server *httptest.Server
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/index.yaml" {
						select {
						case <-time.After(delay):
						case <-r.Context().Done():
							return
						}
						fmt.Fprintf(w, "apiVersion: v1\nentries:\n  acceptance:\n  - apiVersion: v2\n    name: acceptance\n    version: 0.1.0\n    urls:\n    - %s/chart.tgz\n", server.URL)
						return
					}
					reachedOnce.Do(func() { close(reached) })
					<-r.Context().Done()
				}))
				defer server.Close()
				var props map[string]any
				require.NoError(t, json.Unmarshal(request.Properties, &props))
				props["chart"] = "acceptance"
				props["repoURL"] = server.URL
				request.Properties, _ = json.Marshal(props)
			}
			start := time.Now()
			reply := a.call(t, gen.PID{}, "Create", request)
			elapsed := time.Since(start)
			result := reply.Value.(plugin.TrackedProgress)
			require.Equal(t, resource.OperationStatusFailure, result.OperationStatus, "%s", result.StatusMessage)
			if path == "initial-prefetch" {
				select {
				case <-reached:
					t.Fatal("must not start broker mint with only five seconds left")
				default:
				}
				require.Greater(t, elapsed, 43*time.Second)
				require.Less(t, elapsed, 48*time.Second)
				mints, _, _ := f.counts()
				require.Equal(t, 2, mints, "only version and UID authentication may mint")
			} else if path == "initial-prefetch-slow" {
				select {
				case <-reached:
				default:
					t.Fatal("initial worker prefetch was not reached")
				}
				require.Greater(t, elapsed, 40*time.Second)
				require.Less(t, elapsed, 47*time.Second)
				mints, _, _ := f.counts()
				require.Equal(t, 3, mints)
			} else {
				select {
				case <-reached:
				default:
					t.Fatal("callback did not enter required second stage")
				}
				require.Greater(t, elapsed, 47*time.Second)
			}
			require.Less(t, elapsed, 55*time.Second)
			require.NotContains(t, result.RequestID, "#", "preparation exhaustion must not publish a worker")
			assertPackagedReadOnly(t, f)
			t.Logf("%s aggregate callback elapsed %s", path, elapsed)
		})
	}
}
