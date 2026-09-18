// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

// A test-only supervised broker. The external loopback controller owns tokens
// and observations so tests never inspect SDK-private operation contexts.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/platform-engineering-labs/formae/pkg/credential"
)

type broker struct{ controlURL, completionURL string }

func (b *broker) Configure(raw json.RawMessage) error {
	var cfg struct {
		ControlURL    string `json:"controlUrl"`
		StartupURL    string `json:"startupUrl"`
		CompletionURL string `json:"completionUrl"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return errors.New("invalid test broker configuration")
	}
	u, err := url.Parse(cfg.ControlURL)
	if err != nil || u.Scheme != "http" || u.User != nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return errors.New("test broker requires literal loopback HTTP controller")
	}
	if cfg.StartupURL != "" {
		startup, err := url.Parse(cfg.StartupURL)
		if err != nil || startup.Scheme != "http" || startup.User != nil || !net.ParseIP(startup.Hostname()).IsLoopback() {
			return errors.New("test broker requires literal loopback startup controller")
		}
		client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Get(cfg.StartupURL)
		if err != nil {
			return errors.New("startup controller unavailable")
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return errors.New("startup controller denied launch")
		}
	}
	if cfg.CompletionURL != "" {
		completion, err := url.Parse(cfg.CompletionURL)
		if err != nil || completion.Scheme != "http" || completion.User != nil || !net.ParseIP(completion.Hostname()).IsLoopback() {
			return errors.New("test broker requires literal loopback completion controller")
		}
	}
	b.controlURL = cfg.ControlURL
	b.completionURL = cfg.CompletionURL
	return nil
}

func (b *broker) IdentityToken(ctx context.Context, req *credential.OidcIdentityTokenRequest) (*credential.OidcIdentityTokenResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, errors.New("invalid mint request")
	}
	if b.completionURL != "" {
		// Test-only synchronous method-unwind barrier. This is separate from
		// the canceled mint HTTP request and must finish before method return.
		defer func() {
			client := &http.Client{Timeout: 200 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			response, err := client.Post(b.completionURL, "application/json", bytes.NewReader(body))
			if err == nil {
				response.Body.Close()
			}
		}()
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, b.controlURL, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid controller request")
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(r)
	if err != nil {
		return nil, errors.New("test controller unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("test controller denied mint")
	}
	var result credential.OidcIdentityTokenResult
	if json.NewDecoder(resp.Body).Decode(&result) != nil {
		return nil, errors.New("invalid test controller result")
	}
	return &result, nil
}

func main() {
	if credential.Run(&broker{}) != nil {
		os.Exit(1)
	}
}
