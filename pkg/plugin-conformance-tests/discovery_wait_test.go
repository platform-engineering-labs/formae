// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeClock is a deterministic clock for the discovery wait loop tests. Sleep
// advances the clock instantly, so the loop runs in zero real time and timing
// assertions are exact.
type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time        { return c.now }
func (c *fakeClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }

var clockBase = time.Unix(0, 0).UTC()

func TestWaitForResource(t *testing.T) {
	t.Run("found only after a later trigger", func(t *testing.T) {
		clk := &fakeClock{now: clockBase}
		triggers := 0
		trigger := func() error { triggers++; return nil }
		// The resource only becomes visible after a second scan — the exact
		// regression: a single trigger (today's behaviour) would never see it.
		check := func() (bool, error) { return triggers >= 2, nil }

		err := waitForResource(clk, t.Logf, 100*time.Second,
			backoffConfig{initial: 10 * time.Second, max: 60 * time.Second},
			2*time.Second, trigger, check)
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if triggers < 2 {
			t.Errorf("expected at least 2 discovery triggers, got %d", triggers)
		}
	})

	t.Run("re-trigger backoff schedule grows and caps", func(t *testing.T) {
		clk := &fakeClock{now: clockBase}
		var firedAt []time.Duration
		trigger := func() error {
			firedAt = append(firedAt, clk.Now().Sub(clockBase))
			return nil
		}
		check := func() (bool, error) { return false, nil }

		_ = waitForResource(clk, t.Logf, 200*time.Second,
			backoffConfig{initial: 10 * time.Second, max: 60 * time.Second},
			5*time.Second, trigger, check)

		want := []time.Duration{0, 10, 30, 70, 130, 190}
		for i := range want {
			want[i] *= time.Second
		}
		if len(firedAt) != len(want) {
			t.Fatalf("expected triggers at %v, got %v", want, firedAt)
		}
		for i := range want {
			if firedAt[i] != want[i] {
				t.Errorf("trigger %d fired at %v, want %v (all: %v)", i, firedAt[i], want[i], firedAt)
			}
		}
	})

	t.Run("timeout with no discovery reports the attempt count", func(t *testing.T) {
		clk := &fakeClock{now: clockBase}
		triggers := 0
		trigger := func() error { triggers++; return nil }
		check := func() (bool, error) { return false, nil }

		err := waitForResource(clk, t.Logf, 20*time.Second,
			backoffConfig{initial: 10 * time.Second, max: 60 * time.Second},
			5*time.Second, trigger, check)
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if !strings.Contains(err.Error(), "timeout") {
			t.Errorf("expected a timeout error, got: %v", err)
		}
		// The count must surface so a slow-to-propagate case is diagnosable.
		if !strings.Contains(err.Error(), "2 discovery trigger") {
			t.Errorf("expected trigger attempt count (%d) in error, got: %v", triggers, err)
		}
		if strings.Contains(err.Error(), "could not trigger discovery") {
			t.Errorf("successful triggers must not produce a trigger-specific error: %v", err)
		}
	})

	t.Run("all triggers fail returns a trigger-specific error", func(t *testing.T) {
		clk := &fakeClock{now: clockBase}
		trigger := func() error { return errors.New("admin endpoint unreachable") }
		check := func() (bool, error) { return false, nil }

		err := waitForResource(clk, t.Logf, 20*time.Second,
			backoffConfig{initial: 10 * time.Second, max: 60 * time.Second},
			5*time.Second, trigger, check)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "could not trigger discovery") {
			t.Errorf("expected a trigger-specific error, got: %v", err)
		}
		if !strings.Contains(err.Error(), "admin endpoint unreachable") {
			t.Errorf("expected the last trigger error to be carried, got: %v", err)
		}
	})

	t.Run("a trigger error is non-fatal", func(t *testing.T) {
		clk := &fakeClock{now: clockBase}
		triggers := 0
		trigger := func() error {
			triggers++
			if triggers == 1 {
				return errors.New("transient hiccup")
			}
			return nil
		}
		check := func() (bool, error) { return triggers >= 2, nil }

		err := waitForResource(clk, t.Logf, 100*time.Second,
			backoffConfig{initial: 10 * time.Second, max: 60 * time.Second},
			2*time.Second, trigger, check)
		if err != nil {
			t.Fatalf("a transient first-trigger error must not abort the wait, got: %v", err)
		}
	})

	t.Run("no re-trigger inside the near-deadline guard", func(t *testing.T) {
		clk := &fakeClock{now: clockBase}
		triggers := 0
		trigger := func() error { triggers++; return nil }
		check := func() (bool, error) { return false, nil }

		// Next re-trigger would fall due at t=8s, but only 1s remains before the
		// 9s deadline — below the ~one-poll-interval scan grace, so it is skipped.
		err := waitForResource(clk, t.Logf, 9*time.Second,
			backoffConfig{initial: 8 * time.Second, max: 60 * time.Second},
			2*time.Second, trigger, check)
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if triggers != 1 {
			t.Errorf("expected only the t=0 trigger (near-deadline guard), got %d", triggers)
		}
	})
}

func TestGetDiscoveryRetriggerBackoff(t *testing.T) {
	tests := []struct {
		name        string
		env         string
		wantInitial time.Duration
	}{
		{"unset uses default", "", discoveryRetriggerDefault},
		{"valid value honoured", "30", 30 * time.Second},
		{"below floor clamps up", "1", discoveryRetriggerFloor},
		{"above cap clamps down", "120", discoveryRetriggerCap},
		{"zero rejected -> default", "0", discoveryRetriggerDefault},
		{"negative rejected -> default", "-5", discoveryRetriggerDefault},
		{"non-numeric rejected -> default", "abc", discoveryRetriggerDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// An empty value is treated as unset by the parser (-> default).
			t.Setenv("FORMAE_TEST_DISCOVERY_RETRIGGER_INTERVAL", tt.env)

			got := getDiscoveryRetriggerBackoff()
			if got.initial != tt.wantInitial {
				t.Errorf("initial = %v, want %v", got.initial, tt.wantInitial)
			}
			if got.max != discoveryRetriggerCap {
				t.Errorf("max = %v, want %v", got.max, discoveryRetriggerCap)
			}
		})
	}
}
