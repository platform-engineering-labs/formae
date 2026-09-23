// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestWaitForShutdownSignalCompletesBeforeStop(t *testing.T) {
	signals := make(chan os.Signal, 1)
	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	stopped := make(chan struct{})
	done := make(chan struct{})
	var callbackCalls atomic.Int32
	var stopCalls atomic.Int32

	go func() {
		waitForShutdownSignal(signals, func() {
			callbackCalls.Add(1)
			close(callbackStarted)
			<-releaseCallback
		}, func() {
			stopCalls.Add(1)
			close(stopped)
		})
		close(done)
	}()

	signals <- syscall.SIGTERM
	waitForTestSignal(t, callbackStarted, "pre-stop callback to start")

	select {
	case <-stopped:
		t.Fatal("stop ran before the pre-stop callback completed")
	default:
	}

	close(releaseCallback)
	waitForTestSignal(t, stopped, "stop after callback completion")
	waitForTestSignal(t, done, "shutdown sequence to return")

	if got := callbackCalls.Load(); got != 1 {
		t.Fatalf("pre-stop callback called %d times, want 1", got)
	}
	if got := stopCalls.Load(); got != 1 {
		t.Fatalf("stop called %d times, want 1", got)
	}
}

func TestWaitForShutdownSignalAllowsNilBeforeStop(t *testing.T) {
	signals := make(chan os.Signal, 1)
	signals <- syscall.SIGINT

	stopCalls := 0
	waitForShutdownSignal(signals, nil, func() {
		stopCalls++
	})

	if stopCalls != 1 {
		t.Fatalf("stop called %d times, want 1", stopCalls)
	}
}

func waitForTestSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}
