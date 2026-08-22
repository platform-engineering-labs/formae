// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cloudapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three connect calls against the control plane. The stubs here encode the
// shared contract byte for byte: setup is GET
// /api/v1/installations/<id>/cloud-connection-setup answering an unwrapped
// object, registration is POST .../cloud-connections with the exact
// three-field body, and every error rides the apiError envelope.

// validHint returns a hint record that every validation rule accepts.
func validHint(account, installationID string) map[string]any {
	return map[string]any{
		"cloud":            "aws",
		"account":          account,
		"installationId":   installationID,
		"installationName": "prod",
		"tenantName":       "acme",
		"orgName":          "acme-inc",
	}
}

// setupBody renders the setup response the shared contract pins.
func setupBody(t *testing.T, hints ...any) string {
	t.Helper()
	if hints == nil {
		hints = []any{}
	}
	return marshalJSON(t, map[string]any{
		"cloudSubject":          "fai:acme/" + testInstallationA,
		"cloudRoleName":         "formae-connect-" + testInstallationA,
		"issuer":                "https://oidc.cloud.formae.ai",
		"accountsConnectedHint": hints,
	})
}

// apiError renders the control plane's error envelope.
func apiError(t *testing.T, code string, details map[string]any) string {
	t.Helper()
	e := map[string]any{"code": code, "message": "refused"}
	if details != nil {
		e["details"] = details
	}
	return marshalJSON(t, map[string]any{"error": e})
}

func setupFrom(t *testing.T, srv *httptest.Server) (CloudConnectionSetup, error) {
	t.Helper()
	return NewClient(srv.URL).GetCloudConnectionSetup(context.Background(), testBearer, testInstallationA)
}

func TestGetCloudConnectionSetup_SendsTheBearerAndDecodesTheCoordinates(t *testing.T) {
	srv, seen := serveBody(t, http.StatusOK, nil, setupBody(t, validHint("123456789012", testInstallationB)))

	setup, err := setupFrom(t, srv)

	require.NoError(t, err)
	assert.Equal(t, "fai:acme/"+testInstallationA, setup.CloudSubject)
	assert.Equal(t, "formae-connect-"+testInstallationA, setup.CloudRoleName)
	assert.Equal(t, "https://oidc.cloud.formae.ai", setup.Issuer)
	require.Len(t, setup.AccountsConnectedHint, 1)
	assert.Equal(t, ConnectedAccount{
		Cloud:            "aws",
		Account:          "123456789012",
		InstallationID:   testInstallationB,
		InstallationName: "prod",
		TenantName:       "acme",
		OrgName:          "acme-inc",
	}, setup.AccountsConnectedHint[0])
	assert.Empty(t, setup.Warnings)

	requests, header, method, path := seen.snapshot()
	require.Equal(t, 1, requests)
	assert.Equal(t, http.MethodGet, method, "reading the coordinates must not be a write")
	assert.Equal(t, "/api/v1/installations/"+testInstallationA+"/cloud-connection-setup", path)
	assert.Equal(t, testBearer, header.Get("Authorization"))
	for name := range header {
		switch name {
		case "Authorization", "User-Agent", "Accept-Encoding":
		default:
			t.Errorf("the client set an unexpected header %q", name)
		}
	}
}

// A hint record that cannot be used is dropped on its own, with a warning; its
// siblings and the coordinates survive it. The hint is advisory — aborting the
// whole setup read over one broken row would block a connect on data it only
// warns from.
func TestGetCloudConnectionSetup_DropsABrokenHintRecordWithAWarning(t *testing.T) {
	tests := []struct {
		name string
		bad  any
	}{
		{name: "null record", bad: nil},
		{name: "record that is not an object", bad: 7},
		{name: "type error in account", bad: map[string]any{"cloud": "aws", "account": 12, "installationId": testInstallationB}},
		{name: "missing account", bad: map[string]any{"cloud": "aws", "installationId": testInstallationB}},
		{name: "missing installationId", bad: map[string]any{"cloud": "aws", "account": "123456789012"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := serveBody(t, http.StatusOK, nil, setupBody(t, validHint("999999999999", testInstallationC), tc.bad))

			setup, err := setupFrom(t, srv)

			require.NoError(t, err)
			require.Len(t, setup.AccountsConnectedHint, 1, "the valid sibling is still returned")
			assert.Equal(t, "999999999999", setup.AccountsConnectedHint[0].Account)
			require.Len(t, setup.Warnings, 1)
		})
	}
}

