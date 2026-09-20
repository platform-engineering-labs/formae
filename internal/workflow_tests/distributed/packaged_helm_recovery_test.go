// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

func TestPackagedHelmRestartRecoveryExcludesCompetingWorker(t *testing.T) {
	kubeconfig := packagedKubeconfig(t)
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, kubeconfig)
	entered, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/configmaps") {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-r.Context().Done():
			}
			// This request belongs to the stopped process. Never let cleanup
			// release it into the upstream after the client was killed.
			return true
		}
		return false
	}
	a := startPackagedAgent(t, stage, f, 30*time.Second)
	request := packagedChartRequest(t, f, "restart-"+randomSuffix(), 300)
	first := a.call(t, gen.PID{}, "Create", request)
	progress := first.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, progress.OperationStatus, "%s", progress.StatusMessage)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never reached object barrier")
	}
	require.NoError(t, a.m.Node.SendExit(first.PID, gen.TerminateReasonNormal))
	assertNoPackagedMints(t, f, 18*time.Second)
	a.stop()
	releaseOnce.Do(func() { close(release) })
	assertNoPackagedMints(t, f, 3*time.Second)
	out, err := runOwnedKubectl(kubeconfig, "get", "secrets", "-l", "owner=helm,name="+request.Label, "-o", "jsonpath={.items[*].metadata.labels.status}")
	require.NoError(t, err)
	require.Equal(t, "pending-install", string(out), "shutdown with expired credentials may honestly leave pending state")
	readEntered, readRelease := make(chan struct{}), make(chan struct{})
	var readOnce, readReleaseOnce sync.Once
	defer readReleaseOnce.Do(func() { close(readRelease) })
	f.mu.Lock()
	f.lifetime = time.Hour
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/secrets") {
			readOnce.Do(func() { close(readEntered) })
			select {
			case <-readRelease:
			case <-r.Context().Done():
				return true
			}
		}
		return false
	}
	f.mu.Unlock()
	fresh := startPackagedAgent(t, stage, f, 30*time.Second)
	replies := independentPackagedCall(t, fresh, "Status", packagedStatus(progress.RequestID, f.target(), resource.OperationCreate))
	select {
	case <-readEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("fresh Status never reached recovery read")
	}
	before, beforeRequests, _ := f.counts()
	contender := fresh.call(t, gen.PID{}, "Create", request)
	cp := contender.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationErrorCodeResourceConflict, cp.ErrorCode, "recovery lookup must hold ownership: %s", cp.StatusMessage)
	require.NoError(t, fresh.m.Node.SendExit(contender.PID, gen.TerminateReasonNormal))
	after, afterRequests, _ := f.counts()
	require.Equal(t, before, after, "contender reuses the same identity cache for its live UID lookup")
	require.Equal(t, beforeRequests+1, afterRequests, "contender must only read the live UID while recovery owns the flight")
	f.mu.Lock()
	probe := f.requests[beforeRequests]
	f.mu.Unlock()
	require.Equal(t, "GET", probe.Method)
	require.Equal(t, "/api/v1/namespaces/kube-system", probe.Path)
	readReleaseOnce.Do(func() { close(readRelease) })
	recovered := receivePackaged(t, replies, 10*time.Second)
	rp := recovered.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusFailure, rp.OperationStatus, "missing object must recover as failed, not deployed")
	require.Contains(t, rp.StatusMessage, "abandoned")
	require.NoError(t, fresh.m.Node.SendExit(recovered.PID, gen.TerminateReasonNormal))
	out, err = runOwnedKubectl(kubeconfig, "get", "secrets", "-l", "owner=helm,name="+request.Label, "-o", "jsonpath={.items[*].metadata.labels.status}")
	require.NoError(t, err)
	require.Equal(t, "failed", string(out))
	retry := fresh.call(t, gen.PID{}, "Create", request)
	retryProgress := retry.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, retryProgress.OperationStatus, "%s", retryProgress.StatusMessage)
	require.Contains(t, retryProgress.RequestID, "@2:upgrade#")
	require.NotEqual(t, progress.RequestID, retryProgress.RequestID)
	awaitPackagedTerminalSuccess(t, fresh, retry.PID, 35*time.Second)
	t.Logf("fresh process recovered persisted pending revision and completed %s", retryProgress.RequestID)
}

func TestPackagedHelmSuccessSurvivesStatusReadFailure(t *testing.T) {
	kubeconfig := packagedKubeconfig(t)
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, kubeconfig)
	f.lifetime = time.Hour
	objectEntered, objectRelease := make(chan struct{}), make(chan struct{})
	var objectOnce, objectReleaseOnce sync.Once
	defer objectReleaseOnce.Do(func() { close(objectRelease) })
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/configmaps") {
			objectOnce.Do(func() { close(objectEntered) })
			select {
			case <-objectRelease:
			case <-r.Context().Done():
				return true
			}
		}
		return false
	}
	a := startPackagedAgent(t, stage, f, 30*time.Second)
	request := packagedChartRequest(t, f, "retain-read-"+randomSuffix(), 300)
	first := a.call(t, gen.PID{}, "Create", request)
	p := first.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, p.OperationStatus)
	require.NoError(t, a.m.Node.SendExit(first.PID, gen.TerminateReasonNormal))
	select {
	case <-objectEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never reached object barrier")
	}
	readEntered, readRelease := make(chan struct{}), make(chan struct{})
	var readOnce, readReleaseOnce sync.Once
	defer readReleaseOnce.Do(func() { close(readRelease) })
	f.mu.Lock()
	previous := f.gate
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/secrets") {
			readOnce.Do(func() { close(readEntered) })
			select {
			case <-readRelease:
			case <-r.Context().Done():
				return true
			}
			http.Error(w, "injected transient storage failure", http.StatusInternalServerError)
			return true
		}
		return previous(w, r)
	}
	f.mu.Unlock()
	replies := independentPackagedCall(t, a, "Status", packagedStatus(p.RequestID, f.target(), resource.OperationCreate))
	select {
	case <-readEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("Status never entered read")
	}
	objectReleaseOnce.Do(func() { close(objectRelease) })
	require.Eventually(t, func() bool {
		out, e := runOwnedKubectl(kubeconfig, "get", "secrets", "-l", "owner=helm,name="+request.Label, "-o", "jsonpath={.items[*].metadata.labels.status}")
		return e == nil && string(out) == "deployed"
	}, 5*time.Second, 50*time.Millisecond)
	readReleaseOnce.Do(func() { close(readRelease) })
	failed := receivePackaged(t, replies, 10*time.Second)
	fp := failed.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, fp.OperationStatus)
	require.Equal(t, p.RequestID, fp.RequestID)
	require.NoError(t, a.m.Node.SendExit(failed.PID, gen.TerminateReasonNormal))
	f.mu.Lock()
	f.gate = nil
	f.mu.Unlock()
	result := a.call(t, gen.PID{}, "Status", packagedStatus(p.RequestID, f.target(), resource.OperationCreate)).Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusSuccess, result.OperationStatus, "retained success after failed read: %s", result.StatusMessage)
	t.Logf("terminal success for %s survived a concurrent storage read failure", p.RequestID)
}
