// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testBearer is the complete Authorization header value a caller hands the
// client, scheme included.
const testBearer = "Bearer test-token-value"

// rawInstallation is an installation record as it appears in the response,
// built as raw JSON so tests can express shapes the Go type cannot hold.
type rawInstallation map[string]any

// validInstallation returns a record that every validation rule accepts.
func validInstallation(id string) rawInstallation {
	return rawInstallation{
		"installationId":   id,
		"installationName": "prod",
		"tenantName":       "acme",
		"orgName":          "acme-inc",
		"endpoint":         testOrigin,
		"issuerPath":       "/auth/acme",
		"state":            "active",
	}
}

// installationsBody renders a response envelope carrying the given records.
func installationsBody(t *testing.T, records ...any) string {
	t.Helper()
	if records == nil {
		records = []any{}
	}
	return marshalJSON(t, map[string]any{"results": records})
}

func marshalJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}

// capture records what a test server was actually asked, so a test can assert
// on requests that must never happen as well as on ones that must.
type capture struct {
	mu       sync.Mutex
	requests int
	header   http.Header
	method   string
	path     string
}

func (c *capture) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests++
	c.header = r.Header.Clone()
	c.method = r.Method
	c.path = r.URL.Path
}

func (c *capture) snapshot() (int, http.Header, string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests, c.header, c.method, c.path
}

// serveBody starts a server answering every request with the given status,
// headers, and body.
func serveBody(t *testing.T, status int, headers map[string]string, body string) (*httptest.Server, *capture) {
	t.Helper()
	seen := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.record(r)
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

// serveInstallations starts a server answering with a 200 and the given records.
func serveInstallations(t *testing.T, records ...any) (*httptest.Server, *capture) {
	t.Helper()
	return serveBody(t, http.StatusOK, nil, installationsBody(t, records...))
}

// listFrom drives one request against srv and returns the snapshot.
func listFrom(t *testing.T, srv *httptest.Server) (Snapshot, error) {
	t.Helper()
	return newCloudClient(srv.URL).ListInstallations(context.Background(), testBearer)
}

// installationIDs returns the id of every installation, in order.
func installationIDs(installations []Installation) []string {
	out := make([]string, 0, len(installations))
	for _, i := range installations {
		out = append(out, i.InstallationID)
	}
	return out
}

// TestListInstallations_SendsTheBearerAndNothingElse pins the one header the
// client adds. Every extra header is another thing an origin can key off or a
// proxy can cache on, and the bearer must reach the control plane unchanged.
func TestListInstallations_SendsTheBearerAndNothingElse(t *testing.T) {
	srv, seen := serveInstallations(t, validInstallation(testInstallationA))

	snapshot, err := listFrom(t, srv)

	require.NoError(t, err)
	assert.True(t, snapshot.Authoritative)

	requests, header, method, path := seen.snapshot()
	require.Equal(t, 1, requests)
	assert.Equal(t, http.MethodGet, method, "reading the caller's grants must not be a write")
	assert.Equal(t, "/api/v1/me/installations", path)
	assert.Equal(t, testBearer, header.Get("Authorization"))

	// Only the headers net/http adds for us may accompany it.
	for name := range header {
		switch name {
		case "Authorization", "User-Agent", "Accept-Encoding":
		default:
			t.Errorf("the client set an unexpected header %q", name)
		}
	}
}

// TestListInstallations_RefusesARedirectAndDoesNotForwardTheBearer pins that a
// redirect is refused outright. Go forwards custom headers across a redirect,
// so following one would hand the bearer to whatever host the response named.
func TestListInstallations_RefusesARedirectAndDoesNotForwardTheBearer(t *testing.T) {
	target, targetSeen := serveInstallations(t, validInstallation(testInstallationA))
	origin, _ := serveBody(t, http.StatusFound, map[string]string{"Location": target.URL + "/api/v1/me/installations"}, "")

	snapshot, err := listFrom(t, origin)

	require.Error(t, err)
	assert.Empty(t, snapshot.Installations)
	assert.False(t, snapshot.Authoritative)

	requests, header, _, _ := targetSeen.snapshot()
	assert.Zero(t, requests, "the redirect target must never be contacted")
	assert.Empty(t, header.Get("Authorization"), "the bearer must not be forwarded")
	assert.NotContains(t, err.Error(), "test-token-value", "the bearer must not appear in an error")
	assert.Contains(t, err.Error(), fmt.Sprintf("to %q", target.URL+"/api/v1/me/installations"),
		"the location the far end named is quoted, so it cannot rewrite the line it lands in")
}

// TestListInstallations_RejectsABodyOverTheCapBeforeParsing pins that the size
// cap is decided on bytes read, not on what parses. A truncated prefix can be
// valid JSON, and for a client whose caller deletes on absence, reading a
// truncated list as a complete one is the expensive mistake.
func TestListInstallations_RejectsABodyOverTheCapBeforeParsing(t *testing.T) {
	atCap := paddedBody(t, maxResponseBytes)
	require.Len(t, atCap, maxResponseBytes)
	var parses map[string]any
	require.NoError(t, json.Unmarshal([]byte(atCap), &parses), "the prefix must itself be valid JSON")

	tests := []struct {
		name string
		body string
	}{
		{name: "one byte over the cap", body: strings.Repeat("x", maxResponseBytes+1)},
		{name: "a truncated prefix that would itself parse", body: atCap + " "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.body), maxResponseBytes)
			srv, _ := serveBody(t, http.StatusOK, nil, tc.body)

			snapshot, err := listFrom(t, srv)

			require.Error(t, err)
			assert.Empty(t, snapshot.Installations, "nothing may be salvaged from an oversized body")
			assert.False(t, snapshot.Authoritative)
		})
	}
}

