// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package profile_test

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// minimalProfile returns the content of a minimal PKL profile that points the
// CLI at the given host (e.g. "http://127.0.0.1") and port.
func minimalProfile(host string, port int) string {
	return fmt.Sprintf(`amends "formae:/Config.pkl"

cli {
    api {
        url  = %q
        port = %d
    }
}
`, host, port)
}

// closedPort allocates a free TCP port, immediately closes the listener, and
// returns host + port. Nothing will be listening on it by the time the caller
// uses the values.
func closedPort(t *testing.T) (string, int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("closedPort: %v", err)
	}
	addr := l.Addr().(*net.TCPAddr)
	_ = l.Close()
	return "http://" + addr.IP.String(), addr.Port
}

// stubServer wraps an httptest.Server that records every request path and
// returns 200 OK with an empty JSON body.
type stubServer struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits []string
}

func newStubServer(t *testing.T) *stubServer {
	t.Helper()
	s := &stubServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.Path)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stubServer) clearHits() {
	s.mu.Lock()
	s.hits = nil
	s.mu.Unlock()
}

func (s *stubServer) copyHits() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.hits))
	copy(out, s.hits)
	return out
}

func (s *stubServer) port() int {
	return s.srv.Listener.Addr().(*net.TCPAddr).Port
}

// setupRoutingProfileDir creates a cfgDir containing:
//   - profiles/live.pkl  → stub HTTP server (controlled endpoint)
//   - profiles/dead.pkl  → a closed port (nothing listening)
func setupRoutingProfileDir(t *testing.T, stub *stubServer) string {
	t.Helper()
	cfgDir := t.TempDir()
	profDir := filepath.Join(cfgDir, "profiles")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatalf("mkdir profiles: %v", err)
	}

	deadHost, deadPort := closedPort(t)

	livePKL := minimalProfile("http://127.0.0.1", stub.port())
	deadPKL := minimalProfile(deadHost, deadPort)

	if err := os.WriteFile(filepath.Join(profDir, "live.pkl"), []byte(livePKL), 0o644); err != nil {
		t.Fatalf("write live.pkl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profDir, "dead.pkl"), []byte(deadPKL), 0o644); err != nil {
		t.Fatalf("write dead.pkl: %v", err)
	}
	return cfgDir
}

func setActive(t *testing.T, cfgDir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cfgDir, "active"), []byte(name), 0o644); err != nil {
		t.Fatalf("write active: %v", err)
	}
}

func assertStubHit(t *testing.T, stub *stubServer, want string) {
	t.Helper()
	for _, h := range stub.copyHits() {
		if h == want {
			return
		}
	}
	t.Errorf("stub: expected a hit on %q; recorded hits: %v", want, stub.copyHits())
}

func assertNoStubHits(t *testing.T, stub *stubServer) {
	t.Helper()
	if hits := stub.copyHits(); len(hits) > 0 {
		t.Errorf("stub: expected no hits but got: %v", hits)
	}
}

// TestProfileRouting proves that --profile and the active pointer both control
// which agent endpoint the CLI connects to, and that --profile is a one-shot
// override that never mutates the active pointer.
//
// CMD: "status agent" — the lightest agent-connecting subcommand; it calls
// client.Stats() (GET /api/v1/stats) with no side-effects and produces a clean
// connection-refused error on a bad endpoint.
//
// This test proves *routing*, not command success: the stub returns an empty
// body, so the CLI's version check rejects it and exits non-zero even on the
// "live" path. That is intentional — a stub cannot return a version-matched
// agent response. Routing is proven by whether the request reaches the stub
// (assertStubHit) versus failing to connect (assertNoStubHits + "agent is not
// running"), so the live-path exit code is deliberately not asserted here. The
// real end-to-end success path is covered by the e2e test
// (tests/e2e/go/profile_routing_test.go) against an actual agent.
func TestProfileRouting(t *testing.T) {
	bin := buildFormae(t)
	stub := newStubServer(t)
	cfgDir := setupRoutingProfileDir(t, stub)

	const statsPath = "/api/v1/stats"

	t.Run("routing via --profile flag", func(t *testing.T) {
		// --profile live → CLI must connect to the stub (hit /api/v1/stats).
		stub.clearHits()
		_, _, _ = run(t, bin, cfgDir, "status", "agent", "--profile", "live")
		assertStubHit(t, stub, statsPath)

		// --profile dead → CLI must fail with connection error, stub not touched.
		stub.clearHits()
		_, stderr, code := run(t, bin, cfgDir, "status", "agent", "--profile", "dead")
		if code == 0 {
			t.Errorf("expected non-zero exit for dead endpoint, got 0")
		}
		if !strings.Contains(stderr, "agent is not running") {
			t.Errorf("expected 'agent is not running' in stderr, got: %q", stderr)
		}
		assertNoStubHits(t, stub)
	})

	t.Run("routing via active pointer", func(t *testing.T) {
		// active=live → no --profile flag needed, stub must be hit.
		setActive(t, cfgDir, "live")
		stub.clearHits()
		_, _, _ = run(t, bin, cfgDir, "status", "agent")
		assertStubHit(t, stub, statsPath)

		// active=dead → connection must fail, stub not touched.
		setActive(t, cfgDir, "dead")
		stub.clearHits()
		_, stderr, code := run(t, bin, cfgDir, "status", "agent")
		if code == 0 {
			t.Errorf("expected non-zero exit for dead endpoint, got 0")
		}
		if !strings.Contains(stderr, "agent is not running") {
			t.Errorf("expected 'agent is not running' in stderr, got: %q", stderr)
		}
		assertNoStubHits(t, stub)
	})

	t.Run("one-shot override does not mutate active pointer", func(t *testing.T) {
		setActive(t, cfgDir, "live")

		// Override to dead for this one call — must fail.
		stub.clearHits()
		_, _, code := run(t, bin, cfgDir, "status", "agent", "--profile", "dead")
		if code == 0 {
			t.Errorf("expected non-zero exit when overriding to dead, got 0")
		}
		assertNoStubHits(t, stub)

		// Active pointer must still be "live"; the override must NOT mutate it.
		out, _, code := run(t, bin, cfgDir, "profile", "current")
		if code != 0 {
			t.Fatalf("profile current after one-shot override: code=%d", code)
		}
		if got := strings.TrimSpace(out); got != "live" {
			t.Errorf("active after one-shot override = %q, want %q", got, "live")
		}

		// A subsequent no-flag call must still route to live (stub hit).
		stub.clearHits()
		_, _, _ = run(t, bin, cfgDir, "status", "agent")
		assertStubHit(t, stub, statsPath)
	})

	t.Run("--config and --profile are mutually exclusive", func(t *testing.T) {
		_, stderr, code := run(t, bin, cfgDir, "status", "agent", "--profile", "live", "--config", t.TempDir()+"/x.pkl")
		if code == 0 {
			t.Errorf("expected non-zero exit when both --profile and --config are set, got 0")
		}
		lower := strings.ToLower(stderr)
		if !strings.Contains(lower, "config") || !strings.Contains(lower, "profile") {
			t.Errorf("expected both 'config' and 'profile' in stderr, got: %q", stderr)
		}
	})
}
