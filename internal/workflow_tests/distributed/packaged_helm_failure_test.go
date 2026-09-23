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

// A template failure after the original callback expired has no release Secret.
// A different generation must not consume it; original scheduled Status must
// return the precise retained error instead of treating missing storage as new.
func TestPackagedHelmPreRecordFailure(t *testing.T) {
	kubeconfig := os.Getenv("FORMAE_PACKAGED_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set FORMAE_PACKAGED_KUBECONFIG to a task-owned kind config")
	}
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, kubeconfig)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/customresourcedefinitions") {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-r.Context().Done():
				return true
			}
		}
		return false
	}
	a := startPackagedAgent(t, stage, f, 30*time.Second)
	name := "failure-" + randomSuffix()
	chart := writePackagedFailureChart(t, name, true)
	props, _ := json.Marshal(map[string]any{"metadata": map[string]string{"name": name, "namespace": "default"}, "chart": chart, "values": map[string]string{"failure": "retained-template-failure"}, "timeoutSeconds": 300})
	request := plugin.CreateResource{Namespace: "K8S", ResourceType: packagedHelmType, Label: name, Properties: props, TargetConfig: f.target()}
	first := a.call(t, gen.PID{}, "Create", request)
	progress := first.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, progress.OperationStatus, "Create: %s", progress.StatusMessage)
	select {
	case <-entered:
	default:
		t.Fatal("slow CRD never started")
	}

	t.Cleanup(func() {
		f.mu.Lock()
		requests := append([]apiObservation(nil), f.requests...)
		f.mu.Unlock()
		for _, r := range requests {
			require.False(t, r.Method == "POST" && strings.HasSuffix(r.Path, "/secrets"), "pre-record failure unexpectedly created storage")
		}
	})
	close(release)
	// A matching explicit Status supplies the expired bridge so Helm can finish
	// CRD establishment and fail template rendering; it is a real new operator.
	matching := plugin.ResumeWaitingForResource{Namespace: "K8S", ResourceOperation: resource.OperationCreate, Request: plugin.PluginOperatorCheckStatus{Namespace: "K8S", ResourceType: packagedHelmType, RequestID: progress.RequestID, TargetConfig: f.target(), ResourceOperation: resource.OperationCreate}}
	bad := matching
	bad.Request.RequestID = strings.Split(progress.RequestID, "#")[0] + "#aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	mismatch := a.call(t, gen.PID{}, "Status", bad)
	mp := mismatch.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationErrorCodeResourceConflict, mp.ErrorCode)
	require.NoError(t, a.m.Node.SendExit(mismatch.PID, gen.TerminateReasonNormal))

	resumed := a.call(t, gen.PID{}, "Status", matching)
	resumedProgress := resumed.Value.(plugin.TrackedProgress)
	if resumedProgress.OperationStatus == resource.OperationStatusFailure {
		require.Contains(t, resumedProgress.StatusMessage, "retained-template-failure")
		return
	}
	require.NoError(t, a.m.Node.SendExit(resumed.PID, gen.TerminateReasonNormal))
	deadline := time.After(70 * time.Second)
	for {
		select {
		case update := <-a.updates:
			if update.PID != first.PID {
				continue
			}
			result, ok := update.Value.(plugin.TrackedProgress)
			if !ok || result.OperationStatus == resource.OperationStatusInProgress {
				continue
			}
			require.Equal(t, resource.OperationStatusFailure, result.OperationStatus)
			require.Contains(t, result.StatusMessage, "retained-template-failure")
			f.mu.Lock()
			requests := append([]apiObservation(nil), f.requests...)
			f.mu.Unlock()
			for _, r := range requests {
				require.False(t, r.Method == "POST" && strings.HasSuffix(r.Path, "/secrets"), "template failure must precede release record")
			}
			return
		case <-deadline:
			t.Fatal("matching original Status did not report retained pre-record failure")
		}
	}
}

// The same packaged callback reports a synchronous template error, consumes
// that delivered outcome, then permits a corrected desired apply immediately.
func TestPackagedHelmSynchronousFailureAllowsRetry(t *testing.T) {
	kubeconfig := os.Getenv("FORMAE_PACKAGED_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set FORMAE_PACKAGED_KUBECONFIG to a task-owned kind config")
	}
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, kubeconfig)
	a := startPackagedAgent(t, stage, f, 30*time.Second)
	name := "sync-failure-" + randomSuffix()
	chart := writePackagedFailureChart(t, name, false)
	for _, failure := range []string{"original-template-failure", "corrected-template-ran"} {
		props, _ := json.Marshal(map[string]any{"metadata": map[string]string{"name": name, "namespace": "default"}, "chart": chart, "values": map[string]string{"failure": failure}, "timeoutSeconds": 300})
		reply := a.call(t, gen.PID{}, "Create", plugin.CreateResource{Namespace: "K8S", ResourceType: packagedHelmType, Label: name, Properties: props, TargetConfig: f.target()})
		result := reply.Value.(plugin.TrackedProgress)
		require.Equal(t, resource.OperationStatusFailure, result.OperationStatus)
		require.Contains(t, result.StatusMessage, failure)
	}
}

func writePackagedFailureChart(t *testing.T, name string, crd bool) string {
	t.Helper()
	chart := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(chart, "templates"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "Chart.yaml"), []byte("apiVersion: v2\nname: failure\nversion: 0.1.0\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "templates", "fail.yaml"), []byte(`{{ fail .Values.failure }}`), 0600))
	if crd {
		require.NoError(t, os.MkdirAll(filepath.Join(chart, "crds"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(chart, "crds", "probe.yaml"), []byte("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: "+name+"s.packaged.test\nspec:\n  group: packaged.test\n  scope: Namespaced\n  names:\n    plural: "+name+"s\n    singular: "+name+"\n    kind: FailureProbe\n  versions:\n  - name: v1\n    served: true\n    storage: true\n    schema:\n      openAPIV3Schema:\n        type: object\n"), 0600))
	}
	return chart
}