// An unknown field inside the setup object or a hint record is the endpoint
// growing, not an error.
func TestGetCloudConnectionSetup_ToleratesUnknownFields(t *testing.T) {
	hint := validHint("123456789012", testInstallationB)
	hint["region"] = "eu-west-1"
	body := setupBody(t, hint)
	body = body[:len(body)-1] + `,"newTopLevelField":true}`
	srv, _ := serveBody(t, http.StatusOK, nil, body)

	setup, err := setupFrom(t, srv)

	require.NoError(t, err)
	assert.Len(t, setup.AccountsConnectedHint, 1)
}

// The coordinates are what connect acts on; a body missing any of them is not
// a setup answer at all.
func TestGetCloudConnectionSetup_RefusesABodyMissingTheCoordinates(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "no subject", body: `{"cloudRoleName":"r","issuer":"https://oidc.cloud.formae.ai","accountsConnectedHint":[]}`},
		{name: "no role name", body: `{"cloudSubject":"s","issuer":"https://oidc.cloud.formae.ai","accountsConnectedHint":[]}`},
		{name: "no issuer", body: `{"cloudSubject":"s","cloudRoleName":"r","accountsConnectedHint":[]}`},
		{name: "not json", body: "not json at all"},
		{name: "a json array", body: `[]`},
		{name: "empty body", body: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := serveBody(t, http.StatusOK, nil, tc.body)

			_, err := setupFrom(t, srv)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "cloud-connection setup response",
				"the diagnostic names the response it is about")
		})
	}
}

func TestGetCloudConnectionSetup_ClassifiesFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		auth      bool
		transient bool
		notFound  bool
		state     string // non-empty: expect *NotReadyError carrying it
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, auth: true},
		{name: "forbidden", status: http.StatusForbidden, auth: true},
		{name: "not found", status: http.StatusNotFound, notFound: true},
		{name: "request timeout", status: http.StatusRequestTimeout, transient: true},
		{name: "too many requests", status: http.StatusTooManyRequests, transient: true},
		{name: "internal server error", status: http.StatusInternalServerError, transient: true},
		{name: "service unavailable", status: http.StatusServiceUnavailable, transient: true},
		{name: "bad request", status: http.StatusBadRequest},
		{name: "conflict without a readiness code", status: http.StatusConflict, body: `{"error":{"code":"something_else"}}`},
		{name: "conflict with an unreadable body", status: http.StatusConflict, body: "not json"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := serveBody(t, tc.status, nil, tc.body)

			_, err := setupFrom(t, srv)

			require.Error(t, err)
			var authErr *AuthError
			var transientErr *TransientError
			var notFoundErr *NotFoundError
			var notReadyErr *NotReadyError
			assert.Equal(t, tc.auth, errors.As(err, &authErr), "auth classification")
			assert.Equal(t, tc.transient, errors.As(err, &transientErr), "transient classification")
			assert.Equal(t, tc.notFound, errors.As(err, &notFoundErr), "not-found classification")
			assert.False(t, errors.As(err, &notReadyErr))
		})
	}
}

// The readiness refusal is HTTP 409 with code installation_not_ready, and
// details.state says which non-ready state the installation is in. Each state
// value the control plane can answer is carried through verbatim, because the
// caller's message includes it.
func TestGetCloudConnectionSetup_ANotReadyRefusalCarriesTheState(t *testing.T) {
	for _, state := range []string{"provisioning", "destroying", "destroyed", "suspended", "active"} {
		t.Run(state, func(t *testing.T) {
			srv, _ := serveBody(t, http.StatusConflict, nil,
				apiError(t, "installation_not_ready", map[string]any{"state": state}))

			_, err := setupFrom(t, srv)

			require.Error(t, err)
			var notReady *NotReadyError
			require.True(t, errors.As(err, &notReady))
			assert.Equal(t, state, notReady.State)
			assert.Contains(t, notReady.Error(), state, "the message names the state")
		})
	}
}

