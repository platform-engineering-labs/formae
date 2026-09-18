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
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

// Completing a worker while a matching Create re-drive services credentials
// must return the actual outcome, not the bridge's context-canceled wakeup.
func TestPackagedHelmRejoinCompletion(t *testing.T) {
	kubeconfig := os.Getenv("FORMAE_PACKAGED_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set FORMAE_PACKAGED_KUBECONFIG to a task-owned kind config")
	}
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, kubeconfig)
	f.lifetime = 70 * time.Second
	objectEntered, objectRelease := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer func() {
		select {
		case <-objectRelease:
		default:
			close(objectRelease)
		}
	}()
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/configmaps") {
			once.Do(func() { close(objectEntered) })
			select {
			case <-objectRelease:
			case <-r.Context().Done():
				return true
			}
		}
		return false
	}
	a := startPackagedAgent(t, stage, f, 30*time.Second)
	name := "rejoin-" + randomSuffix()
	chart := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(chart, "templates"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "Chart.yaml"), []byte("apiVersion: v2\nname: rejoin\nversion: 0.1.0\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "templates", "object.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+name+"\n"), 0600))
	props, _ := json.Marshal(map[string]any{"metadata": map[string]string{"name": name, "namespace": "default"}, "chart": chart, "timeoutSeconds": 300})
	request := plugin.CreateResource{Namespace: "K8S", ResourceType: packagedHelmType, Label: name, Properties: props, TargetConfig: f.target()}
	first := a.call(t, gen.PID{}, "Create", request)
	progress := first.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, progress.OperationStatus, "initial: %s", progress.StatusMessage)
	select {
	case <-objectEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not enter object barrier")
	}
	mintEntered, mintRelease := make(chan struct{}), make(chan struct{})
	var mintOnce sync.Once
	defer func() {
		select {
		case <-mintRelease:
		default:
			close(mintRelease)
		}
	}()
	f.mu.Lock()
	previous := f.gate
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/api/v1/namespaces/kube-system" {
			select {
			case <-time.After(11 * time.Second):
			case <-r.Context().Done():
				return true
			}
		}
		return previous(w, r)
	}
	f.mintGate = func(ctx context.Context) bool {
		mintOnce.Do(func() { close(mintEntered) })
		select {
		case <-mintRelease:
			return true
		case <-ctx.Done():
			return false
		}
	}
	f.mu.Unlock()
	replies := make(chan packagedReply, 1)
	require.NoError(t, a.m.Node.Send(a.requester, packagedCall{Operation: "Create", Request: request, Reply: replies}))
	select {
	case <-mintEntered:
	case <-time.After(16 * time.Second):
		t.Fatal("matching re-drive did not enter credential service")
	}
	close(objectRelease)
	// Real Helm completion is externally visible as a deployed Secret. No private
	// SDK fields or flight hooks are consulted; kubectl is limited to owned kind.
	require.Eventually(t, func() bool {
		out, err := runOwnedKubectl(kubeconfig, "get", "secrets", "-n", "default", "-l", "owner=helm,name="+name, "-o", "jsonpath={.items[0].metadata.labels.status}")
		return err == nil && string(out) == "deployed"
	}, 5*time.Second, 50*time.Millisecond)
	// The stored status precedes worker return; hold the external mint long
	// enough for its ordinary return path, keeping the total under broker's10s.
	time.Sleep(100 * time.Millisecond)
	close(mintRelease)
	select {
	case reply := <-replies:
		require.NoError(t, reply.Err)
		result := reply.Value.(plugin.TrackedProgress)
		require.NotEqual(t, resource.OperationStatusFailure, result.OperationStatus, "completed rejoin: %s", result.StatusMessage)
		require.Equal(t, progress.RequestID, result.RequestID)
	case <-time.After(10 * time.Second):
		t.Fatal("re-drive failed to return")
	}
}