// TestListInstallations_AcceptsABodyExactlyAtTheCap pins the boundary from the
// other side: the cap is the largest body the client will read, not one byte
// less. It also pins what makes the over-cap fixture above dangerous — read on
// its own, that prefix is a complete, authoritative list.
func TestListInstallations_AcceptsABodyExactlyAtTheCap(t *testing.T) {
	srv, _ := serveBody(t, http.StatusOK, nil, paddedBody(t, maxResponseBytes))

	snapshot, err := listFrom(t, srv)

	require.NoError(t, err)
	assert.Equal(t, []string{testInstallationA}, installationIDs(snapshot.Installations))
	assert.True(t, snapshot.Authoritative, "the padding must not be what decides authority")
}

// paddedBody returns a valid single-record envelope padded to exactly size
// bytes.
//
// The padding is trailing whitespace rather than an extra field, so the body
// keeps its authority: a fixture that lost authority to its own padding could
// not show what a truncated prefix costs, which is that it reads as a complete
// answer.
func paddedBody(t *testing.T, size int) string {
	t.Helper()
	body := `{"results":[` + marshalJSON(t, validInstallation(testInstallationA)) + `]}`
	require.LessOrEqual(t, len(body), size)
	return body + strings.Repeat(" ", size-len(body))
}

// TestListInstallations_RejectsMoreInstallationsThanTheCap pins that a body
// carrying more records than the endpoint may return is refused whole. A
// response that breaks its own contract is not a response we can write the
// user's config directory from.
func TestListInstallations_RejectsMoreInstallationsThanTheCap(t *testing.T) {
	records := make([]any, 0, maxInstallations+1)
	for i := 0; i <= maxInstallations; i++ {
		records = append(records, validInstallation(fmt.Sprintf("aaaaaaaaaaaaaaaaaaaa%07d", i)))
	}
	srv, _ := serveBody(t, http.StatusOK, nil, installationsBody(t, records...))

	snapshot, err := listFrom(t, srv)

	require.Error(t, err)
	assert.Empty(t, snapshot.Installations)
	assert.False(t, snapshot.Authoritative)
}

