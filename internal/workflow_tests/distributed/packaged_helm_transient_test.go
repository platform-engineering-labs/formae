// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"encoding/json"
	"net/http"
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

// A failed UID read must keep the owning SDK operator polling the exact flight.
// Until the read succeeds the callback has no authority to service any bridge.
func TestPackagedHelmTransientUIDStatusPreservesOwner(t *testing.T) {
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, packagedKubeconfig(t))
	f.lifetime = time.Hour
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var failUID atomic.Bool
	var failed atomic.Int32
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/api/v1/namespaces/kube-system" && failUID.CompareAndSwap(true, false) {
			failed.Add(1)
			http.Error(w, "temporary identity service failure", http.StatusInternalServerError)
			return true
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/configmaps") {
			enteredOnce.Do(func() { close(entered) })
			select {
			case <-release:
			case <-r.Context().Done():
				return true
			}
		}
		return false
	}
	a := startPackagedAgent(t, stage, f, time.Second)
	first := a.call(t, gen.PID{}, "Create", packagedChartRequest(t, f, "uid-retry-"+randomSuffix(), 300))
	progress := first.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, progress.OperationStatus)
	require.Contains(t, progress.RequestID, "#")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach mutation barrier")
	}
	failUID.Store(true)
	retry := awaitPackagedProgress(t, a, first.PID, resource.OperationStatusInProgress, 5*time.Second)
	require.Equal(t, int32(1), failed.Load(), "Status did not perform live UID read")
	require.Equal(t, progress.RequestID, retry.RequestID, "Status changed owning generation")
	releaseOnce.Do(func() { close(release) })
	awaitPackagedTerminalSuccess(t, a, first.PID, 10*time.Second)
}

func TestPackagedHelmCallbacksReuseCredentials(t *testing.T) {
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, packagedKubeconfig(t))
	f.lifetime = time.Hour
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var uidReads atomic.Int32
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/api/v1/namespaces/kube-system" {
			uidReads.Add(1)
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/configmaps") {
			enteredOnce.Do(func() { close(entered) })
			select {
			case <-release:
			case <-r.Context().Done():
				return true
			}
		}
		return false
	}
	a := startPackagedAgent(t, stage, f, time.Second)
	first := a.call(t, gen.PID{}, "Create", packagedChartRequest(t, f, "reuse-"+randomSuffix(), 300))
	p := first.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, p.OperationStatus)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker not running")
	}
	for i := 0; i < 3; i++ {
		next := awaitPackagedProgress(t, a, first.PID, resource.OperationStatusInProgress, 5*time.Second)
		require.Equal(t, p.RequestID, next.RequestID)
	}
	require.GreaterOrEqual(t, uidReads.Load(), int32(4), "every callback must read live UID")
	mints, _, expired := f.counts()
	require.Equal(t, 1, mints, "UID, bridge and storage must share identity-bound credentials")
	require.Zero(t, expired)
	releaseOnce.Do(func() { close(release) })
	awaitPackagedTerminalSuccess(t, a, first.PID, 10*time.Second)
}

func TestPackagedHelmAfterStartReadFailurePreservesOwner(t *testing.T) {
	for _, operation := range []string{"Create", "Update"} {
		t.Run(operation, func(t *testing.T) {
			stage, _ := stagePackagedPlugins(t)
			f := newPackagedFixture(t, packagedKubeconfig(t))
			f.lifetime = time.Hour
			a := startPackagedAgent(t, stage, f, time.Second)
			name := "await-retry-" + randomSuffix()
			request := packagedChartRequest(t, f, name, 300)
			if operation == "Update" {
				first := a.call(t, gen.PID{}, "Create", request)
				require.Equal(t, resource.OperationStatusInProgress, first.Value.(plugin.TrackedProgress).OperationStatus)
				awaitPackagedTerminalSuccess(t, a, first.PID, 10*time.Second)
			}
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			var workerStarted, failed atomic.Bool
			f.mu.Lock()
			f.gate = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/secrets") {
					workerStarted.Store(true)
					select {
					case <-release:
					case <-r.Context().Done():
						return true
					}
				}
				if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/secrets") && workerStarted.Load() && failed.CompareAndSwap(false, true) {
					http.Error(w, "temporary storage failure", 500)
					return true
				}
				return false
			}
			f.mu.Unlock()
			var call any = request
			if operation == "Update" {
				// Change desired values so the existing record cannot take the settled path.
				var desired map[string]any
				require.NoError(t, json.Unmarshal(request.Properties, &desired))
				desired["values"] = map[string]string{"revision": "next"}
				data, _ := json.Marshal(desired)
				call = plugin.UpdateResource{Namespace: "K8S", ResourceType: packagedHelmType, Label: name, NativeID: "default/" + name, PriorProperties: request.Properties, DesiredProperties: data, TargetConfig: f.target()}
			}
			reply := a.call(t, gen.PID{}, operation, call)
			progress := reply.Value.(plugin.TrackedProgress)
			require.True(t, failed.Load(), "after-start await did not hit injected storage failure")
			require.Equal(t, resource.OperationStatusInProgress, progress.OperationStatus, progress.StatusMessage)
			require.Contains(t, progress.RequestID, "#")
			releaseOnce.Do(func() { close(release) })
			awaitPackagedTerminalSuccess(t, a, reply.PID, 10*time.Second)
		})
	}
}

func TestPackagedHelmUIDOutageReportsRetainedWorkerFailure(t *testing.T) {
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, packagedKubeconfig(t))
	f.lifetime = time.Hour
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var outage atomic.Bool
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if outage.Load() && r.URL.Path == "/api/v1/namespaces/kube-system" {
			http.Error(w, "UID unavailable", 500)
			return true
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/configmaps") {
			select {
			case <-release:
			case <-r.Context().Done():
				return true
			}
			http.Error(w, "mutation denied", 403)
			return true
		}
		return false
	}
	a := startPackagedAgent(t, stage, f, time.Second)
	first := a.call(t, gen.PID{}, "Create", packagedChartRequest(t, f, "uid-outcome-"+randomSuffix(), 300))
	p := first.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, p.OperationStatus)
	outage.Store(true)
	retry := awaitPackagedProgress(t, a, first.PID, resource.OperationStatusInProgress, 5*time.Second)
	require.Equal(t, p.RequestID, retry.RequestID)
	releaseOnce.Do(func() { close(release) })
	deadline := time.After(10 * time.Second)
	for {
		select {
		case update := <-a.updates:
			if update.PID != first.PID {
				continue
			}
			result, ok := update.Value.(plugin.TrackedProgress)
			if !ok {
				continue
			}
			if result.OperationStatus == resource.OperationStatusFailure {
				require.Contains(t, result.StatusMessage, "Helm operation failed")
				return
			}
			require.Equal(t, resource.OperationStatusInProgress, result.OperationStatus)
		case <-deadline:
			t.Fatal("UID outage hid retained terminal outcome")
		}
	}
}
