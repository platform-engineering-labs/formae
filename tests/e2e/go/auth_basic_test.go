// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestAuthBasic(t *testing.T) {
	bin := FormaeBinary(t)

	password := "e2e-test-pass"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to generate bcrypt hash: %v", err)
	}

	agent := StartAgent(t, bin, WithAuth("e2e-user", password, string(hash)))
	baseURL := fmt.Sprintf("http://localhost:%d", agent.Port())

	// Wait for the auth plugin to finish initializing. The plugin process
	// is spawned during agent startup but the RPC handshake completes
	// asynchronously. Poll until a valid-credentials request returns 200
	// (not 503 "auth plugin unavailable").
	waitForAuthReady(t, baseURL, "e2e-user", password, 30*time.Second)

	t.Run("health endpoint requires no auth", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/v1/health")
		if err != nil {
			t.Fatalf("health request failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 for health, got %d", resp.StatusCode)
		}
	})

	t.Run("unauthenticated request returns 401", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/v1/stats")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusUnauthorized {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 401, got %d: %s", resp.StatusCode, string(body))
		}
	})

	t.Run("valid credentials return 200", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/v1/stats", nil)
		req.Header.Set("Authorization", basicAuth("e2e-user", password))

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
		}
	})

	t.Run("invalid password returns 401", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/v1/stats", nil)
		req.Header.Set("Authorization", basicAuth("e2e-user", "wrong-password"))

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})

	t.Run("unknown user returns 401", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/v1/stats", nil)
		req.Header.Set("Authorization", basicAuth("unknown-user", password))

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})
}

func basicAuth(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

// waitForAuthReady polls an authenticated endpoint until the auth plugin
// is initialized and returning 200 for valid credentials.
func waitForAuthReady(t *testing.T, baseURL, username, password string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/v1/stats", nil)
		req.Header.Set("Authorization", basicAuth(username, password))

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("auth plugin not ready after %v", timeout)
}