// TestListInstallations_AcceptsExactlyTheCap pins the boundary: the cap is a
// count the endpoint may return, not one it may not reach.
func TestListInstallations_AcceptsExactlyTheCap(t *testing.T) {
	records := make([]any, 0, maxInstallations)
	for i := 0; i < maxInstallations; i++ {
		records = append(records, validInstallation(fmt.Sprintf("aaaaaaaaaaaaaaaaaaaa%07d", i)))
	}
	srv, _ := serveBody(t, http.StatusOK, nil, installationsBody(t, records...))

	snapshot, err := listFrom(t, srv)

	require.NoError(t, err)
	assert.Len(t, snapshot.Installations, maxInstallations)
	assert.True(t, snapshot.Authoritative)
}

// TestListInstallations_ClassifiesFailures pins the three answers a caller
// acts on differently: sign in again, try again later, and something is wrong
// that neither of those fixes.
func TestListInstallations_ClassifiesFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		auth      bool
		transient bool
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, auth: true},
		{name: "forbidden", status: http.StatusForbidden, auth: true},
		{name: "request timeout", status: http.StatusRequestTimeout, transient: true},
		{name: "too many requests", status: http.StatusTooManyRequests, transient: true},
		{name: "internal server error", status: http.StatusInternalServerError, transient: true},
		{name: "service unavailable", status: http.StatusServiceUnavailable, transient: true},
		{name: "not found", status: http.StatusNotFound},
		{name: "bad request", status: http.StatusBadRequest},
		{name: "undecodable body", status: http.StatusOK, body: "not json at all"},
		{name: "body that is not a JSON object", status: http.StatusOK, body: `[{"installationId":"x"}]`},
		{name: "null body", status: http.StatusOK, body: "null"},
		{name: "empty body", status: http.StatusOK, body: ""},
		{name: "a second object after the first", status: http.StatusOK, body: `{"results":[]}{"results":[]}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := serveBody(t, tc.status, nil, tc.body)

			snapshot, err := listFrom(t, srv)

			require.Error(t, err)
			assert.Empty(t, snapshot.Installations)
			assert.False(t, snapshot.Authoritative)

			var authErr *cloudAuthError
			var transientErr *cloudTransientError
			assert.Equal(t, tc.auth, errors.As(err, &authErr), "auth classification")
			assert.Equal(t, tc.transient, errors.As(err, &transientErr), "transient classification")
		})
	}
}

// TestListInstallations_AuthErrorNamesSigningInAgain pins that the one error a
// user can act on says what to do about it.
func TestListInstallations_AuthErrorNamesSigningInAgain(t *testing.T) {
	srv, _ := serveBody(t, http.StatusUnauthorized, nil, "")

	_, err := listFrom(t, srv)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "login")
	assert.NotContains(t, err.Error(), "test-token-value")
}

// TestListInstallations_ClassifiesATimeoutAsTransient covers the transport
// side of the same classification: a control plane that never answers is a
// reason to try again, not a reason to conclude anything about grants.
func TestListInstallations_ClassifiesATimeoutAsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)

	client, ok := newCloudClient(srv.URL).(*httpCloudClient)
	require.True(t, ok)
	client.http.Timeout = 20 * time.Millisecond

	snapshot, err := client.ListInstallations(context.Background(), testBearer)

	require.Error(t, err)
	var transientErr *cloudTransientError
	assert.True(t, errors.As(err, &transientErr))
	assert.False(t, snapshot.Authoritative)
}

// TestListInstallations_DecodesAWellFormedResponse covers the happy path over
// every field, including the issuer path the client retains but does not use
// and a state it does not recognise: classifying states is not this client's
// job, and an unknown one says nothing about whether the list is complete.
func TestListInstallations_DecodesAWellFormedResponse(t *testing.T) {
	record := validInstallation(testInstallationA)
	record["state"] = "suspended-for-reasons-we-do-not-know"
	srv, _ := serveInstallations(t, record)

	snapshot, err := listFrom(t, srv)

	require.NoError(t, err)
	assert.True(t, snapshot.Authoritative)
	assert.Empty(t, snapshot.Warnings)
	require.Len(t, snapshot.Installations, 1)
	assert.Equal(t, Installation{
		InstallationID:   testInstallationA,
		InstallationName: "prod",
		TenantName:       "acme",
		OrgName:          "acme-inc",
		Endpoint:         testOrigin,
		IssuerPath:       "/auth/acme",
		State:            "suspended-for-reasons-we-do-not-know",
	}, snapshot.Installations[0])
}

// TestListInstallations_AcceptsAndCanonicalisesAnEndpoint pins the endpoints a
// record may carry and the spelling they come back in. One origin gets one
// representation, so two records naming it in different spellings are one
// installation's endpoint and not two.
//
// The loopback row is the same rule the rest of the command applies to an
// origin: a bearer sent over plain http can be read by anything on the path,
// so http is confined to the hosts where there is no path off the machine. A
// record naming one is a development endpoint, not a broken one.
func TestListInstallations_AcceptsAndCanonicalisesAnEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{
			name:     "a mixed-case https origin with a redundant port and trailing slash",
			endpoint: "HTTPS://Cloud.Formae.IO:443/",
			want:     testOrigin,
		},
		{
			name:     "a loopback http origin",
			endpoint: "http://127.0.0.1:9999",
			want:     "http://127.0.0.1:9999",
		},
		{
			name:     "a loopback http origin named by hostname",
			endpoint: "HTTP://LocalHost:8080/",
			want:     "http://localhost:8080",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := validInstallation(testInstallationA)
			record["endpoint"] = tc.endpoint
			srv, _ := serveInstallations(t, record)

			snapshot, err := listFrom(t, srv)

			require.NoError(t, err)
			assert.True(t, snapshot.Authoritative, "a usable endpoint costs the run nothing")
			assert.Empty(t, snapshot.Warnings)
			require.Len(t, snapshot.Installations, 1)
			assert.Equal(t, tc.want, snapshot.Installations[0].Endpoint)
		})
	}
}

// TestListInstallations_AnEmptyArrayIsAnAuthoritativeEmptyList pins the one
// shape that legitimately means "your grants cover nothing here" — and so the
// one shape that licenses removing every profile for this origin.
func TestListInstallations_AnEmptyArrayIsAnAuthoritativeEmptyList(t *testing.T) {
	srv, _ := serveBody(t, http.StatusOK, nil, `{"results":[]}`)

	snapshot, err := listFrom(t, srv)

	require.NoError(t, err)
	assert.True(t, snapshot.Authoritative)
	assert.Empty(t, snapshot.Installations)
	assert.Empty(t, snapshot.Warnings)
}

// TestListInstallations_AMissingListIsNotAnEmptyList pins the distinction the
// whole client exists for: a body with no results list says nothing about the
// caller's grants, and reading it as "you have none" would delete every
// profile for the origin.
func TestListInstallations_AMissingListIsNotAnEmptyList(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "no results key", body: `{}`},
		{name: "null results", body: `{"results":null}`},
		{name: "results under another name", body: `{"installations":[{"installationId":"` + testInstallationA + `"}]}`},
		{name: "results that is not an array", body: `{"results":7}`},
		{name: "results that is an object", body: `{"results":{"items":[]}}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := serveBody(t, http.StatusOK, nil, tc.body)

			snapshot, err := listFrom(t, srv)

			require.NoError(t, err, "an answer we cannot use is not a failed request")
			assert.False(t, snapshot.Authoritative, "nothing may be removed on the strength of this body")
			assert.Empty(t, snapshot.Installations)
			assert.NotEmpty(t, snapshot.Warnings)
		})
	}
}

