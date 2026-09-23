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
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

func TestPackagedHelmSlowStatusAndPollingGap(t *testing.T) {
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, packagedKubeconfig(t))
	f.lifetime = time.Hour
	entered, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var armed, failed atomic.Bool
	statusStarted := make(chan time.Time, 1)
	retried := make(chan time.Time, 1)
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/configmaps") {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-r.Context().Done():
				return true
			}
		}
		if armed.Load() && r.URL.Path == "/api/v1/namespaces/kube-system" {
			if !failed.Load() {
				select {
				case statusStarted <- time.Now():
				default:
				}
				select {
				case <-time.After(25 * time.Second):
				case <-r.Context().Done():
					return true
				}
			} else {
				select {
				case retried <- time.Now():
				default:
				}
			}
		}
		if armed.Load() && !failed.Load() && r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/secrets") {
			select {
			case <-time.After(24 * time.Second):
			case <-r.Context().Done():
				return true
			}
			failed.Store(true)
			// Even this formerly retryable SDK throttling error must stay InProgress
			// while the worker lives; exhausting MaxRetries must not orphan it.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(500)
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","status":"Failure","message":"Throttling: injected slow Status","reason":"InternalError","code":500}`)
			return true
		}
		return false
	}
	a := startPackagedAgent(t, stage, f, 30*time.Second, func(cfg *model.Config) { cfg.Agent.Retry.RetryDelay = 30 * time.Second; cfg.Agent.Retry.MaxRetries = 0 })
	request := packagedChartRequest(t, f, "throttle-"+randomSuffix(), 300)
	first := a.call(t, gen.PID{}, "Create", request)
	require.Equal(t, resource.OperationStatusInProgress, first.Value.(plugin.TrackedProgress).OperationStatus)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	armed.Store(true)
	var start time.Time
	select {
	case start = <-statusStarted:
	case <-time.After(35 * time.Second):
		t.Fatal("first scheduled Status absent")
	}
	progress := awaitPackagedProgress(t, a, first.PID, resource.OperationStatusInProgress, 55*time.Second)
	elapsed := time.Since(start)
	require.Empty(t, progress.ErrorCode)
	require.Equal(t, first.Value.(plugin.TrackedProgress).RequestID, progress.RequestID)
	require.Greater(t, elapsed, 47*time.Second)
	require.Less(t, elapsed, 53*time.Second)
	callbackEnded := time.Now()
	assertNoPackagedMints(t, f, 28*time.Second)
	var retry time.Time
	select {
	case retry = <-retried:
	case <-time.After(5 * time.Second):
		t.Fatal("SDK did not poll after configured interval")
	}
	gap := retry.Sub(callbackEnded)
	require.Greater(t, gap, 29*time.Second)
	require.Less(t, gap, 33*time.Second)
	awaitPackagedProgress(t, a, first.PID, resource.OperationStatusInProgress, 5*time.Second)
	releaseOnce.Do(func() { close(release) })
	awaitPackagedTerminalSuccess(t, a, first.PID, 35*time.Second)
	t.Logf("real Status callback %s then configured polling gap %s; no broker calls in first28s of gap", elapsed, gap)
}
