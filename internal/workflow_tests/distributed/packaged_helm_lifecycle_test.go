// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"encoding/base64"
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

// Catches detached broker minting, loss of the idle operator, duplicate Helm
// workers on a real coordinator re-drive, and premature pre-record success.
func TestPackagedHelmLifecycle(t *testing.T) {
	kubeconfig := os.Getenv("FORMAE_PACKAGED_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set FORMAE_PACKAGED_KUBECONFIG to a task-owned kind config")
	}
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, kubeconfig)
	crdEntered, crdRelease := make(chan struct{}), make(chan struct{})
	hookEntered, hookRelease := make(chan struct{}), make(chan struct{})
	var crdOnce, hookOnce sync.Once
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/customresourcedefinitions") {
			crdOnce.Do(func() { close(crdEntered) })
			select {
			case <-crdRelease:
			case <-r.Context().Done():
				return true
			}
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/configmaps") {
			hookOnce.Do(func() { close(hookEntered) })
			select {
			case <-hookRelease:
			case <-r.Context().Done():
				return true
			}
		}
		return false
	}
	// Defers run before fixture Cleanup, releasing any failed-test requests.
	defer func() {
		select {
		case <-crdRelease:
		default:
			close(crdRelease)
		}
		select {
		case <-hookRelease:
		default:
			close(hookRelease)
		}
	}()
	a := startPackagedAgent(t, stage, f, 30*time.Second)
	name := "packaged-" + strings.ToLower(randomSuffix())
	chart := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(chart, "crds"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(chart, "templates"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "Chart.yaml"), []byte("apiVersion: v2\nname: packaged\nversion: 0.1.0\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "crds", "probe.yaml"), []byte("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: "+name+"s.packaged.test\nspec:\n  group: packaged.test\n  scope: Namespaced\n  names:\n    plural: "+name+"s\n    singular: "+name+"\n    kind: PackagedProbe\n  versions:\n  - name: v1\n    served: true\n    storage: true\n    schema:\n      openAPIV3Schema:\n        type: object\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "templates", "hook.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+name+"\n  annotations:\n    helm.sh/hook: post-install\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "templates", "object.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+name+"-object\n"), 0600))
	props, _ := json.Marshal(map[string]any{"metadata": map[string]string{"name": name, "namespace": "default"}, "chart": chart, "timeoutSeconds": 300})
	request := plugin.CreateResource{Namespace: "K8S", ResourceType: packagedHelmType, Label: name, Properties: props, TargetConfig: f.target()}
	start := time.Now()
	first := a.call(t, gen.PID{}, "Create", request)
	elapsed := time.Since(start)
	progress, ok := first.Value.(plugin.TrackedProgress)
	require.True(t, ok, "unexpected result %T", first.Value)
	require.Equal(t, resource.OperationStatusInProgress, progress.OperationStatus, "callback: %s", progress.StatusMessage)
	require.Contains(t, progress.RequestID, "#")
	require.Less(t, elapsed, 55*time.Second)
	select {
	case <-crdEntered:
	default:
		t.Fatal("Create did not start the real Helm CRD path")
	}
	_, requests, expired := f.counts()
	require.Zero(t, expired)
	require.Positive(t, requests)
	assertNoPackagedMints(t, f, 18*time.Second)
	close(crdRelease)
	// The scheduled Status belongs to the original operator; the requester stays
	// linked and alive, and no second operator has been spawned yet.
	select {
	case <-hookEntered:
	case <-time.After(25 * time.Second):
		t.Fatal("same-operator Status did not unblock lazy worker discovery")
	}
	// Observe the callback result before measuring an idle interval. Reaching
	// the worker hook alone does not establish that Status has returned.
	awaitPackagedProgress(t, a, first.PID, resource.OperationStatusInProgress, 10*time.Second)
	assertNoPackagedMints(t, f, 18*time.Second)
	second := a.call(t, gen.PID{}, "Create", request)
	again, ok := second.Value.(plugin.TrackedProgress)
	require.True(t, ok)
	require.NotEqual(t, first.PID, second.PID)
	require.Equal(t, resource.OperationStatusInProgress, again.OperationStatus, "re-drive: %s", again.StatusMessage)
	require.Equal(t, progress.RequestID, again.RequestID)

	// Desired changes and a legacy selector for the same physical UID must
	// conflict without a second Helm revision or feeding the original worker.
	changed := request
	var changedProps map[string]any
	require.NoError(t, json.Unmarshal(props, &changedProps))
	changedProps["values"] = map[string]any{"different": true}
	changed.Properties, _ = json.Marshal(changedProps)
	conflict := a.call(t, gen.PID{}, "Create", changed)
	cp, ok := conflict.Value.(plugin.TrackedProgress)
	require.True(t, ok)
	require.Equal(t, resource.OperationErrorCodeResourceConflict, cp.ErrorCode)
	require.NoError(t, a.m.Node.SendExit(conflict.PID, gen.TerminateReasonNormal))
	legacy := request
	legacy.TargetConfig, _ = json.Marshal(map[string]any{"Auth": map[string]string{"Type": "Kubeconfig", "Kubeconfig": kubeconfig}})
	legacyResult := a.call(t, gen.PID{}, "Create", legacy)
	lp, ok := legacyResult.Value.(plugin.TrackedProgress)
	require.True(t, ok)
	require.Equal(t, resource.OperationErrorCodeResourceConflict, lp.ErrorCode, "same UID through kubeconfig must share exclusion: %s", lp.StatusMessage)
	require.NoError(t, a.m.Node.SendExit(legacyResult.PID, gen.TerminateReasonNormal))

	for _, kind := range []string{"audience", "ca"} {
		probe := request
		var target map[string]map[string]string
		require.NoError(t, json.Unmarshal(probe.TargetConfig, &target))
		if kind == "audience" {
			target["Auth"]["Audience"] = packagedOtherAudience
		} else {
			ca, e := base64.StdEncoding.DecodeString(target["Auth"]["CertificateAuthority"])
			require.NoError(t, e)
			target["Auth"]["CertificateAuthority"] = base64.StdEncoding.EncodeToString(append(ca, '\n'))
		}
		probe.TargetConfig, _ = json.Marshal(target)
		beforeMint, beforeRequests, _ := f.counts()
		reply := a.call(t, gen.PID{}, "Create", probe)
		rp := reply.Value.(plugin.TrackedProgress)
		require.Equal(t, resource.OperationErrorCodeResourceConflict, rp.ErrorCode, "%s identity mismatch: %s", kind, rp.StatusMessage)
		require.NoError(t, a.m.Node.SendExit(reply.PID, gen.TerminateReasonNormal))
		afterMint, afterRequests, _ := f.counts()
		require.Equal(t, 1, afterMint-beforeMint, "new identity shares one credential for discovery and UID, without servicing the foreign bridge")
		require.Equal(t, 2, afterRequests-beforeRequests, "foreign identity must perform only discovery and live UID reads")
		f.mu.Lock()
		observed := append([]apiObservation(nil), f.requests[beforeRequests:]...)
		f.mu.Unlock()
		for _, r := range observed {
			require.Equal(t, "GET", r.Method)
			require.Contains(t, []string{"/version", "/api/v1/namespaces/kube-system"}, r.Path)
		}
	}
	for _, id := range []string{strings.Split(progress.RequestID, "#")[0], strings.Replace(progress.RequestID, "@1:", "@2:", 1), strings.Replace(progress.RequestID, ":install", ":delete", 1), strings.Split(progress.RequestID, "#")[0] + "#aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"} {
		check := plugin.ResumeWaitingForResource{Namespace: "K8S", ResourceOperation: resource.OperationCreate, Request: plugin.PluginOperatorCheckStatus{Namespace: "K8S", ResourceType: packagedHelmType, RequestID: id, TargetConfig: f.target(), ResourceOperation: resource.OperationCreate}}
		reply := a.call(t, gen.PID{}, "Status", check)
		rp, ok := reply.Value.(plugin.TrackedProgress)
		require.True(t, ok)
		require.Equal(t, resource.OperationErrorCodeResourceConflict, rp.ErrorCode, "mismatched Status: %s", rp.StatusMessage)
		require.NoError(t, a.m.Node.SendExit(reply.PID, gen.TerminateReasonNormal))
	}

	verified := a.call(t, gen.PID{}, "Create", request)
	vp := verified.Value.(plugin.TrackedProgress)
	require.Equal(t, progress.RequestID, vp.RequestID, "mismatch probes must not clear the flight")
	require.NoError(t, a.m.Node.SendExit(verified.PID, gen.TerminateReasonNormal))
	close(hookRelease)
	deadline := time.After(40 * time.Second)
	completed := map[gen.PID]bool{}
completedCreate:
	for {
		select {
		case update := <-a.updates:
			if update.PID != first.PID && update.PID != second.PID {
				continue
			}
			result, ok := update.Value.(plugin.TrackedProgress)
			if !ok {
				continue
			}
			if result.OperationStatus == resource.OperationStatusSuccess {
				require.Equal(t, "default/"+name, result.NativeID)
				_, _, expired = f.counts()
				require.Zero(t, expired)
				t.Logf("actual packaged lifecycle passed; first callback %s, matching operators %v and %v", elapsed, first.PID, second.PID)
				completed[update.PID] = true
				if len(completed) == 2 {
					break completedCreate
				}
				continue
			}
			require.NotEqual(t, resource.OperationStatusFailure, result.OperationStatus, "Status: %s", result.StatusMessage)
		case <-deadline:
			t.Fatal("packaged Helm did not complete")
		}
	}
	deleteEntered, deleteRelease := make(chan struct{}), make(chan struct{})
	var deleteOnce sync.Once
	defer func() {
		select {
		case <-deleteRelease:
		default:
			close(deleteRelease)
		}
	}()
	f.mu.Lock()
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && strings.HasSuffix(r.URL.Path, "/configmaps/"+name+"-object") {
			deleteOnce.Do(func() { close(deleteEntered) })
			select {
			case <-deleteRelease:
			case <-r.Context().Done():
				return true
			}
		}
		return false
	}
	f.mu.Unlock()
	del := plugin.DeleteResource{Namespace: "K8S", ResourceType: packagedHelmType, NativeID: "default/" + name, TargetConfig: f.target()}
	deleted := a.call(t, gen.PID{}, "Delete", del)
	dp, ok := deleted.Value.(plugin.TrackedProgress)
	require.True(t, ok)
	require.Equal(t, resource.OperationStatusInProgress, dp.OperationStatus, "Delete: %s", dp.StatusMessage)
	select {
	case <-deleteEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("actual Uninstall.Run did not reach object deletion")
	}
	assertNoPackagedMints(t, f, 18*time.Second)

	pollDeadline := time.After(20 * time.Second)
waitDeletePoll:
	for {
		select {
		case update := <-a.updates:
			if update.PID == deleted.PID {
				result, ok := update.Value.(plugin.TrackedProgress)
				require.True(t, ok)
				require.Equal(t, resource.OperationStatusInProgress, result.OperationStatus)
				break waitDeletePoll
			}
		case <-pollDeadline:
			t.Fatal("original Delete operator did not poll while uninstall was blocked")
		}
	}
	redel := a.call(t, gen.PID{}, "Delete", del)
	rdp, ok := redel.Value.(plugin.TrackedProgress)
	require.True(t, ok)
	require.Equal(t, resource.OperationStatusInProgress, rdp.OperationStatus, "repeated Delete: %s", rdp.StatusMessage)
	require.Equal(t, dp.RequestID, rdp.RequestID)
	for _, id := range []string{strings.Split(dp.RequestID, "#")[0], strings.Replace(dp.RequestID, "@1:", "@2:", 1), strings.Replace(dp.RequestID, ":delete", ":install", 1), strings.Split(dp.RequestID, "#")[0] + "#aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"} {
		mismatch := a.call(t, gen.PID{}, "Status", packagedStatus(id, f.target(), resource.OperationDelete))
		require.Equal(t, resource.OperationErrorCodeResourceConflict, mismatch.Value.(plugin.TrackedProgress).ErrorCode)
		require.NoError(t, a.m.Node.SendExit(mismatch.PID, gen.TerminateReasonNormal))
	}
	foreignDelete := del
	foreignDelete.TargetConfig = []byte(strings.ReplaceAll(string(f.target()), packagedAudience, packagedOtherAudience))
	foreign := a.call(t, gen.PID{}, "Delete", foreignDelete)
	require.Equal(t, resource.OperationErrorCodeResourceConflict, foreign.Value.(plugin.TrackedProgress).ErrorCode)
	require.NoError(t, a.m.Node.SendExit(foreign.PID, gen.TerminateReasonNormal))
	retainedDelete := a.call(t, gen.PID{}, "Delete", del)
	require.Equal(t, dp.RequestID, retainedDelete.Value.(plugin.TrackedProgress).RequestID, "Delete mismatches must not consume generation")
	require.NoError(t, a.m.Node.SendExit(retainedDelete.PID, gen.TerminateReasonNormal))
	close(deleteRelease)
	deadline = time.After(40 * time.Second)
	for {
		select {
		case update := <-a.updates:
			if update.PID != deleted.PID && update.PID != redel.PID {
				continue
			}
			result, ok := update.Value.(plugin.TrackedProgress)
			if !ok {
				continue
			}
			if result.OperationStatus == resource.OperationStatusSuccess {
				_, _, expired = f.counts()

				require.Zero(t, expired)
				f.mu.Lock()
				secretCreates, objectDeletes := 0, 0
				for _, r := range f.requests {
					if r.Method == "POST" && strings.HasSuffix(r.Path, "/secrets") {
						secretCreates++
					}
					if r.Method == "DELETE" && strings.HasSuffix(r.Path, "/configmaps/"+name+"-object") {
						objectDeletes++
					}
				}
				f.mu.Unlock()
				require.Equal(t, 1, secretCreates, "re-drive must not create a second revision")
				require.Equal(t, 1, objectDeletes, "repeated Delete must not launch a second uninstall")
				a.stop()
				assertNoPackagedMints(t, f, 3*time.Second)
				return
			}
			require.NotEqual(t, resource.OperationStatusFailure, result.OperationStatus, "Delete Status: %s", result.StatusMessage)
		case <-deadline:
			t.Fatal("actual uninstall did not complete")
		}
	}
}
func randomSuffix() string { return strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "") }
func assertNoPackagedMints(t *testing.T, f *packagedFixture, d time.Duration) {
	t.Helper()
	before, _, _ := f.counts()
	timer := time.NewTimer(d)
	defer timer.Stop()
	<-timer.C
	after, _, expired := f.counts()
	require.Equal(t, before, after, "broker called with no active callback")
	require.Zero(t, expired, "expired bearer reached Kubernetes fixture")
}
