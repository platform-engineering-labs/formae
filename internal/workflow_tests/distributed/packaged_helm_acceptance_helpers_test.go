// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

func packagedKubeconfig(t *testing.T) string {
	t.Helper()
	path := os.Getenv("FORMAE_PACKAGED_KUBECONFIG")
	if path == "" {
		t.Skip("set FORMAE_PACKAGED_KUBECONFIG to a task-owned kind config")
	}
	return path
}
func packagedChartRequest(t *testing.T, f *packagedFixture, name string, timeout int) plugin.CreateResource {
	t.Helper()
	chart := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(chart, "templates"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "Chart.yaml"), []byte("apiVersion: v2\nname: acceptance\nversion: 0.1.0\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "templates", "object.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+name+"\n"), 0600))
	props, err := json.Marshal(map[string]any{"metadata": map[string]string{"name": name, "namespace": "default"}, "chart": chart, "timeoutSeconds": timeout})
	require.NoError(t, err)
	return plugin.CreateResource{Namespace: "K8S", ResourceType: packagedHelmType, Label: name, Properties: props, TargetConfig: f.target()}
}
func packagedStatus(id string, target json.RawMessage, op resource.Operation) plugin.ResumeWaitingForResource {
	return plugin.ResumeWaitingForResource{Namespace: "K8S", ResourceOperation: op, Request: plugin.PluginOperatorCheckStatus{Namespace: "K8S", ResourceType: packagedHelmType, RequestID: id, TargetConfig: target, ResourceOperation: op}}
}
func awaitPackagedProgress(t *testing.T, a *packagedAgent, pid gen.PID, status resource.OperationStatus, timeout time.Duration) plugin.TrackedProgress {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case update := <-a.updates:
			if update.PID != pid {
				continue
			}
			result, ok := update.Value.(plugin.TrackedProgress)
			if !ok {
				continue
			}
			require.Equal(t, status, result.OperationStatus, "operator %v: %s", pid, result.StatusMessage)
			return result
		case <-timer.C:
			t.Fatal("operator did not report callback result")
			return plugin.TrackedProgress{}
		}
	}
}
func independentPackagedCall(t *testing.T, a *packagedAgent, op string, request any) <-chan packagedReply {
	t.Helper()
	pid, err := a.m.Node.Spawn(func() gen.ProcessBehavior { return &packagedRequester{updates: make(chan packagedProgress, 100)} }, gen.ProcessOptions{})
	require.NoError(t, err)
	replies := make(chan packagedReply, 1)
	require.NoError(t, a.m.Node.Send(pid, packagedCall{Operation: op, Request: request, Reply: replies}))
	return replies
}
func receivePackaged(t *testing.T, replies <-chan packagedReply, timeout time.Duration) packagedReply {
	t.Helper()
	select {
	case result := <-replies:
		require.NoError(t, result.Err)
		return result
	case <-time.After(timeout):
		t.Fatal("asynchronous packaged callback timed out")
		return packagedReply{}
	}
}
func assertPackagedReadOnly(t *testing.T, f *packagedFixture) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		require.Equal(t, "GET", r.Method, "unexpected mutation %s", r.Path)
	}
}

func awaitPackagedListing(t *testing.T, a *packagedAgent, pid gen.PID, timeout time.Duration) plugin.Listing {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case update := <-a.updates:
			if update.PID != pid {
				continue
			}
			if listing, ok := update.Value.(plugin.Listing); ok {
				return listing
			}
		case <-timer.C:
			t.Fatal("operator did not send listing")
			return plugin.Listing{}
		}
	}
}
