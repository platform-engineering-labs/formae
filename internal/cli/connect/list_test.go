// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// `connect list` is a read: it authenticates and reads the connections
// registered on the installation, and never touches the admin-gated setup
// endpoint that only provisioning needs. These pin the machine document, the
// route it must (and must not) reach, and how a refusal is classified.

// listStub is a minimal control-plane double for the routes list drives: the
// connections listing and the installations listing its 404 disambiguates
// against. It records every path seen, so a test can assert the setup route
// was never reached.
type listStub struct {
	mu    sync.Mutex
	paths []string

	connectionsStatus int
	connectionsBody   string

	installationsStatus int
	installationsBody   string
}

func newListStub() *listStub {
	return &listStub{
		connectionsStatus:   http.StatusOK,
		connectionsBody:     `{"results":[]}`,
		installationsStatus: http.StatusOK,
		installationsBody:   `{"results":[]}`,
	}
}

func (s *listStub) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.Path)
		status, body := s.route(r)
		s.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (s *listStub) route(r *http.Request) (int, string) {
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/cloud-connections"):
		return s.connectionsStatus, s.connectionsBody
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/me/installations":
		return s.installationsStatus, s.installationsBody
	default:
		return http.StatusNotFound, `{"error":{"code":"not_found"}}`
	}
}

func (s *listStub) recordedPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

// seedListProfile writes a hosted profile naming contractInstallation and
// points the connect env at url, the way seedProfile does for the contract
// tests, but against listStub rather than the contract controlPlane.
func seedListProfile(t *testing.T, url string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "profiles"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "profiles", "prod.pkl"), []byte(hostedProfile(contractInstallation)), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "active"), []byte("prod\n"), 0o600))
	t.Setenv("FORMAE_CONFIG_DIR", dir)
	clearConnectEnv(t)
	t.Setenv("FORMAE_CLOUD_URL", url)
	t.Setenv("FORMAE_CLOUD_ISSUER", "https://auth.formae.ai")
}

// runList runs `connect list` with args appended, and returns what it wrote
// and the error it finished with.
func runList(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	c := ConnectCmd()
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(append([]string{"list"}, args...))
	err := c.Execute()
	return out.String(), err
}

func machineArgs() []string {
	return []string{"--output-consumer", "machine", "--output-schema", "json"}
}

// conn builds one row of a stub connections response. An empty roleArn omits
// the field, the way a non-AWS cloud's record does on the real control plane.
func conn(cloud, account, roleArn string) map[string]any {
	m := map[string]any{"cloud": cloud, "account": account}
	if roleArn != "" {
		m["roleArn"] = roleArn
	}
	return m
}

// snapshotOf renders a cloud-connections response body carrying exactly the
// given rows; called with none, it renders a legitimate empty list rather
// than a null one.
func snapshotOf(rows ...map[string]any) string {
	if rows == nil {
		rows = []map[string]any{}
	}
	data, err := json.Marshal(map[string]any{"results": rows})
	if err != nil {
		panic(err)
	}
	return string(data)
}

// installationsSnapshot describes the /me/installations answer a 404
// disambiguates against: whether it may be believed completely, and whether
// it lists the installation being read.
type installationsSnapshot struct {
	Authoritative bool
	Listed        bool
}

// runListNotFound runs list against a connections endpoint that 404s, with
// the installations listing built from snap, and returns the run's error.
func runListNotFound(t *testing.T, snap installationsSnapshot) error {
	t.Helper()
	stub := newListStub()
	stub.connectionsStatus = http.StatusNotFound
	stub.connectionsBody = `{"error":{"code":"not_found"}}`
	switch {
	case !snap.Authoritative:
		stub.installationsBody = `{"results":[],"nextPageToken":"abc"}` // one page of several
	case snap.Listed:
		stub.installationsBody = meBodyListing(t, contractInstallation)
	default:
		stub.installationsBody = meBodyListing(t, otherInstallation)
	}
	url := stub.start(t)
	seedListProfile(t, url)
	stubCredentials(t, bearerAnswer("t1"))

	_, err := runList(t, machineArgs()...)
	return err
}

