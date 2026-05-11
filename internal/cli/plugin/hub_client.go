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
	"syscall"
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

// HubTransientError signals a definitely-temporary inability to reach the hub:
// HTTP timeouts (including dial/read timeouts) and HTTP 408/429/5xx responses.
// Callers always downgrade to a warning and continue, regardless of whether
// the hub URL was explicit or the default.
type HubTransientError struct{ Cause error }

func (e *HubTransientError) Error() string { return e.Cause.Error() }
func (e *HubTransientError) Unwrap() error { return e.Cause }

// HubUnreachableError signals a transport-level failure that is NOT
// definitively temporary: DNS resolution failures and connection-refused
// errors. The hub host either doesn't exist or nothing is listening.
//
// Callers must distinguish whether the hub URL was explicitly configured:
//   - explicit URL (--hub flag / FORMAE_HUB_URL env var) → hard-fail
//   - default URL → downgrade to warning (same as HubTransientError)
//
// Trust and protocol-mismatch errors do NOT use this type — they bubble up
// as plain errors so the caller can always hard-fail.
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
		// Caller-driven cancellation: propagate unchanged so the wider
		// command flow can react to it (interactive Ctrl-C, parent
		// deadline). We check ctx.Err() rather than errors.Is on the
		// network error because http.Client's own timeout also surfaces
		// as context.DeadlineExceeded — distinguishable by whether the
		// caller's context has itself errored.
		if ctx.Err() != nil {
			return AvailabilityResult{}, ctx.Err()
		}
		if isTrustError(err) {
			return AvailabilityResult{}, fmt.Errorf("hub TLS validation failed: %w", err)
		}
		if isProtocolMismatchError(err) {
			return AvailabilityResult{}, fmt.Errorf(
				"hub protocol mismatch (is --hub using the right scheme?): %w", err)
		}
		if isDefinitiveTransportError(err) {
			return AvailabilityResult{}, &HubUnreachableError{Cause: err}
		}
		return AvailabilityResult{}, &HubTransientError{Cause: err}
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
		return AvailabilityResult{}, &HubTransientError{
			Cause: fmt.Errorf("hub returned HTTP %d", resp.StatusCode),
		}
	default:
		if resp.StatusCode >= 500 {
			return AvailabilityResult{}, &HubTransientError{
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

// isProtocolMismatchError detects deterministic protocol mismatches
// where --hub points at the wrong scheme.
//
//   - HTTPS client → HTTP server: net/http returns http.ErrSchemeMismatch.
//   - HTTP client → HTTPS server: net/http has no exported sentinel;
//     the transport surfaces strings like "malformed HTTP response"
//     when it tries to parse TLS handshake bytes as an HTTP response.
//     We match on the prefix as a deliberate string-coupling to the
//     stdlib for this specific case.
//
// These are not transient and must hard-fail so the user fixes their
// config rather than silently scaffolding.
func isProtocolMismatchError(err error) bool {
	if errors.Is(err, http.ErrSchemeMismatch) {
		return true
	}
	if err != nil && strings.Contains(err.Error(), "malformed HTTP response") {
		return true
	}
	return false
}

// isDefinitiveTransportError returns true when err is a transport-level
// failure that is definitively not transient: DNS resolution failures or
// connection-refused errors. These indicate the host does not exist or
// nothing is listening, rather than a temporary overload or timeout.
//
// Timeout errors are intentionally excluded — they are transient and
// handled by returning HubTransientError instead.
func isDefinitiveTransportError(err error) bool {
	// Unwrap url.Error to get at the underlying net.Error / syscall.Errno.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Unwrap()
	}

	// DNS lookup failure.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		// IsNotFound: NXDOMAIN — definitively not transient.
		// IsTimeout: treated as transient (server-side DNS timeout, not our fault).
		if dnsErr.IsNotFound {
			return true
		}
		// Timeout DNS errors are transient; server-not-found is definitive.
		return false
	}

	// Connection refused (ECONNREFUSED) or no route to host (ENETUNREACH / EHOSTUNREACH).
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EHOSTUNREACH) {
		return true
	}

	return false
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
