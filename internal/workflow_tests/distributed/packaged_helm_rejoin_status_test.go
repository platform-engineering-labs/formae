// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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

// A matching rejoin must retain its generation until it returns, even when the
// original operator reports terminal Status while the rejoin mint is blocked.
func TestPackagedHelmRejoinConcurrentTerminalStatus(t *testing.T) {
	kubeconfig := os.Getenv("FORMAE_PACKAGED_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set FORMAE_PACKAGED_KUBECONFIG to a task-owned kind config")
	}
	for _, operation := range []string{"Create", "Update", "Delete"} {
		t.Run(operation, func(t *testing.T) {
			stage, _ := stagePackagedPlugins(t)
			f := newPackagedFixture(t, kubeconfig)
			f.lifetime = 70 * time.Second
			a := startPackagedAgent(t, stage, f, 2*time.Second)
			name := "retain-" + randomSuffix()
			chart := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(chart, "templates"), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(chart, "Chart.yaml"), []byte("apiVersion: v2\nname: retain\nversion: 0.1.0\n"), 0600))
			require.NoError(t, os.WriteFile(filepath.Join(chart, "templates", "object.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+name+"\ndata:\n  value: {{ .Values.value | quote }}\n"), 0600))
			properties := func(value string) []byte {
				raw, err := json.Marshal(map[string]any{"metadata": map[string]string{"name": name, "namespace": "default"}, "chart": chart, "timeoutSeconds": 300, "values": map[string]string{"value": value}})
				require.NoError(t, err)
				return raw
			}
			props := properties("first")
			create := plugin.CreateResource{Namespace: "K8S", ResourceType: packagedHelmType, Label: name, Properties: props, TargetConfig: f.target()}
			if operation != "Create" {
				initial := a.call(t, gen.PID{}, "Create", create)
				require.Equal(t, resource.OperationStatusInProgress, initial.Value.(plugin.TrackedProgress).OperationStatus)
				awaitPackagedTerminalSuccess(t, a, initial.PID, 10*time.Second)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce, enteredOnce sync.Once
			statusEntered, statusRelease := make(chan struct{}), make(chan struct{})
			var statusOnce, statusReleaseOnce sync.Once
			var holdStatus, holdUID atomic.Bool
			defer statusReleaseOnce.Do(func() { close(statusRelease) })
			defer releaseOnce.Do(func() { close(release) })
			method := map[string]string{"Create": "POST", "Update": "PATCH", "Delete": "DELETE"}[operation]
			f.mu.Lock()
			f.gate = func(w http.ResponseWriter, r *http.Request) bool {
				if holdUID.Load() && r.URL.Path == "/api/v1/namespaces/kube-system" {
					select {
					case <-time.After(11 * time.Second):
					case <-r.Context().Done():
						return true
					}
				}
				if holdStatus.Load() && r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/secrets") {
					statusOnce.Do(func() { close(statusEntered) })
					select {
					case <-statusRelease:
					case <-r.Context().Done():
						return true
					}
				}
				if r.Method == method && strings.Contains(r.URL.Path, "/configmaps") {
					enteredOnce.Do(func() { close(entered) })
					select {
					case <-release:
					case <-r.Context().Done():
						return true
					}
				}
				return false
			}
			f.mu.Unlock()
			var request any = create
			switch operation {
			case "Update":
				request = plugin.UpdateResource{Namespace: "K8S", ResourceType: packagedHelmType, Label: name, NativeID: "default/" + name, PriorProperties: props, DesiredProperties: properties("second"), TargetConfig: f.target()}
			case "Delete":
				request = plugin.DeleteResource{Namespace: "K8S", ResourceType: packagedHelmType, NativeID: "default/" + name, TargetConfig: f.target()}
			}
			first := a.call(t, gen.PID{}, operation, request)
			progress := first.Value.(plugin.TrackedProgress)
			require.Equal(t, resource.OperationStatusInProgress, progress.OperationStatus, "initial: %s", progress.StatusMessage)
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not reach object barrier")
			}

			// Wait until the original Status has authenticated and acquired its
			// reader, then hold its storage response. The credential actor handles
			// mints serially, so Status must not need another mint during rejoin.
			holdStatus.Store(true)
			select {
			case <-statusEntered:
			case <-time.After(5 * time.Second):
				t.Fatal("original Status did not enter storage read")
			}
			holdUID.Store(true)
			mintEntered, mintRelease := make(chan struct{}), make(chan struct{})
			var mintReleaseOnce sync.Once
			defer mintReleaseOnce.Do(func() { close(mintRelease) })
			f.mu.Lock()
			f.mintGate = func(ctx context.Context) bool {
				close(mintEntered)
				select {
				case <-mintRelease:
					return true
				case <-ctx.Done():
					return false
				}
			}
			f.mu.Unlock()
			// The shared token reaches its early refresh check after10s, while the
			// worker retains >50s of validity. Hold that refresh after UID admission
			// by first delaying the rejoin's authenticated UID response past10s.
			// The original requester must remain free to receive terminal Status while
			// this independent requester waits synchronously for its rejoin response.
			rejoinRequester, err := a.m.Node.Spawn(func() gen.ProcessBehavior { return &packagedRequester{updates: make(chan packagedProgress, 100)} }, gen.ProcessOptions{})
			require.NoError(t, err)
			replies := make(chan packagedReply, 1)
			require.NoError(t, a.m.Node.Send(rejoinRequester, packagedCall{Operation: operation, Request: request, Reply: replies}))
			select {
			case <-mintEntered:
			case <-time.After(16 * time.Second):
				t.Fatal("rejoin did not enter bridge mint")
			}
			releaseOnce.Do(func() { close(release) })
			require.Eventually(t, func() bool {
				out, err := runOwnedKubectl(kubeconfig, "get", "secrets", "-n", "default", "-l", "owner=helm,name="+name, "-o", "jsonpath={.items[*].metadata.labels.status}")
				if err != nil {
					return false
				}
				if operation == "Delete" {
					return len(out) == 0
				}
				return strings.HasSuffix(string(out), "deployed")
			}, 3*time.Second, 25*time.Millisecond)
			// Persisted terminal state precedes Helm's return. This bounded
			// external hold supplements the exact unit-level completion barrier.
			time.Sleep(100 * time.Millisecond)
			statusReleaseOnce.Do(func() { close(statusRelease) })
			if operation == "Delete" {
				awaitPackagedTerminalSuccess(t, a, first.PID, 7*time.Second)
				// Delete needs no subsequent readiness GET, so the exact terminal-before-
				// rejoin ordering remains observable while shared refresh is held.
				mintReleaseOnce.Do(func() { close(mintRelease) })
			} else {
				// Install/upgrade readiness shares the refreshing client cache. Its live
				// request waits behind this same bounded mint; separate-cache ordering is
				// no longer inducible. Exact reader retention remains unit/race tested.
				waiting := time.NewTimer(100 * time.Millisecond)
			waitOriginal:
				for {
					select {
					case update := <-a.updates:
						if update.PID != first.PID {
							continue
						}
						if result, ok := update.Value.(plugin.TrackedProgress); ok {
							require.Equal(t, resource.OperationStatusInProgress, result.OperationStatus, "original must wait for shared refresh")
						}
					case <-waiting.C:
						break waitOriginal
					}
				}
				mintReleaseOnce.Do(func() { close(mintRelease) })
				awaitPackagedTerminalSuccess(t, a, first.PID, 7*time.Second)
			}
			select {
			case reply := <-replies:
				require.NoError(t, reply.Err)
				result := reply.Value.(plugin.TrackedProgress)
				require.Equal(t, resource.OperationStatusInProgress, result.OperationStatus, "rejoin after original terminal Status: %s", result.StatusMessage)
				require.Equal(t, progress.RequestID, result.RequestID, "rejoin must observe its retained generation")
				t.Logf("%s original operator %v reported terminal before matching rejoin %v returned generation %s", operation, first.PID, reply.PID, result.RequestID)
			case <-time.After(5 * time.Second):
				t.Fatal("rejoin did not return")
			}
		})
	}
}

func awaitPackagedTerminalSuccess(t *testing.T, a *packagedAgent, pid gen.PID, timeout time.Duration) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case update := <-a.updates:
			if update.PID != pid {
				continue
			}
			progress, ok := update.Value.(plugin.TrackedProgress)
			if !ok {
				continue
			}
			require.NotEqual(t, resource.OperationStatusFailure, progress.OperationStatus, "original Status: %s", progress.StatusMessage)
			if progress.OperationStatus == resource.OperationStatusSuccess {
				return
			}
		case <-deadline.C:
			t.Fatal("original operator did not report terminal Status")
		}
	}
}
