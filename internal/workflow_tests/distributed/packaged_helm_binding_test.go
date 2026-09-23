// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

// This is an actual supervisor restart/re-pair, not a fabricated SDK binding.
// Config hot reload is not a production interface: replacement changes both
// broker name and opaque config, while identical restarts retain both.
func TestPackagedHelmTrustedBrokerRestartAndReplacement(t *testing.T) {
	kubeconfig := packagedKubeconfig(t)
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, kubeconfig)
	f.lifetime = time.Hour
	oldDir := filepath.Join(stage, "test-oidc-broker", "v0.0.1")
	newDir := filepath.Join(stage, "replacement-broker", "v0.0.1")
	require.NoError(t, os.MkdirAll(newDir, 0755))
	for _, file := range []string{"test-oidc-broker", "formae-plugin.pkl", "schema/Config.pkl"} {
		data, err := os.ReadFile(filepath.Join(oldDir, file))
		require.NoError(t, err)
		if file == "formae-plugin.pkl" {
			data = []byte(strings.ReplaceAll(string(data), "test-oidc-broker", "replacement-broker"))
		}
		dest := file
		if file == "test-oidc-broker" {
			dest = "replacement-broker"
		}
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(newDir, dest)), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(newDir, dest), data, 0755))
	}
	replacementRelease, oldRelease := make(chan struct{}), make(chan struct{})
	var replacementOnce, oldOnce sync.Once
	defer replacementOnce.Do(func() { close(replacementRelease) })
	defer oldOnce.Do(func() { close(oldRelease) })
	var oldStarts atomic.Int32
	replacementStarted := make(chan struct{}, 1)
	restartHeld := make(chan struct{}, 1)
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/replacement" {
			select {
			case replacementStarted <- struct{}{}:
			default:
			}
			select {
			case <-replacementRelease:
			case <-r.Context().Done():
				return
			}
		}
		if r.URL.Path == "/original" && oldStarts.Add(1) > 2 {
			select {
			case restartHeld <- struct{}{}:
			default:
			}
			select {
			case <-oldRelease:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(control.Close)
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	var rejectCachedUID atomic.Bool
	defer releaseOnce.Do(func() { close(release) })
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/api/v1/namespaces/kube-system" && rejectCachedUID.CompareAndSwap(true, false) {
			http.Error(w, "refresh after broker restart", http.StatusUnauthorized)
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
	a := startPackagedAgent(t, stage, f, 30*time.Second, func(cfg *model.Config) {
		original, _ := json.Marshal(map[string]string{"controlUrl": f.broker.URL, "startupUrl": control.URL + "/original"})
		replacement, _ := json.Marshal(map[string]string{"controlUrl": f.broker.URL, "startupUrl": control.URL + "/replacement"})
		cfg.Agent.OidcCredentialPlugins = []model.OidcCredentialPluginUserConfig{{Type: "test-oidc-broker", Enabled: true, PluginConfig: original}, {Type: "replacement-broker", Enabled: true, PluginConfig: replacement}}
	})
	request := packagedChartRequest(t, f, "binding-"+randomSuffix(), 300)
	first := a.call(t, gen.PID{}, "Create", request)
	p := first.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, p.OperationStatus)
	require.NoError(t, a.m.Node.SendExit(first.PID, gen.TerminateReasonNormal))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	select {
	case <-replacementStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement broker not supervised")
	}
	oldPID := ownedPackagedPID(t, filepath.Join(oldDir, "test-oidc-broker"))
	require.NoError(t, oldPID.Kill())
	require.Eventually(t, func() bool { return oldStarts.Load() == 2 }, 5*time.Second, 20*time.Millisecond)
	// Force a real401 refresh so a valid shared token cannot make the restart
	// proof pass without invoking the newly registered broker launch.
	time.Sleep(300 * time.Millisecond)
	beforeRestartMint, _, _ := f.counts()
	rejectCachedUID.Store(true)
	same := a.call(t, gen.PID{}, "Create", request)
	sp := same.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, sp.OperationStatus, "identical supervised restart: %s", sp.StatusMessage)
	require.Equal(t, p.RequestID, sp.RequestID)
	afterRestartMint, _, _ := f.counts()
	require.Equal(t, 1, afterRestartMint-beforeRestartMint, "restart must refresh through current callback broker")
	require.NoError(t, a.m.Node.SendExit(same.PID, gen.TerminateReasonNormal))
	restartedPID := ownedPackagedPID(t, filepath.Join(oldDir, "test-oidc-broker"))
	require.NotEqual(t, oldPID.Pid, restartedPID.Pid)
	require.NoError(t, restartedPID.Kill())
	select {
	case <-restartHeld:
	case <-time.After(5 * time.Second):
		t.Fatal("original restart did not enter Configure barrier")
	}
	replacementOnce.Do(func() { close(replacementRelease) })
	// Registration is asynchronous after Configure. Wait for its own log rather
	// than bypassing the supervisor's trusted launch and announcement handshake.
	require.True(t, setupTestLogger().WaitForLog("name=replacement-broker", 5*time.Second))
	time.Sleep(300 * time.Millisecond)
	beforeMint, beforeRequests, _ := f.counts()
	foreign := a.call(t, gen.PID{}, "Create", request)
	fp := foreign.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationErrorCodeResourceConflict, fp.ErrorCode, "replacement binding: %s", fp.StatusMessage)
	require.NoError(t, a.m.Node.SendExit(foreign.PID, gen.TerminateReasonNormal))
	afterMint, afterRequests, _ := f.counts()
	require.Equal(t, 1, afterMint-beforeMint, "replacement binding must have an independent credential cache")
	require.Equal(t, 2, afterRequests-beforeRequests, "only version and live UID reads precede conflict")
	f.mu.Lock()
	observed := append([]apiObservation(nil), f.requests[beforeRequests:]...)
	f.mu.Unlock()
	for _, r := range observed {
		require.Equal(t, "GET", r.Method)
		require.Contains(t, []string{"/version", "/api/v1/namespaces/kube-system"}, r.Path)
	}
	foreignStatus := a.call(t, gen.PID{}, "Status", packagedStatus(p.RequestID, f.target(), resource.OperationCreate))
	fs := foreignStatus.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationErrorCodeResourceConflict, fs.ErrorCode)
	require.NoError(t, a.m.Node.SendExit(foreignStatus.PID, gen.TerminateReasonNormal))
	// A second foreign callback must still conflict: none may consume the flight.
	again := a.call(t, gen.PID{}, "Create", request)
	require.Equal(t, resource.OperationErrorCodeResourceConflict, again.Value.(plugin.TrackedProgress).ErrorCode)
	require.NoError(t, a.m.Node.SendExit(again.PID, gen.TerminateReasonNormal))
	f.mu.Lock()
	mutations := 0
	for _, r := range f.requests {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/secrets") {
			mutations++
		}
	}
	f.mu.Unlock()
	require.Equal(t, 1, mutations)
	t.Logf("same-name/config broker PID %d -> %d retained generation; replacement name+config rejected without bridge service", oldPID.Pid, restartedPID.Pid)
	a.stop()
}

func ownedPackagedPID(t *testing.T, executable string) *os.Process {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	require.NoError(t, err)
	for _, entry := range entries {
		pid, e := strconv.Atoi(entry.Name())
		if e != nil {
			continue
		}
		path, e := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		if e == nil && path == executable {
			p, e := os.FindProcess(pid)
			require.NoError(t, e)
			return p
		}
	}
	t.Fatalf("owned executable not running: %s", executable)
	return nil
}
