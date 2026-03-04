// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/platform-engineering-labs/formae/pkg/auth"
)

func basicAuth(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

func hashPassword(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(hash)
}

func makeConfig(t *testing.T) json.RawMessage {
	t.Helper()
	hash := hashPassword(t, "pass123")
	cfg := fmt.Sprintf(`{
		"Username": "admin",
		"Password": "pass123",
		"AuthorizedUsers": [
			{"Username": "admin", "Password": %q}
		]
	}`, hash)
	return json.RawMessage(cfg)
}

func TestAuthBasic_Init(t *testing.T) {
	plugin := &AuthBasic{}
	cfg := makeConfig(t)

	var resp auth.InitResponse
	err := plugin.Init(&auth.InitRequest{Config: cfg}, &resp)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("Init returned error: %s", resp.Error)
	}
}

func TestAuthBasic_Init_BadConfig(t *testing.T) {
	plugin := &AuthBasic{}

	var resp auth.InitResponse
	err := plugin.Init(&auth.InitRequest{Config: json.RawMessage(`{invalid}`)}, &resp)
	if err != nil {
		t.Fatalf("Init should not return Go error, got: %v", err)
	}
	if resp.Error == "" {
		t.Fatal("expected error in response for bad config")
	}
}

func TestAuthBasic_Validate(t *testing.T) {
	plugin := &AuthBasic{}
	cfg := makeConfig(t)

	var initResp auth.InitResponse
	plugin.Init(&auth.InitRequest{Config: cfg}, &initResp)

	t.Run("valid credentials", func(t *testing.T) {
		headers := map[string][]string{
			"Authorization": {basicAuth("admin", "pass123")},
		}
		var resp auth.ValidateResponse
		err := plugin.Validate(&auth.ValidateRequest{Headers: headers}, &resp)
		if err != nil {
			t.Fatalf("Validate failed: %v", err)
		}
		if !resp.Valid {
			t.Fatalf("expected Valid=true, got error: %s", resp.Error)
		}
		if resp.CacheKey == "" {
			t.Fatal("expected non-empty CacheKey")
		}
		if resp.CacheTTL < time.Second {
			t.Fatalf("expected CacheTTL >= 1s, got %v", resp.CacheTTL)
		}
	})

	t.Run("invalid password", func(t *testing.T) {
		headers := map[string][]string{
			"Authorization": {basicAuth("admin", "wrongpass")},
		}
		var resp auth.ValidateResponse
		err := plugin.Validate(&auth.ValidateRequest{Headers: headers}, &resp)
		if err != nil {
			t.Fatalf("Validate failed: %v", err)
		}
		if resp.Valid {
			t.Fatal("expected Valid=false for wrong password")
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		headers := map[string][]string{
			"Authorization": {basicAuth("unknown", "pass123")},
		}
		var resp auth.ValidateResponse
		err := plugin.Validate(&auth.ValidateRequest{Headers: headers}, &resp)
		if err != nil {
			t.Fatalf("Validate failed: %v", err)
		}
		if resp.Valid {
			t.Fatal("expected Valid=false for unknown user")
		}
	})

	t.Run("missing auth header", func(t *testing.T) {
		headers := map[string][]string{}
		var resp auth.ValidateResponse
		err := plugin.Validate(&auth.ValidateRequest{Headers: headers}, &resp)
		if err != nil {
			t.Fatalf("Validate failed: %v", err)
		}
		if resp.Valid {
			t.Fatal("expected Valid=false for missing header")
		}
	})
}

func TestAuthBasic_GetAuthHeader(t *testing.T) {
	plugin := &AuthBasic{}
	cfg := makeConfig(t)

	var initResp auth.InitResponse
	plugin.Init(&auth.InitRequest{Config: cfg}, &initResp)

	var resp auth.GetAuthHeaderResponse
	err := plugin.GetAuthHeader(&auth.GetAuthHeaderRequest{}, &resp)
	if err != nil {
		t.Fatalf("GetAuthHeader failed: %v", err)
	}

	authHeaders := resp.Headers["Authorization"]
	if len(authHeaders) != 1 {
		t.Fatalf("expected 1 Authorization header, got %d", len(authHeaders))
	}
	if authHeaders[0] != basicAuth("admin", "pass123") {
		t.Fatalf("unexpected Authorization header: %s", authHeaders[0])
	}
}