// runListStatus runs list against a connections endpoint answering status,
// and returns the run's error.
func runListStatus(t *testing.T, status int) error {
	t.Helper()
	stub := newListStub()
	stub.connectionsStatus = status
	stub.connectionsBody = `{"error":{"code":"denied"}}`
	url := stub.start(t)
	seedListProfile(t, url)
	stubCredentials(t, bearerAnswer("t1"))

	_, err := runList(t, machineArgs()...)
	return err
}

// The document a consumer branches on: connections always present, and a
// non-AWS row carries no empty role ARN.
func TestConnectList_MachineDocument(t *testing.T) {
	stub := newListStub()
	stub.connectionsBody = snapshotOf(
		conn("aws", testAccount, "arn:aws:iam::"+testAccount+":role/r"),
		conn("gcp", "my-project", ""),
	)
	url := stub.start(t)
	seedListProfile(t, url)
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runList(t, machineArgs()...)
	require.NoError(t, err, "out: %s", out)

	doc := decodeDoc(t, out)
	assert.Equal(t, "connections", doc["phase"])
	assert.Equal(t, float64(2), doc["schemaVersion"])
	assert.Equal(t, true, doc["complete"])
	assert.Equal(t, contractInstallation, doc["installation"])

	rows, ok := doc["connections"].([]any)
	require.True(t, ok, "connections is not an array: %s", out)
	require.Len(t, rows, 2)
	if _, present := rows[1].(map[string]any)["roleArn"]; present {
		t.Error("a GCP row carries a roleArn key")
	}
}

// A nil slice would encode as null, and a consumer must never have to tell
// absent from empty.
func TestConnectList_EmptyIsAnArray(t *testing.T) {
	stub := newListStub()
	stub.connectionsBody = snapshotOf()
	url := stub.start(t)
	seedListProfile(t, url)
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runList(t, machineArgs()...)
	require.NoError(t, err, "out: %s", out)
	assert.Contains(t, out, `"connections":[]`)
}

// The list is member-readable, so it must reach the connections endpoint
// without ever touching the admin-gated setup endpoint. Asserting only the
// absence would also pass if the command failed before authenticating.
func TestConnectList_ReachesConnectionsAndNotSetup(t *testing.T) {
	stub := newListStub()
	url := stub.start(t)
	seedListProfile(t, url)
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runList(t, machineArgs()...)
	require.NoError(t, err, "the run failed, so the path assertions prove nothing: out=%s", out)

	paths := stub.recordedPaths()
	assert.True(t, slices.ContainsFunc(paths, func(p string) bool { return strings.Contains(p, "/cloud-connections") }),
		"never called the connections endpoint")
	for _, p := range paths {
		assert.NotContains(t, p, "cloud-connection-setup", "called the admin-gated setup endpoint")
	}
}

// 403 means a member whose tenant grant excludes this installation. The
// server answers non-membership with 404, so this message must claim
// neither.
func TestConnectList_ForbiddenMessageClaimsNothingAboutMembership(t *testing.T) {
	err := runListStatus(t, http.StatusForbidden)
	failureCode(t, err, printer.CodeNotAuthorized)
	msg := strings.ToLower(err.Error())
	for _, word := range []string{"member", "admin"} {
		assert.NotContains(t, msg, word)
	}
}

// A 401 means the session itself has lapsed, distinct from a 403.
func TestConnectList_SessionLapsedIsAuthFailed(t *testing.T) {
	err := runListStatus(t, http.StatusUnauthorized)
	failureCode(t, err, printer.CodeAuthFailed)
}

// Absence from a non-authoritative installations snapshot is not evidence the
// installation does not exist.
func TestConnectList_NotFoundAgainstAPartialSnapshotCannotDecide(t *testing.T) {
	err := runListNotFound(t, installationsSnapshot{Authoritative: false})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not", "a partial snapshot produced a definite answer: %v", err)

	var fail *printer.Failure
	assert.False(t, errors.As(err, &fail), "a partial snapshot must not be reported as a declared, definite code")
}