func TestGetCloudConnectionSetup_RefusesARedirectAndDoesNotForwardTheBearer(t *testing.T) {
	target, targetSeen := serveBody(t, http.StatusOK, nil, setupBody(t))
	origin, _ := serveBody(t, http.StatusFound, map[string]string{"Location": target.URL}, "")

	_, err := setupFrom(t, origin)

	require.Error(t, err)
	requests, header, _, _ := targetSeen.snapshot()
	assert.Zero(t, requests, "the redirect target must never be contacted")
	assert.Empty(t, header.Get("Authorization"), "the bearer must not be forwarded")
	assert.NotContains(t, err.Error(), "test-token-value")
}

func TestGetCloudConnectionSetup_RejectsABodyOverTheCap(t *testing.T) {
	srv, _ := serveBody(t, http.StatusOK, nil, strings.Repeat("x", maxResponseBytes+1))

	_, err := setupFrom(t, srv)

	require.Error(t, err)
}

// registerFrom drives one registration against srv.
func registerFrom(t *testing.T, srv *httptest.Server) (RegisterOutcome, error) {
	t.Helper()
	return NewClient(srv.URL).RegisterCloudConnection(context.Background(), testBearer, testInstallationA,
		CloudConnectionRegistration{
			Cloud:   "aws",
			Account: "123456789012",
			RoleArn: "arn:aws:iam::123456789012:role/formae-connect-" + testInstallationA,
		})
}