// TestListInstallations_ARepeatedResultsKeyLosesAuthority pins the shape a
// decoder hides: a body naming results twice decodes to whichever list came
// last, so every installation in the other one would be read as absent while
// the body still looked like a complete, wholly recognised answer.
func TestListInstallations_ARepeatedResultsKeyLosesAuthority(t *testing.T) {
	body := fmt.Sprintf(`{"results":[%s],"results":[%s]}`,
		marshalJSON(t, validInstallation(testInstallationA)),
		marshalJSON(t, validInstallation(testInstallationB)))
	srv, _ := serveBody(t, http.StatusOK, nil, body)

	snapshot, err := listFrom(t, srv)

	require.NoError(t, err)
	assert.False(t, snapshot.Authoritative, "a list we may only have half of removes nothing")
	require.NotEmpty(t, snapshot.Warnings)
	assert.Contains(t, snapshot.Warnings[0], "results")
}

// TestListInstallations_TheEnvelopeWalkOnlyReadsTheTopLevel pins that the
// token walk that reads the envelope reads the envelope and nothing else. It
// decodes each member's value whole, so a record's own keys are never mistaken
// for the response's: both rows are records that are present and identifiable,
// and a run that can see them can still decide every installation's presence.
func TestListInstallations_TheEnvelopeWalkOnlyReadsTheTopLevel(t *testing.T) {
	record := marshalJSON(t, validInstallation(testInstallationA))
	withField := func(field, value string) string {
		return record[:len(record)-1] + `,` + strconv.Quote(field) + `:` + value + `}`
	}

	tests := []struct {
		name   string
		record string
	}{
		{
			name:   "a key repeated inside a record",
			record: withField("orgName", `"acme-inc"`),
		},
		{
			name:   "a nested object whose key is the envelope's",
			record: withField("features", `{"results":[{"beta":true}]}`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := serveBody(t, http.StatusOK, nil, `{"results":[`+tc.record+`]}`)

			snapshot, err := listFrom(t, srv)

			require.NoError(t, err)
			assert.True(t, snapshot.Authoritative, "what a record carries says nothing about the list being complete")
			assert.Empty(t, snapshot.Warnings)
			assert.Equal(t, []string{testInstallationA}, installationIDs(snapshot.Installations))
		})
	}
}

