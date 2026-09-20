// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"context"
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

func TestPackagedHelmQueuedServiceUsesCallbackBudget(t *testing.T) {
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, packagedKubeconfig(t))
	f.lifetime = time.Hour
	f.repeatToken = true
	objectEntered, objectRelease := make(chan struct{}), make(chan struct{})
	challenge := make(chan struct{})
	var challenged atomic.Bool
	var objectOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(objectRelease) })
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/configmaps") {
			objectOnce.Do(func() { close(objectEntered) })
			if challenged.CompareAndSwap(false, true) {
				select {
				case <-challenge:
					http.Error(w, "refresh requested", 401)
					return true
				case <-objectRelease:
				case <-r.Context().Done():
					return true
				}
			}
			select {
			case <-objectRelease:
			case <-r.Context().Done():
				return true
			}
		}
		return false
	}
	a := startPackagedAgent(t, stage, f, 30*time.Second)
	request := packagedChartRequest(t, f, "queued-"+randomSuffix(), 300)
	first := a.call(t, gen.PID{}, "Create", request)
	p := first.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, p.OperationStatus)
	require.NoError(t, a.m.Node.SendExit(first.PID, gen.TerminateReasonNormal))
	select {
	case <-objectEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker barrier not reached")
	}
	uidEntered := make(chan int, 3)
	uidRelease := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	var uidCount atomic.Int32
	defer func() {
		for _, ch := range uidRelease {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
	}()
	f.mu.Lock()
	previous := f.gate
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/api/v1/namespaces/kube-system" {
			n := int(uidCount.Add(1)) - 1
			if n < 3 {
				uidEntered <- n
				select {
				case <-uidRelease[n]:
				case <-r.Context().Done():
					return true
				}
			}
		}
		return previous(w, r)
	}
	f.mu.Unlock()
	start := time.Now()
	one := independentPackagedCall(t, a, "Status", packagedStatus(p.RequestID, f.target(), resource.OperationCreate))
	<-uidEntered
	two := independentPackagedCall(t, a, "Status", packagedStatus(p.RequestID, f.target(), resource.OperationCreate))
	<-uidEntered
	three := independentPackagedCall(t, a, "Status", packagedStatus(p.RequestID, f.target(), resource.OperationCreate))
	<-uidEntered
	close(challenge)
	beforeMints, _, _ := f.counts()
	mintEntered := make(chan struct{}, 2)
	f.mu.Lock()
	f.mintGate = func(ctx context.Context) bool {
		select {
		case mintEntered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return false
	}
	f.mu.Unlock()
	// All three callbacks have authenticated before the serial credential actor is
	// blocked. Their remaining allowance includes waiting for bridge Service.
	time.Sleep(25 * time.Second)
	close(uidRelease[0])
	select {
	case <-mintEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("first Service did not mint")
	}
	close(uidRelease[1])
	close(uidRelease[2])
	r1 := receivePackaged(t, one, 35*time.Second)
	r2 := receivePackaged(t, two, 20*time.Second)
	r3 := receivePackaged(t, three, 5*time.Second)
	elapsed := time.Since(start)
	t.Logf("queued callbacks returned in %s: one=%+v two=%+v", elapsed, r1.Value, r2.Value)
	for _, reply := range []packagedReply{r1, r2, r3} {
		require.Equal(t, resource.OperationStatusInProgress, reply.Value.(plugin.TrackedProgress).OperationStatus)
		require.Equal(t, p.RequestID, reply.Value.(plugin.TrackedProgress).RequestID)
		require.NoError(t, a.m.Node.SendExit(reply.PID, gen.TerminateReasonNormal))
	}
	require.Greater(t, elapsed, 32*time.Second)
	require.Less(t, elapsed, 55*time.Second)
	afterMints, _, _ := f.counts()
	require.Equal(t, 2, afterMints-beforeMints, "third queued service has insufficient remaining budget and must not mint")
	f.mu.Lock()
	f.mintGate = nil
	f.gate = previous
	f.mu.Unlock()
	join := a.call(t, gen.PID{}, "Create", request)
	jp := join.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, jp.OperationStatus)
	require.Equal(t, p.RequestID, jp.RequestID, "timed-out service waiter must retain original worker")
	require.NoError(t, a.m.Node.SendExit(join.PID, gen.TerminateReasonNormal))
	releaseOnce.Do(func() { close(objectRelease) })
	t.Logf("queued service callback canceled at %s; repeated identical JWT retained same worker generation", elapsed)
}

func TestPackagedHelmWorker401RefreshWithSameJWT(t *testing.T) {
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, packagedKubeconfig(t))
	f.lifetime = time.Hour
	f.repeatToken = true
	var unauthorized atomic.Int32
	var observations atomic.Int32
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		// Lazy worker discovery is contextless inside Helm; its adapter must queue
		// the refresh to the live callback after this deliberately injected 401.
		if r.Method == "GET" && r.URL.Path == "/api" {
			observations.Add(1)
			if unauthorized.CompareAndSwap(0, 1) {
				http.Error(w, "retry once", 401)
				return true
			}
		}
		return false
	}
	a := startPackagedAgent(t, stage, f, 2*time.Second)
	request := packagedChartRequest(t, f, "worker401-"+randomSuffix(), 300)
	first := a.call(t, gen.PID{}, "Create", request)
	p := first.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, p.OperationStatus, "%s", p.StatusMessage)
	awaitPackagedTerminalSuccess(t, a, first.PID, 10*time.Second)
	require.Equal(t, int32(1), unauthorized.Load())
	require.GreaterOrEqual(t, observations.Load(), int32(2))
	_, _, expired := f.counts()
	require.Zero(t, expired)
}

// A broker returning the same now-expired JWT cannot extend its usable life.
// A later valid mint can service the retained generation without a new worker.
func TestPackagedHelmRepeatedExpiredJWTDoesNotExtendFlightToken(t *testing.T) {
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, packagedKubeconfig(t))
	f.repeatToken = true
	entered, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
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
	request := packagedChartRequest(t, f, "same-expired-"+randomSuffix(), 300)
	first := a.call(t, gen.PID{}, "Create", request)
	p := first.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, p.OperationStatus)
	require.NoError(t, a.m.Node.SendExit(first.PID, gen.TerminateReasonNormal))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker barrier not reached")
	}
	assertNoPackagedMints(t, f, 18*time.Second)
	rejected := a.call(t, gen.PID{}, "Status", packagedStatus(p.RequestID, f.target(), resource.OperationCreate))
	rp := rejected.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusFailure, rp.OperationStatus)
	require.NoError(t, a.m.Node.SendExit(rejected.PID, gen.TerminateReasonNormal))
	_, _, expired := f.counts()
	require.Zero(t, expired, "expired repeated token must not reach HTTP")
	f.mu.Lock()
	f.repeatToken = false
	f.mu.Unlock()
	resumed := a.call(t, gen.PID{}, "Create", request)
	sp := resumed.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, sp.OperationStatus)
	require.Equal(t, p.RequestID, sp.RequestID)
	require.NoError(t, a.m.Node.SendExit(resumed.PID, gen.TerminateReasonNormal))
}
