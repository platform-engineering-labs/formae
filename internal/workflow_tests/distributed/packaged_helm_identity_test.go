// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"fmt"
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

func TestPackagedHelmUIDFailureAndReadListPermissions(t *testing.T) {
	kubeconfig := packagedKubeconfig(t)
	for _, mode := range []string{"denied", "missing"} {
		t.Run(mode, func(t *testing.T) {
			stage, _ := stagePackagedPlugins(t)
			f := newPackagedFixture(t, kubeconfig)
			f.lifetime = time.Hour
			f.gate = func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/api/v1/namespaces/kube-system" {
					if mode == "denied" {
						http.Error(w, "UID forbidden", 403)
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"kube-system"}}`)
					}
					return true
				}
				return false
			}
			a := startPackagedAgent(t, stage, f, 30*time.Second)
			request := packagedChartRequest(t, f, "uid-"+randomSuffix(), 300)
			p := a.call(t, gen.PID{}, "Create", request).Value.(plugin.TrackedProgress)
			require.Equal(t, resource.OperationStatusFailure, p.OperationStatus)
			require.Contains(t, p.StatusMessage, "UID")
			assertPackagedReadOnly(t, f)
			f.mu.Lock()
			before := len(f.requests)
			f.mu.Unlock()
			list := a.call(t, gen.PID{}, "List", plugin.ListResources{Namespace: "K8S", ResourceType: packagedHelmType, TargetConfig: f.target()})
			listing := awaitPackagedListing(t, a, list.PID, 10*time.Second)
			require.Empty(t, listing.Error)
			read := a.call(t, gen.PID{}, "Read", plugin.ReadResource{Namespace: "K8S", ResourceType: packagedHelmType, NativeID: "default/" + request.Label, TargetConfig: f.target(), IsSync: true})
			rp, ok := read.Value.(plugin.TrackedProgress)
			require.True(t, ok, "%T", read.Value)
			require.Equal(t, resource.OperationStatusSuccess, rp.OperationStatus, "%s", rp.StatusMessage)
			f.mu.Lock()
			observed := append([]apiObservation(nil), f.requests[before:]...)
			f.mu.Unlock()
			require.NotEmpty(t, observed)
			for _, r := range observed {
				require.NotEqual(t, "/api/v1/namespaces/kube-system", r.Path, "Read/List must not add UID permission")
				require.Equal(t, "GET", r.Method)
			}
		})
	}
}

func TestPackagedHelmPhysicalAliasesAndReplacedUID(t *testing.T) {
	kubeconfig := packagedKubeconfig(t)
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, kubeconfig)
	f.lifetime = time.Hour
	entered, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var replacement atomic.Bool
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if replacement.Load() && r.URL.Path == "/api/v1/namespaces/kube-system" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"kube-system","uid":"replacement-cluster"}}`)
			return true
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/configmaps") {
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
	request := packagedChartRequest(t, f, "aliases-"+randomSuffix(), 300)
	first := a.call(t, gen.PID{}, "Create", request)
	p := first.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, p.OperationStatus)
	require.NoError(t, a.m.Node.SendExit(first.PID, gen.TerminateReasonNormal))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never reached object barrier")
	}
	for _, alias := range []string{"localhost", "localhost.localdomain"} {
		changed := request
		changed.TargetConfig = []byte(strings.ReplaceAll(string(f.target()), "127.0.0.1", alias))
		beforeMints, beforeRequests, _ := f.counts()
		reply := a.call(t, gen.PID{}, "Create", changed)
		cp := reply.Value.(plugin.TrackedProgress)
		require.Equal(t, resource.OperationErrorCodeResourceConflict, cp.ErrorCode, "same UID alias %s: %s", alias, cp.StatusMessage)
		require.NoError(t, a.m.Node.SendExit(reply.PID, gen.TerminateReasonNormal))
		afterMints, afterRequests, _ := f.counts()
		require.Equal(t, 1, afterMints-beforeMints, "alias must reuse one credential cache for its authenticated probes")
		require.Equal(t, 2, afterRequests-beforeRequests, "conflicting alias may perform only version and live UID GETs")
		f.mu.Lock()
		for _, r := range f.requests[beforeRequests:] {
			require.Equal(t, "GET", r.Method)
			require.Contains(t, []string{"/version", "/api/v1/namespaces/kube-system"}, r.Path, "alias must not read/mutate foreign Helm state")
		}
		f.mu.Unlock()
	}
	// Replacing the authenticated UID at the identical URL must be re-read.
	// Return empty Helm storage for this protocol-fixture cluster, then hold its
	// first mutation so coexistence with the original worker is observable.
	replacement.Store(true)
	f.mu.Lock()
	previous := f.gate
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/secrets") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"SecretList","metadata":{},"items":[]}`)
			return true
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/secrets") {
			http.Error(w, "replacement storage reached", http.StatusForbidden)
			return true
		}
		return previous(w, r)
	}
	f.mu.Unlock()
	newer := a.call(t, gen.PID{}, "Create", request)
	np := newer.Value.(plugin.TrackedProgress)
	require.NotEqual(t, resource.OperationErrorCodeResourceConflict, np.ErrorCode, "same URL must not cache previous UID")
	require.Contains(t, np.StatusMessage, "replacement storage reached", "distinct UID must independently reach its own Helm mutation")
	replacement.Store(false)
	f.mu.Lock()
	f.gate = previous
	f.mu.Unlock()
	same := a.call(t, gen.PID{}, "Create", request)
	sp := same.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, sp.OperationStatus)
	require.Equal(t, p.RequestID, sp.RequestID, "new UID must not consume old UID flight")
	require.NoError(t, a.m.Node.SendExit(same.PID, gen.TerminateReasonNormal))
	// A different release on the original UID is independently admitted too.
	other := packagedChartRequest(t, f, "distinct-"+randomSuffix(), 300)
	distinct := a.call(t, gen.PID{}, "Create", other)
	dp := distinct.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, dp.OperationStatus)
	require.NotEqual(t, p.RequestID, dp.RequestID)
	require.NoError(t, a.m.Node.SendExit(distinct.PID, gen.TerminateReasonNormal))
	releaseOnce.Do(func() { close(release) })
	t.Logf("two DNS aliases excluded by shared UID; replaced UID at same URL admitted independently; original generation %s retained", p.RequestID)
}