// TestListInstallations_RefusesAnythingAfterTheTopLevelObject pins that the
// body ends where the object it opened with ends. Anything after it means the
// body is not the single answer it claims to be, and there is no telling which
// part of it to believe — so believing the first part would let a body whose
// later half carries the rest of the list read as a complete one.
//
// The rows are the bytes that end a JSON value: a decoder asked only whether
// more values follow reads a `}` or a `]` as the end of the stream, so those
// are the shapes a bare "is there more?" check waves through.
func TestListInstallations_RefusesAnythingAfterTheTopLevelObject(t *testing.T) {
	first := marshalJSON(t, validInstallation(testInstallationA))
	rest := fmt.Sprintf(`{"results":[%s,%s]}`,
		marshalJSON(t, validInstallation(testInstallationB)),
		marshalJSON(t, validInstallation(testInstallationC)))

	tests := []struct {
		name     string
		trailing string
		accepted bool
	}{
		{name: "a stray closing brace", trailing: "}"},
		{name: "a stray closing bracket", trailing: "]junk-after"},
		{name: "junk text", trailing: "junk-after"},
		{name: "a second object", trailing: `{"results":[]}`},
		{name: "a closing brace hiding a second object", trailing: "} " + rest},
		{name: "a closing bracket hiding a second object", trailing: "] " + rest},
		{name: "trailing whitespace and a newline", trailing: " \n\t ", accepted: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := serveBody(t, http.StatusOK, nil, `{"results":[`+first+`]}`+tc.trailing)

			snapshot, err := listFrom(t, srv)

			if tc.accepted {
				require.NoError(t, err, "a body may end with whitespace")
				assert.True(t, snapshot.Authoritative)
				assert.Equal(t, []string{testInstallationA}, installationIDs(snapshot.Installations))
				return
			}
			require.Error(t, err)
			assert.Empty(t, snapshot.Installations, "nothing may be salvaged from a body carrying more than one value")
			assert.False(t, snapshot.Authoritative)
		})
	}
}

