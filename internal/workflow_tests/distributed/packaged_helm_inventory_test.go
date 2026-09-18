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

func TestPackagedHelmInventoryAliasesAndBuilderMutationFence(t *testing.T) {
	kubeconfig := packagedKubeconfig(t)
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, kubeconfig)
	f.lifetime = time.Hour
	seed := "inventory-seed-" + randomSuffix()
	_, err := runOwnedKubectl(kubeconfig, "create", "configmap", seed, "--from-literal=x=y")
	require.NoError(t, err)
	t.Cleanup(func() { runOwnedKubectl(kubeconfig, "delete", "configmap", seed, "--ignore-not-found") })
	entered, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var mutated atomic.Bool
	var reads atomic.Int32
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasPrefix(r.Host, "localhost") && r.URL.Path == "/api/v1/namespaces/kube-system" {
			http.Error(w, "inventory has no UID permission", 403)
			return true
		}
		if r.Method == "GET" && r.URL.Path == "/api/v1/secrets" {
			reads.Add(1)
			if !mutated.Load() {
				if strings.HasPrefix(r.Host, "localhost.localdomain:") {
					once.Do(func() { close(entered) })
					select {
					case <-release:
					case <-r.Context().Done():
						return true
					}
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"apiVersion":"v1","kind":"SecretList","metadata":{},"items":[]}`)
				return true
			}
		}
		return false
	}
	a := startPackagedAgent(t, stage, f, 2*time.Second)
	listingRequest := func(alias string) plugin.ListResources {
		return plugin.ListResources{Namespace: "K8S", ResourceType: "K8S::Core::ConfigMap", TargetConfig: []byte(strings.ReplaceAll(string(f.target()), "127.0.0.1", alias)), ListParameters: map[string]plugin.ListParam{"namespace": {ListParam: "namespace", ListValue: "default"}}}
	}
	cacheStart := time.Now()
	cached := a.call(t, gen.PID{}, "List", listingRequest("localhost"))
	require.Empty(t, awaitPackagedListing(t, a, cached.PID, 10*time.Second).Error)
	blocked := a.call(t, gen.PID{}, "List", listingRequest("localhost.localdomain"))
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("inventory builder not reached")
	}
	request := packagedChartRequest(t, f, "inventory-chart-"+randomSuffix(), 300)
	created := a.call(t, gen.PID{}, "Create", request)
	require.Equal(t, resource.OperationStatusInProgress, created.Value.(plugin.TrackedProgress).OperationStatus)
	awaitPackagedTerminalSuccess(t, a, created.PID, 10*time.Second)
	mutated.Store(true)
	releaseOnce.Do(func() { close(release) })
	require.Empty(t, awaitPackagedListing(t, a, blocked.PID, 10*time.Second).Error)
	for _, alias := range []string{"localhost", "localhost.localdomain"} {
		before := reads.Load()
		next := a.call(t, gen.PID{}, "List", listingRequest(alias))
		listing := awaitPackagedListing(t, a, next.PID, 10*time.Second)
		require.Empty(t, listing.Error)
		require.Greater(t, reads.Load(), before, "mutation must invalidate alias and prevent stale builder publication")
		for _, r := range listing.Resources {
			require.NotEqual(t, "default/"+request.Label, r.NativeID, "new chart object must be collapsed")
		}
	}
	require.Less(t, time.Since(cacheStart), 30*time.Second, "TTL expiry must not explain cache misses")
	t.Log("mutation invalidated both DNS aliases; blocked pre-mutation builder did not republish; inventory succeeded without UID permission")
}
