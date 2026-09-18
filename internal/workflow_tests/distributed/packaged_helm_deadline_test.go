// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

func TestPackagedHelmStarvationAndActionDeadline(t *testing.T) {
	for _, mode := range []string{"starvation", "action-deadline"} {
		t.Run(mode, func(t *testing.T) {
			stage, _ := stagePackagedPlugins(t)
			f := newPackagedFixture(t, packagedKubeconfig(t))
			timeout := 300
			if mode == "action-deadline" {
				timeout = 132
				f.lifetime = time.Hour
			}
			entered, release, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once, releaseOnce, cancelOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			f.gate = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/configmaps") {
					once.Do(func() { close(entered) })
					select {
					case <-release:
					case <-r.Context().Done():
						cancelOnce.Do(func() { close(canceled) })
						return true
					}
				}
				return false
			}
			a := startPackagedAgent(t, stage, f, 30*time.Second)
			request := packagedChartRequest(t, f, "deadline-"+randomSuffix(), timeout)
			if mode == "starvation" {
				var props map[string]any
				require.NoError(t, json.Unmarshal(request.Properties, &props))
				require.NoError(t, os.WriteFile(filepath.Join(props["chart"].(string), "templates", "hook.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+request.Label+"-hook\n  annotations:\n    helm.sh/hook: post-install\n"), 0600))
			}
			start := time.Now()
			first := a.call(t, gen.PID{}, "Create", request)
			p := first.Value.(plugin.TrackedProgress)
			require.Equal(t, resource.OperationStatusInProgress, p.OperationStatus, "%s", p.StatusMessage)
			require.NoError(t, a.m.Node.SendExit(first.PID, gen.TerminateReasonNormal))
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not reach request barrier")
			}
			if mode == "starvation" {
				assertNoPackagedMints(t, f, 18*time.Second)
				releaseOnce.Do(func() { close(release) })
			}
			remaining := 134*time.Second - time.Since(start)
			if remaining > 0 {
				assertNoPackagedMints(t, f, remaining)
			}
			if mode == "action-deadline" {
				select {
				case <-canceled:
				default:
					t.Fatal("action deadline did not cancel the actual worker HTTP request")
				}
			}
			result := a.call(t, gen.PID{}, "Status", packagedStatus(p.RequestID, f.target(), resource.OperationCreate)).Value.(plugin.TrackedProgress)
			require.Equal(t, resource.OperationStatusFailure, result.OperationStatus, "%s", result.StatusMessage)
			if mode == "starvation" {
				require.Contains(t, result.StatusMessage, "authentication request failed", "transport redacts internal starvation text")
			} else {
				require.Contains(t, result.StatusMessage, "deadline")
			}
			t.Logf("%s completed after %s with no callback/mint during 134s observation", mode, time.Since(start))
		})
	}
}
