// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

// Catches starting a fresh C-minus10 allowance after plugin version preflight.
// Actual SDK callback performs 15s version discovery, then a blocked chart GET;
// the sum must still return by 50s and must never start a Helm mutation.
func TestPackagedHelmWholeCallbackBudget(t *testing.T) {
	kubeconfig := os.Getenv("FORMAE_PACKAGED_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set FORMAE_PACKAGED_KUBECONFIG to a task-owned kind config")
	}
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, kubeconfig)
	var once sync.Once
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/version" {
			once.Do(func() {
				select {
				case <-time.After(15 * time.Second):
				case <-r.Context().Done():
				}
			})
		}
		return false
	}
	chartStarted := make(chan struct{})
	var chartOnce sync.Once
	chart := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chartOnce.Do(func() { close(chartStarted) })
		<-r.Context().Done()
	}))
	defer chart.Close()
	a := startPackagedAgent(t, stage, f, 30*time.Second)
	props, _ := json.Marshal(map[string]any{"metadata": map[string]string{"name": "budget-probe", "namespace": "default"}, "chart": chart.URL + "/chart.tgz", "timeoutSeconds": 300})
	start := time.Now()
	reply := a.call(t, gen.PID{}, "Create", plugin.CreateResource{Namespace: "K8S", ResourceType: packagedHelmType, Label: "budget-probe", Properties: props, TargetConfig: f.target()})
	elapsed := time.Since(start)
	result, ok := reply.Value.(plugin.TrackedProgress)
	require.True(t, ok)
	require.Equal(t, resource.OperationStatusFailure, result.OperationStatus)
	select {
	case <-chartStarted:
	default:
		t.Fatal("callback never reached chart retrieval")
	}
	require.Less(t, elapsed, 55*time.Second, "preflight plus chart retrieval must share one 50s callback allowance")
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		require.Equal(t, "GET", r.Method, "exhausted preparation must not mutate %s", r.Path)
	}
	t.Logf("preflight plus blocked chart returned in %s", elapsed)
}

// No broker launch means no trusted binding. This must reject at the actual
// Plugin boundary before version discovery, UID lookup, chart or storage I/O.
func TestPackagedHelmMissingMetadataBeforeNetwork(t *testing.T) {
	kubeconfig := os.Getenv("FORMAE_PACKAGED_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set FORMAE_PACKAGED_KUBECONFIG to a task-owned kind config")
	}
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, kubeconfig)
	a := startPackagedAgent(t, stage, f, 30*time.Second, func(cfg *model.Config) { cfg.Agent.OidcCredentialPlugins[0].Enabled = false })
	reply := a.call(t, gen.PID{}, "Create", plugin.CreateResource{Namespace: "K8S", ResourceType: packagedHelmType, Label: "missing-metadata", Properties: json.RawMessage(`{"metadata":{"name":"missing-metadata","namespace":"default"},"chart":"invalid","timeoutSeconds":300}`), TargetConfig: f.target()})
	result, ok := reply.Value.(plugin.TrackedProgress)
	require.True(t, ok)
	require.Equal(t, resource.OperationStatusFailure, result.OperationStatus)
	require.Contains(t, result.StatusMessage, "trusted operation metadata")
	mint, requests, _ := f.counts()
	require.Zero(t, mint)
	require.Zero(t, requests)
}

func TestPackagedHelmShortTimeoutBeforeNetwork(t *testing.T) {
	kubeconfig := os.Getenv("FORMAE_PACKAGED_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set FORMAE_PACKAGED_KUBECONFIG to a task-owned kind config")
	}
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, kubeconfig)
	a := startPackagedAgent(t, stage, f, 30*time.Second)
	reply := a.call(t, gen.PID{}, "Create", plugin.CreateResource{Namespace: "K8S", ResourceType: packagedHelmType, Label: "short-timeout", Properties: json.RawMessage(`{"metadata":{"name":"short-timeout","namespace":"default"},"chart":"invalid","timeoutSeconds":1}`), TargetConfig: f.target()})
	result := reply.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusFailure, result.OperationStatus)
	require.Contains(t, result.StatusMessage, "timeoutSeconds")
	mint, requests, _ := f.counts()
	require.Zero(t, mint, "insufficient action timeout must reject before preflight authentication")
	require.Zero(t, requests)
}
