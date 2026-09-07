// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cloudapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// The connect calls: read an installation's cloud-connection coordinates,
// register a connection, and list the ones already registered. They share the
// installations client's hardening — no redirects, one-byte-past-cap body
// reads, per-record decode, warnings that never repeat an uncapped value.

// CloudConnectionSetup is the admin-gated coordinates read: the server-produced
// subject and role name, the issuer the trust artifacts must pin, and the
// accounts already connected across the installations this admin can see.
type CloudConnectionSetup struct {
	CloudSubject          string             `json:"cloudSubject"`
	CloudRoleName         string             `json:"cloudRoleName"`
	Issuer                string             `json:"issuer"`
	AccountsConnectedHint []ConnectedAccount `json:"accountsConnectedHint"`
	Warnings              []string           `json:"-"`
}

// ConnectedAccount is one hint entry: an account already connected on an
// installation the caller's grants cover, including the one being connected.
type ConnectedAccount struct {
	Cloud            string `json:"cloud"`
	Account          string `json:"account"`
	InstallationID   string `json:"installationId"`
	InstallationName string `json:"installationName"`
	TenantName       string `json:"tenantName"`
	OrgName          string `json:"orgName"`
}

// CloudConnectionRegistration is the registration request: the cloud, the
// account, and exactly that cloud's own trust coordinate.
//
// Both coordinates are omitempty because the control plane's schema is a
// discriminated union that admits exactly one shape per cloud and rejects any
// field belonging to another. Sending "roleArn": "" alongside a GCP
// registration is not a harmless empty value, it is a field that variant does
// not have.
type CloudConnectionRegistration struct {
	Cloud   string `json:"cloud"`
	Account string `json:"account"`
	RoleArn string `json:"roleArn,omitempty"`
	// WorkloadIdentityProvider is the GCP coordinate: the full resource name
	// of the workload identity pool provider that trusts this installation.
	WorkloadIdentityProvider string `json:"workloadIdentityProvider,omitempty"`
	// AzureTenantID and AzureClientID are the Azure coordinate: the
	// subscription's Entra tenant and the managed identity's client id.
	AzureTenantID string `json:"azureTenantId,omitempty"`
	AzureClientID string `json:"azureClientId,omitempty"`
}

// CloudConnection is one registered connection as the control plane lists it.
type CloudConnection struct {
	Cloud                    string `json:"cloud"`
	Account                  string `json:"account"`
	RoleArn                  string `json:"roleArn"`
	WorkloadIdentityProvider string `json:"workloadIdentityProvider"`
	AzureTenantID            string `json:"azureTenantId"`
	AzureClientID            string `json:"azureClientId"`
}

// RegisterOutcome reports what a registration did.
type RegisterOutcome struct{ Created bool }

// NotFoundError: 404. The control plane answers 404 for both no-grant and
// nonexistent, and an old control plane 404s the missing route; the caller
// disambiguates with the listing it already fetched, so this error stays dumb.
type NotFoundError struct{ Cause error }

func (e *NotFoundError) Error() string { return e.Cause.Error() }
func (e *NotFoundError) Unwrap() error { return e.Cause }

// NotReadyError: the setup endpoint refused because the installation's durable
// phase has not applied the split-key template yet, or it is destroying.
// State carries the control plane's error.details.state verbatim.
type NotReadyError struct{ State string }

func (e *NotReadyError) Error() string {
	if e.State == "" {
		return "the installation is not ready for a cloud connection"
	}
	return fmt.Sprintf("the installation is not ready for a cloud connection (state: %s)", e.State)
}

// ConflictError: registration answered 409; the caller GETs and compares.
type ConflictError struct{}

func (e *ConflictError) Error() string {
	return "a cloud connection for this account is already registered on this installation"
}

// apiErrorBody is the control plane's error envelope, read only as far as the
// fields this client branches on.
type apiErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Details struct {
			State string `json:"state"`
		} `json:"details"`
	} `json:"error"`
}

// decodeAPIError reads the error envelope from a non-2xx body. A body that is
// not the envelope decodes to the zero value: the status code has already said
// what class of failure this is, so the envelope only refines it.
func decodeAPIError(body []byte) apiErrorBody {
	var e apiErrorBody
	_ = json.Unmarshal(body, &e)
	return e
}

