// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultHubURL = "https://hub.platform.engineering"

const (
	hubDialTimeout           = 1 * time.Second
	hubTLSHandshakeTimeout   = 1 * time.Second
	hubResponseHeaderTimeout = 2 * time.Second
	hubTotalTimeout          = 3 * time.Second
)

type AvailabilityResult struct {
	Available     bool
	GitHubRepoURL string
}

type HubClient interface {
	CheckPluginAvailability(ctx context.Context, name string) (AvailabilityResult, error)
}

// HubUnreachableError signals a transient inability to talk to the hub.
// Callers downgrade to a warning. Trust and protocol-mismatch errors do
// NOT use this type — they bubble up as plain errors so the caller can
// hard-fail.
type HubUnreachableError struct{ Cause error }

func (e *HubUnreachableError) Error() string { return e.Cause.Error() }
func (e *HubUnreachableError) Unwrap() error { return e.Cause }

func NewHubClient(baseURL string) HubClient {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout: hubDialTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   hubTLSHandshakeTimeout,
		ResponseHeaderTimeout: hubResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &httpHubClient{
		baseURL: baseURL,
		http:    &http.Client{Transport: transport, Timeout: hubTotalTimeout},
	}
}

type httpHubClient struct {
	baseURL string
	http    *http.Client
}

func (c *httpHubClient) CheckPluginAvailability(ctx context.Context, name string) (AvailabilityResult, error) {
	u, err := url.JoinPath(c.baseURL, "api", "v1", "plugins", url.PathEscape(name))
	if err != nil {
		return AvailabilityResult{}, fmt.Errorf("invalid hub base URL %q: %w", c.baseURL, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return AvailabilityResult{}, &HubUnreachableError{Cause: err}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if isTrustError(err) {
			return AvailabilityResult{}, fmt.Errorf("hub TLS validation failed: %w", err)
		}
		return AvailabilityResult{}, &HubUnreachableError{Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNotFound:
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return AvailabilityResult{}, fmt.Errorf(
				"hub returned 404 but body is not valid JSON: %w — is --hub pointing at the right service?",
				err)
		}
		if body.Error.Code != "plugin_not_found" {
			return AvailabilityResult{}, fmt.Errorf(
				"hub returned 404 with error.code=%q (expected plugin_not_found) — is --hub pointing at the right service?",
				body.Error.Code)
		}
		return AvailabilityResult{Available: true}, nil
	case http.StatusOK:
		var body struct {
			Name          string `json:"name"`
			GitHubRepoURL string `json:"github_repo_url"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return AvailabilityResult{}, fmt.Errorf("hub returned 200 but body is not valid JSON: %w", err)
		}
		if body.Name == "" || body.GitHubRepoURL == "" {
			return AvailabilityResult{}, fmt.Errorf("hub returned 200 but response is missing required fields (name, github_repo_url)")
		}
		if body.Name != name {
			return AvailabilityResult{}, fmt.Errorf(
				"hub returned 200 for %q but body.name=%q — is --hub pointing at the right service?",
				name, body.Name)
		}
		return AvailabilityResult{Available: false, GitHubRepoURL: body.GitHubRepoURL}, nil
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return AvailabilityResult{}, &HubUnreachableError{
			Cause: fmt.Errorf("hub returned HTTP %d", resp.StatusCode),
		}
	default:
		if resp.StatusCode >= 500 {
			return AvailabilityResult{}, &HubUnreachableError{
				Cause: fmt.Errorf("hub returned HTTP %d", resp.StatusCode),
			}
		}
		return AvailabilityResult{}, fmt.Errorf(
			"hub returned unexpected HTTP %d — is --hub pointing at the right service?",
			resp.StatusCode)
	}
}

func isTrustError(err error) bool {
	var verifyErr *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	return errors.As(err, &verifyErr) ||
		errors.As(err, &unknownAuth) ||
		errors.As(err, &hostErr)
}

// validateHubURL normalizes and validates a hub base URL. Used by
// resolveHubURL in init.go before constructing a HubClient.
func validateHubURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("hub URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("hub URL %q is not a valid URL: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("hub URL %q has unsupported scheme %q (want http or https)", raw, u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("hub URL %q is missing a host", raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("hub URL %q must not embed credentials", raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}