// Listed in the installations listing but 404 on connections means the
// control plane predates the route.
func TestConnectList_NotFoundButListedIsControlPlaneTooOld(t *testing.T) {
	err := runListNotFound(t, installationsSnapshot{Authoritative: true, Listed: true})
	failureCode(t, err, printer.CodeControlPlaneTooOld)
}

// Unlisted against an authoritative listing means genuinely not visible.
func TestConnectList_NotFoundAndUnlistedIsNotVisible(t *testing.T) {
	err := runListNotFound(t, installationsSnapshot{Authoritative: true, Listed: false})
	failureCode(t, err, printer.CodeNotAuthorized)
}

// A record a cloud understands but that carries an anomaly (here, no role
// ARN for an AWS record) is dropped rather than aborting the body, and its
// absence is reported through Complete rather than silently.
func TestConnectList_IncompleteListingReportsNotCompleteWithAWarning(t *testing.T) {
	stub := newListStub()
	stub.connectionsBody = `{"results":[{"cloud":"aws","account":"` + testAccount + `"}]}`
	url := stub.start(t)
	seedListProfile(t, url)
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runList(t, machineArgs()...)
	require.NoError(t, err, "out: %s", out)

	doc := decodeDoc(t, out)
	assert.Equal(t, false, doc["complete"])
	warnings, ok := doc["warnings"].([]any)
	require.True(t, ok, "the drop is reported through a warning: %s", out)
	assert.NotEmpty(t, warnings)
}

// Human output names the installation, carries the cloud, account, and role
// for an AWS row, never carries the machine document, and never claims a
// connection was "verified" (formae never verifies that a registration's
// trust is actually usable).
func TestConnectList_HumanOutputNamesTheInstallation(t *testing.T) {
	roleArn := "arn:aws:iam::" + testAccount + ":role/r"
	stub := newListStub()
	stub.connectionsBody = snapshotOf(conn("aws", testAccount, roleArn))
	url := stub.start(t)
	seedListProfile(t, url)
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runList(t)
	require.NoError(t, err, "out: %s", out)
	assert.Contains(t, out, contractInstallation)
	assert.Contains(t, out, "aws")
	assert.Contains(t, out, testAccount)
	assert.Contains(t, out, roleArn)
	assert.NotContains(t, out, "schemaVersion")
	assert.NotContains(t, strings.ToLower(out), "verified")
}

// The spec pins the empty-and-complete sentence exactly: no more, no less,
// and no claim hedged by an unrelated warning that was never raised.
func TestConnectList_HumanOutputEmptyAndCompleteIsTheExactSentence(t *testing.T) {
	stub := newListStub()
	stub.connectionsBody = snapshotOf()
	url := stub.start(t)
	seedListProfile(t, url)
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runList(t)
	require.NoError(t, err, "out: %s", out)
	assert.Equal(t, "No cloud accounts are registered on "+contractInstallation+".\n", out)
}

// An incomplete listing says plainly that it may be partial, prints the
// warnings that explain why, and never presents the rows it did read as if
// they were the whole count.
func TestConnectList_HumanOutputIncompleteSaysPartialAndNeverACount(t *testing.T) {
	stub := newListStub()
	// One valid AWS row plus one record missing its role: the second is
	// dropped with a warning, so the listing is one row short of complete.
	stub.connectionsBody = `{"results":[` +
		`{"cloud":"aws","account":"` + testAccount + `","roleArn":"arn:aws:iam::` + testAccount + `:role/r"},` +
		`{"cloud":"aws","account":"999999999999"}]}`
	url := stub.start(t)
	seedListProfile(t, url)
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runList(t)
	require.NoError(t, err, "out: %s", out)

	lower := strings.ToLower(out)
	assert.Contains(t, lower, "partial", "an incomplete listing must say plainly that it may be partial")
	assert.NotContains(t, lower, "no cloud accounts are registered",
		"a partial listing must not read as the empty-and-complete sentence")
	assert.NotRegexp(t, `\b1\s+(cloud account|connection)s?\b`, lower,
		"the one row read must not be presented as if it were the total count")
	assert.Contains(t, out, testAccount, "the row that was read is still shown")
}