// GetCloudConnectionSetup reads the coordinates for connecting a cloud account
// to the installation.
func (c *httpCloudClient) GetCloudConnectionSetup(ctx context.Context, bearer, installationID string) (CloudConnectionSetup, error) {
	status, data, err := c.request(ctx, http.MethodGet, bearer, nil,
		"cloud-connection setup response", "api", "v1", "installations", installationID, "cloud-connection-setup")
	if err != nil {
		return CloudConnectionSetup{}, err
	}

	switch status {
	case http.StatusOK:
	case http.StatusConflict:
		if e := decodeAPIError(data); e.Error.Code == "installation_not_ready" {
			return CloudConnectionSetup{}, &NotReadyError{State: clip(e.Error.Details.State, maxWarnedRunes)}
		}
		return CloudConnectionSetup{}, fmt.Errorf(
			"the control plane returned unexpected HTTP %d for the cloud-connection setup request", status)
	default:
		if err := classifyStatus(status, "cloud-connection setup"); err != nil {
			return CloudConnectionSetup{}, err
		}
		return CloudConnectionSetup{}, fmt.Errorf(
			"the control plane returned unexpected HTTP %d for the cloud-connection setup request", status)
	}

	return parseSetup(data)
}

// parseSetup decodes the unwrapped setup object. The coordinates are required
// outright — connect acts on all three — while the hint is advisory: each of
// its records is decoded on its own, and a broken one is dropped with a
// warning rather than aborting the body.
func parseSetup(data []byte) (CloudConnectionSetup, error) {
	var raw struct {
		CloudSubject          *string           `json:"cloudSubject"`
		CloudRoleName         *string           `json:"cloudRoleName"`
		Issuer                *string           `json:"issuer"`
		AccountsConnectedHint []json.RawMessage `json:"accountsConnectedHint"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&raw); err != nil {
		return CloudConnectionSetup{}, errors.New("the cloud-connection setup response is not a JSON object")
	}
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"cloudSubject", raw.CloudSubject},
		{"cloudRoleName", raw.CloudRoleName},
		{"issuer", raw.Issuer},
	} {
		if field.value == nil || *field.value == "" {
			return CloudConnectionSetup{}, fmt.Errorf(
				"the cloud-connection setup response carries no %s, so connect has no coordinates to act on", field.name)
		}
	}

	setup := CloudConnectionSetup{
		CloudSubject:  *raw.CloudSubject,
		CloudRoleName: *raw.CloudRoleName,
		Issuer:        *raw.Issuer,
	}
	for i, record := range raw.AccountsConnectedHint {
		var hint ConnectedAccount
		if err := json.Unmarshal(record, &hint); err != nil {
			setup.Warnings = append(setup.Warnings, fmt.Sprintf(
				"ignoring connected-account hint %d of the cloud-connection setup response: it could not be read (%q)",
				i+1, clip(err.Error(), maxWarnedRunes)))
			continue
		}
		// The CLI requires only what it compares on; the rest of the record is
		// display text.
		if hint.Account == "" || hint.InstallationID == "" {
			setup.Warnings = append(setup.Warnings, fmt.Sprintf(
				"ignoring connected-account hint %d of the cloud-connection setup response: it names no account or no installation",
				i+1))
			continue
		}
		setup.AccountsConnectedHint = append(setup.AccountsConnectedHint, hint)
	}
	return setup, nil
}

// RegisterCloudConnection declares the connection on the installation. The
// registration is declared-unverified by design: the CLI states the trust it
// believes exists, and the control plane records exactly that.
func (c *httpCloudClient) RegisterCloudConnection(ctx context.Context, bearer, installationID string,
	registration CloudConnectionRegistration) (RegisterOutcome, error) {

	payload, err := json.Marshal(registration)
	if err != nil {
		return RegisterOutcome{}, fmt.Errorf("build the cloud-connection registration: %w", err)
	}

	status, _, err := c.request(ctx, http.MethodPost, bearer, payload,
		"cloud-connection registration response", "api", "v1", "installations", installationID, "cloud-connections")
	if err != nil {
		return RegisterOutcome{}, err
	}

	switch status {
	case http.StatusCreated:
		// The created row is the registration echoed back; nothing in it is
		// acted on, so nothing in it is parsed.
		return RegisterOutcome{Created: true}, nil
	case http.StatusConflict:
		return RegisterOutcome{}, &ConflictError{}
	default:
		if err := classifyStatus(status, "cloud-connection registration"); err != nil {
			return RegisterOutcome{}, err
		}
		return RegisterOutcome{}, fmt.Errorf(
			"the control plane returned unexpected HTTP %d for the cloud-connection registration", status)
	}
}

// ConnectionsSnapshot is a connections listing and whether it may be believed
// completely.
//
// Complete is the difference between "this installation has no connections" and
// "this response could not be read", which a caller branching on absence cannot
// otherwise tell apart. It mirrors Snapshot.Authoritative, for the same reason
// and with the same discipline: a consumer that ignores it decides on a list it
// only partly received.
type ConnectionsSnapshot struct {
	Connections []CloudConnection
	Complete    bool
	Warnings    []string
}

// knownClouds is the closed set the control plane's own discriminated union
// admits. A record naming anything else is one this build cannot interpret, so
// it is dropped rather than guessed at, and dropping it costs completeness.
var knownClouds = map[string]bool{"aws": true, "gcp": true, "azure": true}

// SessionLapsedError is a sign-in that is no longer good.
type SessionLapsedError struct{ Cause error }

func (e *SessionLapsedError) Error() string { return e.Cause.Error() }
func (e *SessionLapsedError) Unwrap() error { return e.Cause }

// InstallationForbiddenError is a caller the control plane knows and will not
// serve for this installation.
//
// It says nothing about membership. The server answers a caller with no grant on
// the owning organization with 404, so 403 means a member whose explicit tenant
// list does not include this installation.
type InstallationForbiddenError struct{ Cause error }

func (e *InstallationForbiddenError) Error() string { return e.Cause.Error() }
func (e *InstallationForbiddenError) Unwrap() error { return e.Cause }

// ListCloudConnections reads the connections registered on the installation.
// Broken records are dropped with a warning, never aborting the body: the
// caller compares against what it can read, and Complete tells it whether what
// it read was everything.
func (c *httpCloudClient) ListCloudConnections(ctx context.Context, bearer, installationID string) (ConnectionsSnapshot, error) {
	status, data, err := c.request(ctx, http.MethodGet, bearer, nil,
		"cloud connections response", "api", "v1", "installations", installationID, "cloud-connections")
	if err != nil {
		return ConnectionsSnapshot{}, err
	}
	if status != http.StatusOK {
		// Split before classifyStatus, which collapses 401 and 403 into one
		// AuthError that retains no status. The two mean different things to a
		// user and only this layer still knows which happened.
		switch status {
		case http.StatusUnauthorized:
			return ConnectionsSnapshot{}, &SessionLapsedError{Cause: errors.New(
				"your formae session has lapsed; sign in again")}
		case http.StatusForbidden:
			return ConnectionsSnapshot{}, &InstallationForbiddenError{Cause: errors.New(
				"you do not have access to this installation")}
		}
		if err := classifyStatus(status, "cloud connections"); err != nil {
			return ConnectionsSnapshot{}, err
		}
		return ConnectionsSnapshot{}, fmt.Errorf(
			"the control plane returned unexpected HTTP %d for the cloud-connections request", status)
	}

	envelope, repeated, err := decodeEnvelope(data, "cloud connections response")
	if err != nil {
		return ConnectionsSnapshot{}, err
	}

	snapshot := ConnectionsSnapshot{Complete: true}

	for _, key := range repeated {
		snapshot.Complete = false
		snapshot.Warnings = append(snapshot.Warnings, fmt.Sprintf(
			"the cloud connections response carries the %q field more than once, so this run has at most "+
				"one of the lists it was sent", clip(key, maxWarnedRunes)))
	}

	// An absent or null list is not an empty one. Reading either as "there are
	// none" is what turns an unreadable response into a decision.
	raw, present := envelope["results"]
	var results []json.RawMessage
	switch {
	case !present:
		snapshot.Complete = false
		snapshot.Warnings = append(snapshot.Warnings,
			"the cloud connections response carries no list where one was expected")
	default:
		if err := json.Unmarshal(raw, &results); err != nil {
			return ConnectionsSnapshot{}, errors.New(
				"the cloud connections response carries no list where one was expected")
		}
		if results == nil {
			snapshot.Complete = false
			snapshot.Warnings = append(snapshot.Warnings,
				"the cloud connections response carries an empty list where one was expected")
		}
	}

	for i, record := range results {
		var connection CloudConnection
		if err := json.Unmarshal(record, &connection); err != nil {
			snapshot.Complete = false
			snapshot.Warnings = append(snapshot.Warnings, fmt.Sprintf(
				"ignoring record %d of the cloud connections response: it could not be read (%q)",
				i+1, clip(err.Error(), maxWarnedRunes)))
			continue
		}
		if reason := invalidConnection(connection); reason != "" {
			snapshot.Complete = false
			snapshot.Warnings = append(snapshot.Warnings, fmt.Sprintf(
				"ignoring record %d of the cloud connections response: %s", i+1, reason))
			continue
		}
		snapshot.Connections = append(snapshot.Connections, connection)
	}
	return snapshot, nil
}

// invalidConnection names why a record cannot be used, or returns "".
//
// The role ARN is required of AWS alone. GCP and Azure carry their own trust
// coordinates and no role, so requiring it of every cloud dropped valid records
// and reported an installation that has connections as having none.
//
// Their own coordinates are deliberately not required either, for the same
// reason: this listing is read from whatever control plane is deployed, and a
// version that does not serve a field yet would have every connection of that
// cloud vanish from a listing whose whole job is to be complete. Nothing
// downstream needs the guard - the duplicate check compares coordinates as
// strings, and an empty registered coordinate can only produce a refusal, never
// a false "already registered".
func invalidConnection(c CloudConnection) string {
	switch {
	case !knownClouds[c.Cloud]:
		return "it names a cloud this formae does not understand"
	case c.Account == "":
		return "it names no account"
	case c.Cloud == "aws" && c.RoleArn == "":
		return "it names no role"
	}
	return ""
}

// classifyStatus maps the statuses every connect call classifies the same way:
// sign in again, try again later, or the thing addressed is not there. A nil
// return means the status is one the caller has to interpret itself.
func classifyStatus(status int, what string) error {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return &AuthError{Cause: fmt.Errorf(
			"the control plane rejected this session (HTTP %d) for the %s request", status, what)}
	case status == http.StatusNotFound:
		return &NotFoundError{Cause: fmt.Errorf(
			"the control plane answered 404 for the %s request", what)}
	case status == http.StatusRequestTimeout,
		status == http.StatusTooManyRequests,
		status >= 500:
		return &TransientError{Cause: fmt.Errorf(
			"the control plane returned HTTP %d for the %s request", status, what)}
	}
	return nil
}

// request performs one call and returns the status and the capped body. It
// carries every transport-level protection the installations call has: the
// bearer is the only header beyond what the body needs, a redirect is refused,
// a timeout classifies as transient, and the body is read one byte past the
// cap so a truncated list can never read as a complete one.
func (c *httpCloudClient) request(ctx context.Context, method, bearer string, payload []byte,
	response string, elem ...string) (int, []byte, error) {

	u, err := url.JoinPath(c.baseURL, elem...)
	if err != nil {
		return 0, nil, fmt.Errorf("invalid control-plane URL %q: %w", c.baseURL, err)
	}
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return 0, nil, fmt.Errorf("build the %s request: %w", response, err)
	}
	req.Header.Set("Authorization", bearer)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		if errors.Is(err, errUnexpectedRedirect) {
			return 0, nil, err
		}
		if isTimeout(err) {
			return 0, nil, &TransientError{Cause: err}
		}
		return 0, nil, fmt.Errorf("%s: %w", response, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return 0, nil, &TransientError{Cause: fmt.Errorf("read the %s: %w", response, err)}
	}
	if len(data) > maxResponseBytes {
		return 0, nil, fmt.Errorf("the %s is larger than %d bytes", response, maxResponseBytes)
	}
	return resp.StatusCode, data, nil
}