func TestRegisterCloudConnection_SendsExactlyTheContractBody(t *testing.T) {
	var gotBody []byte
	seen := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.record(r)
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"cloud":"aws","account":"123456789012","roleArn":"x"}`))
	}))
	t.Cleanup(srv.Close)

	outcome, err := registerFrom(t, srv)

	require.NoError(t, err)
	assert.True(t, outcome.Created)

	requests, header, method, path := seen.snapshot()
	require.Equal(t, 1, requests)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/api/v1/installations/"+testInstallationA+"/cloud-connections", path)
	assert.Equal(t, testBearer, header.Get("Authorization"))
	assert.Equal(t, "application/json", header.Get("Content-Type"))
	assert.JSONEq(t,
		`{"cloud":"aws","account":"123456789012","roleArn":"arn:aws:iam::123456789012:role/formae-connect-`+testInstallationA+`"}`,
		string(gotBody))
	// The three keys and nothing else: an extra key is a contract change.
	var sent map[string]any
	require.NoError(t, json.Unmarshal(gotBody, &sent))
	assert.Len(t, sent, 3)
}

func TestRegisterCloudConnection_ClassifiesFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		auth      bool
		transient bool
		notFound  bool
		conflict  bool
	}{
		{name: "created", status: http.StatusCreated},
		{name: "duplicate", status: http.StatusConflict, body: `{"error":{"code":"cloud_connection_exists"}}`, conflict: true},
		{name: "conflict with unreadable body", status: http.StatusConflict, body: "not json", conflict: true},
		{name: "unauthorized", status: http.StatusUnauthorized, auth: true},
		{name: "forbidden", status: http.StatusForbidden, auth: true},
		{name: "not found", status: http.StatusNotFound, notFound: true},
		{name: "request timeout", status: http.StatusRequestTimeout, transient: true},
		{name: "too many requests", status: http.StatusTooManyRequests, transient: true},
		{name: "internal server error", status: http.StatusInternalServerError, transient: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := serveBody(t, tc.status, nil, tc.body)

			outcome, err := registerFrom(t, srv)

			if tc.status == http.StatusCreated {
				require.NoError(t, err)
				assert.True(t, outcome.Created)
				return
			}
			require.Error(t, err)
			assert.False(t, outcome.Created)
			var authErr *AuthError
			var transientErr *TransientError
			var notFoundErr *NotFoundError
			var conflictErr *ConflictError
			assert.Equal(t, tc.auth, errors.As(err, &authErr), "auth classification")
			assert.Equal(t, tc.transient, errors.As(err, &transientErr), "transient classification")
			assert.Equal(t, tc.notFound, errors.As(err, &notFoundErr), "not-found classification")
			assert.Equal(t, tc.conflict, errors.As(err, &conflictErr), "conflict classification")
		})
	}
}

func TestRegisterCloudConnection_RefusesARedirectAndDoesNotForwardTheBearer(t *testing.T) {
	target, targetSeen := serveBody(t, http.StatusCreated, nil, "")
	origin, _ := serveBody(t, http.StatusFound, map[string]string{"Location": target.URL}, "")

	_, err := registerFrom(t, origin)

	require.Error(t, err)
	requests, _, _, _ := targetSeen.snapshot()
	assert.Zero(t, requests)
	assert.NotContains(t, err.Error(), "test-token-value")
}

// connectionsBody renders the cloud-connections listing.
func connectionsBody(t *testing.T, records ...any) string {
	t.Helper()
	if records == nil {
		records = []any{}
	}
	return marshalJSON(t, map[string]any{"results": records})
}

func listConnectionsFrom(t *testing.T, srv *httptest.Server) ([]CloudConnection, []string, error) {
	t.Helper()
	return NewClient(srv.URL).ListCloudConnections(context.Background(), testBearer, testInstallationA)
}

func TestListCloudConnections_DecodesRecordsAndSendsTheBearerOnly(t *testing.T) {
	srv, seen := serveBody(t, http.StatusOK, nil, connectionsBody(t,
		map[string]any{"cloud": "aws", "account": "123456789012", "roleArn": "arn:aws:iam::123456789012:role/r"}))

	connections, warnings, err := listConnectionsFrom(t, srv)

	require.NoError(t, err)
	assert.Empty(t, warnings)
	require.Len(t, connections, 1)
	assert.Equal(t, CloudConnection{
		Cloud:   "aws",
		Account: "123456789012",
		RoleArn: "arn:aws:iam::123456789012:role/r",
	}, connections[0])

	requests, header, method, path := seen.snapshot()
	require.Equal(t, 1, requests)
	assert.Equal(t, http.MethodGet, method)
	assert.Equal(t, "/api/v1/installations/"+testInstallationA+"/cloud-connections", path)
	assert.Equal(t, testBearer, header.Get("Authorization"))
}

func TestListCloudConnections_DropsABrokenRecordWithAWarning(t *testing.T) {
	srv, _ := serveBody(t, http.StatusOK, nil, connectionsBody(t,
		map[string]any{"cloud": "aws", "account": "123456789012", "roleArn": "arn:aws:iam::123456789012:role/r"},
		map[string]any{"cloud": "aws", "account": 12},
	))

	connections, warnings, err := listConnectionsFrom(t, srv)

	require.NoError(t, err)
	require.Len(t, connections, 1, "the valid sibling survives")
	require.Len(t, warnings, 1)
}

func TestListCloudConnections_ClassifiesFailuresAndRefusesRedirects(t *testing.T) {
	srv, _ := serveBody(t, http.StatusForbidden, nil, "")
	_, _, err := listConnectionsFrom(t, srv)
	var authErr *AuthError
	require.True(t, errors.As(err, &authErr))

	srv2, _ := serveBody(t, http.StatusServiceUnavailable, nil, "")
	_, _, err = listConnectionsFrom(t, srv2)
	var transientErr *TransientError
	require.True(t, errors.As(err, &transientErr))

	target, targetSeen := serveBody(t, http.StatusOK, nil, connectionsBody(t))
	origin, _ := serveBody(t, http.StatusFound, map[string]string{"Location": target.URL}, "")
	_, _, err = listConnectionsFrom(t, origin)
	require.Error(t, err)
	requests, _, _, _ := targetSeen.snapshot()
	assert.Zero(t, requests)

	over, _ := serveBody(t, http.StatusOK, nil, strings.Repeat("x", maxResponseBytes+1))
	_, _, err = listConnectionsFrom(t, over)
	require.Error(t, err)
}

// No warning or error from any connect call repeats an uncapped value the far
// end chose.
func TestConnectCalls_WarningsAreBoundedAndLeakNothing(t *testing.T) {
	long := strings.Repeat("k", 4000)
	srv, _ := serveBody(t, http.StatusOK, nil, setupBody(t, map[string]any{
		"cloud": "aws", "account": long, "installationId": 7,
	}))

	setup, err := setupFrom(t, srv)

	require.NoError(t, err)
	require.Len(t, setup.Warnings, 1)
	assert.Less(t, len(setup.Warnings[0]), 1000)

	badIssuer, _ := serveBody(t, http.StatusOK, nil, `{"cloudSubject":"s","cloudRoleName":"r","issuer":"`+long+`","accountsConnectedHint":[]}`)
	_, err = setupFrom(t, badIssuer)
	if err != nil {
		assert.Less(t, len(err.Error()), 1000, "an error must not flood the terminal with text the far end chose")
	}
}