// TestListInstallations_PaginationTripwiresDropAuthorityButKeepRecords pins
// the asymmetry that keeps a future paginated endpoint from reading as "all
// your grants vanished": anything at the top level or in the headers that we
// do not recognise may be the marker saying this is one page of several.
func TestListInstallations_PaginationTripwiresDropAuthorityButKeepRecords(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		body    string
		warning string
	}{
		{
			name:    "an unknown top-level key",
			body:    `{"results":[%s],"nextPageToken":"abc"}`,
			warning: "nextPageToken",
		},
		{
			name:    "a Link header",
			headers: map[string]string{"Link": `<https://cloud.formae.io/api/v1/me/installations?page=2>; rel="next"`},
			body:    `{"results":[%s]}`,
			warning: "Link",
		},
		{
			name:    "a Content-Range header",
			headers: map[string]string{"Content-Range": "items 0-1/97"},
			body:    `{"results":[%s]}`,
			warning: "Content-Range",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(tc.body, marshalJSON(t, validInstallation(testInstallationA)))
			srv, _ := serveBody(t, http.StatusOK, tc.headers, body)

			snapshot, err := listFrom(t, srv)

			require.NoError(t, err, "an unrecognised marker does not fail the request")
			assert.False(t, snapshot.Authoritative)
			assert.Equal(t, []string{testInstallationA}, installationIDs(snapshot.Installations),
				"partial knowledge still adds; only removal needs a complete answer")
			require.NotEmpty(t, snapshot.Warnings)
			assert.Contains(t, strings.Join(snapshot.Warnings, "\n"), tc.warning)
		})
	}
}

// TestListInstallations_AnUnknownFieldInsideARecordKeepsAuthority pins the
// other half of that asymmetry. A new per-installation field says nothing
// about whether the list is complete, and treating it as a pagination marker
// would switch pruning off for every user the day one is added.
func TestListInstallations_AnUnknownFieldInsideARecordKeepsAuthority(t *testing.T) {
	record := validInstallation(testInstallationA)
	record["region"] = "eu-west-1"
	record["features"] = map[string]any{"beta": true}
	srv, _ := serveInstallations(t, record)

	snapshot, err := listFrom(t, srv)

	require.NoError(t, err)
	assert.True(t, snapshot.Authoritative)
	assert.Empty(t, snapshot.Warnings)
	assert.Equal(t, []string{testInstallationA}, installationIDs(snapshot.Installations))
}

// TestListInstallations_DropsUnidentifiableRecordsAndLosesAuthority walks the
// records that cannot be matched against a ledger entry at all. Some
// installation's presence is then genuinely unknown, so the run may not
// subtract — but its siblings are still returned, because adding is
// non-destructive.
func TestListInstallations_DropsUnidentifiableRecordsAndLosesAuthority(t *testing.T) {
	mutate := func(f func(rawInstallation)) rawInstallation {
		r := validInstallation(testInstallationB)
		f(r)
		return r
	}

	tests := []struct {
		name string
		bad  any
	}{
		{name: "null record", bad: nil},
		{name: "record that is not an object", bad: 7},
		{name: "record that is an array", bad: []any{1, 2}},
		{name: "type error in installationId", bad: mutate(func(r rawInstallation) { r["installationId"] = 1 })},
		{name: "type error in endpoint", bad: mutate(func(r rawInstallation) { r["endpoint"] = []string{testOrigin} })},
		{name: "malformed installationId", bad: mutate(func(r rawInstallation) { r["installationId"] = "not-an-installation" })},
		{name: "over-long installationId", bad: mutate(func(r rawInstallation) { r["installationId"] = testInstallationB + "a" })},
		// The identifier format installations used to carry. Nothing mints one
		// any more, so a record naming one identifies no installation.
		{name: "installationId in the retired form", bad: mutate(func(r rawInstallation) { r["installationId"] = "3f2b8c14-0000-4000-8000-000000000000" })},
		{name: "empty installationId", bad: mutate(func(r rawInstallation) { r["installationId"] = "" })},
		{name: "missing installationId", bad: mutate(func(r rawInstallation) { delete(r, "installationId") })},
		{name: "plain http endpoint", bad: mutate(func(r rawInstallation) { r["endpoint"] = "http://cloud.formae.io" })},
		{name: "endpoint with a path", bad: mutate(func(r rawInstallation) { r["endpoint"] = testOrigin + "/api/v1" })},
		{name: "endpoint with a query", bad: mutate(func(r rawInstallation) { r["endpoint"] = testOrigin + "?tenant=acme" })},
		{name: "endpoint with a fragment", bad: mutate(func(r rawInstallation) { r["endpoint"] = testOrigin + "#frag" })},
		{name: "endpoint with userinfo", bad: mutate(func(r rawInstallation) { r["endpoint"] = "https://user:hunter2@cloud.formae.io" })},
		{name: "relative endpoint", bad: mutate(func(r rawInstallation) { r["endpoint"] = "/api/v1" })},
		{name: "empty endpoint", bad: mutate(func(r rawInstallation) { r["endpoint"] = "" })},
		{name: "missing endpoint", bad: mutate(func(r rawInstallation) { delete(r, "endpoint") })},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := serveInstallations(t, validInstallation(testInstallationA), tc.bad)

			snapshot, err := listFrom(t, srv)

			require.NoError(t, err)
			assert.False(t, snapshot.Authoritative)
			assert.Equal(t, []string{testInstallationA}, installationIDs(snapshot.Installations),
				"the valid sibling is still returned")
			require.Len(t, snapshot.Warnings, 1)
		})
	}
}

// TestListInstallations_DropsEveryCopyOfADuplicatedID pins the same rule from
// the other side: an endpoint that breaks its own key contract gives no ground
// to trust the set it returned, and picking one copy of a duplicated id would
// write a profile from a record chosen by nothing but its position.
func TestListInstallations_DropsEveryCopyOfADuplicatedID(t *testing.T) {
	second := validInstallation(testInstallationB)
	second["installationName"] = "staging"

	srv, _ := serveInstallations(t,
		validInstallation(testInstallationB),
		validInstallation(testInstallationA),
		second,
	)

	snapshot, err := listFrom(t, srv)

	require.NoError(t, err)
	assert.False(t, snapshot.Authoritative)
	assert.Equal(t, []string{testInstallationA}, installationIDs(snapshot.Installations),
		"no copy of a duplicated id is believed; the unrelated record survives")
	require.Len(t, snapshot.Warnings, 1)
	assert.Contains(t, snapshot.Warnings[0], testInstallationB)
}

// TestListInstallations_ATypeErrorInOneRecordDoesNotAbortTheBody is the test
// that fails if results is decoded as a plain slice of installations: one
// element's type error would take the whole body down with it, and a body we
// could not read is a body we return nothing from.
func TestListInstallations_ATypeErrorInOneRecordDoesNotAbortTheBody(t *testing.T) {
	broken := validInstallation(testInstallationB)
	broken["installationName"] = 42 // a number where a string belongs.

	srv, _ := serveInstallations(t,
		validInstallation(testInstallationA),
		broken,
		validInstallation(testInstallationC),
	)

	snapshot, err := listFrom(t, srv)

	require.NoError(t, err)
	assert.False(t, snapshot.Authoritative)
	assert.Equal(t, []string{testInstallationA, testInstallationC}, installationIDs(snapshot.Installations),
		"the siblings of a wrongly typed record survive it")
	require.Len(t, snapshot.Warnings, 1)
	assert.Contains(t, snapshot.Warnings[0], `("`,
		"the decode error is quoted, so text the far end reached cannot rewrite the line it lands in")
}

// TestListInstallations_WarningsAreBoundedAndLeakNothing pins what a warning
// may repeat back. Everything in a response is chosen by the far end, so a
// warning names the position of a record and quotes nothing it is not obliged
// to — and never a credential.
func TestListInstallations_WarningsAreBoundedAndLeakNothing(t *testing.T) {
	longKey := strings.Repeat("k", 4000)
	record := validInstallation(testInstallationA)
	record["endpoint"] = "https://user:hunter2@cloud.formae.io"
	body := fmt.Sprintf(`{"results":[%s],%q:1}`, marshalJSON(t, record), longKey)
	srv, _ := serveBody(t, http.StatusOK, nil, body)

	snapshot, err := listFrom(t, srv)

	require.NoError(t, err)
	assert.False(t, snapshot.Authoritative)
	joined := strings.Join(snapshot.Warnings, "\n")
	assert.NotContains(t, joined, "hunter2", "a warning must not repeat a credential back")
	assert.Less(t, len(joined), 1000, "a warning must not flood the terminal with text the far end chose")
	assert.Contains(t, joined, "kkkk", "the unrecognised key is still named")
}
