// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/connection"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

// These exercise the command as a consumer meets it: real flag parsing, real
// profile selection, real rendering, against an httptest control plane whose
// stubs encode the shared contract byte for byte.

const (
	contractInstallation = "3HzFPXfPDGhwLJJVtaHbmFs6vLa"
	otherInstallation    = "2ZaBcDeFgHiJkLmNoPqRsTuVwXy"
	contractRoleName     = "formae-connect-" + contractInstallation
	contractRoleArn      = "arn:aws:iam::" + testAccount + ":role/" + contractRoleName
)

const classicProfile = `amends "formae:/Config.pkl"

cli {
    connection = new Classic {
        url = "http://localhost"
        port = 49684
    }
}
`

func hostedProfile(installation string) string {
	return `amends "formae:/Config.pkl"

cli {
    connection = new Hosted {
        endpoint = "https://cloud.formae.ai"
        installation = "` + installation + `"
        auth = new Dynamic {
            type = "oidc"
            role = "cli"
            issuer = "https://auth.formae.ai"
        }
    }
}
`
}

// cpRequest is one request the stub control plane saw.
type cpRequest struct {
	Method string
	Path   string
	Auth   string
	Body   string
}

// controlPlane is a configurable stub of the endpoints connect drives.
type controlPlane struct {
	mu   sync.Mutex
	reqs []cpRequest

	setupStatus int
	setupBody   string

	registerStatus int
	registerBody   string

	connectionsBody string

	meStatus int
	meBody   string

	srv *httptest.Server
}

func newControlPlane(t *testing.T) *controlPlane {
	t.Helper()
	cp := &controlPlane{
		setupStatus:     http.StatusOK,
		registerStatus:  http.StatusCreated,
		registerBody:    `{"cloud":"aws","account":"` + testAccount + `","roleArn":"` + contractRoleArn + `"}`,
		connectionsBody: `{"results":[]}`,
		meStatus:        http.StatusOK,
		meBody:          `{"results":[]}`,
	}
	cp.setupBody = defaultSetupBody(t, nil)

	cp.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cp.mu.Lock()
		cp.reqs = append(cp.reqs, cpRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Auth:   r.Header.Get("Authorization"),
			Body:   string(body),
		})
		status, answer := cp.route(r)
		cp.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(cp.srv.Close)
	return cp
}

func (cp *controlPlane) route(r *http.Request) (int, string) {
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/cloud-connection-setup"):
		return cp.setupStatus, cp.setupBody
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cloud-connections"):
		return cp.registerStatus, cp.registerBody
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/cloud-connections"):
		return http.StatusOK, cp.connectionsBody
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/me/installations":
		return cp.meStatus, cp.meBody
	default:
		return http.StatusNotFound, `{"error":{"code":"not_found"}}`
	}
}

func (cp *controlPlane) requests() []cpRequest {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return append([]cpRequest(nil), cp.reqs...)
}

func (cp *controlPlane) posts() []cpRequest {
	var posts []cpRequest
	for _, r := range cp.requests() {
		if r.Method == http.MethodPost {
			posts = append(posts, r)
		}
	}
	return posts
}

func defaultSetupBody(t *testing.T, hints []map[string]any) string {
	t.Helper()
	if hints == nil {
		hints = []map[string]any{}
	}
	data, err := json.Marshal(map[string]any{
		"cloudSubject":          "fai:acme/" + contractInstallation,
		"cloudRoleName":         contractRoleName,
		"issuer":                "https://oidc.cloud.formae.ai",
		"accountsConnectedHint": hints,
	})
	require.NoError(t, err)
	return string(data)
}

func meBodyListing(t *testing.T, installations ...string) string {
	t.Helper()
	records := make([]map[string]any, 0, len(installations))
	for _, id := range installations {
		records = append(records, map[string]any{
			"installationId":   id,
			"installationName": "prod",
			"tenantName":       "acme",
			"orgName":          "acme-inc",
			"endpoint":         "https://cloud.formae.ai",
			"state":            "active",
		})
	}
	data, err := json.Marshal(map[string]any{"results": records})
	require.NoError(t, err)
	return string(data)
}

// seedProfile writes a config dir holding one profile and points formae at it
// and at the stub control plane.
func seedProfile(t *testing.T, cp *controlPlane, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "profiles"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "profiles", "prod.pkl"), []byte(body), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "active"), []byte("prod\n"), 0o600))
	t.Setenv("FORMAE_CONFIG_DIR", dir)
	pointEnvAt(t, cp)
	return dir
}

func pointEnvAt(t *testing.T, cp *controlPlane) {
	t.Helper()
	clearConnectEnv(t)
	if cp != nil {
		t.Setenv("FORMAE_CLOUD_URL", cp.srv.URL)
	} else {
		t.Setenv("FORMAE_CLOUD_URL", "http://127.0.0.1:1")
	}
	t.Setenv("FORMAE_CLOUD_ISSUER", "https://auth.formae.ai")
}

// stubCreds answers GetAuthHeader from a script of bearers and errors, and
// records the force flag of every call.
type stubCreds struct {
	mu      sync.Mutex
	answers []func() (*pkgauth.GetAuthHeaderResponse, error)
	forced  []bool
	calls   int
}

func (s *stubCreds) GetAuthHeader(forceRefresh bool) (*pkgauth.GetAuthHeaderResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forced = append(s.forced, forceRefresh)
	i := s.calls
	s.calls++
	if i >= len(s.answers) {
		i = len(s.answers) - 1
	}
	return s.answers[i]()
}

func bearerAnswer(token string) func() (*pkgauth.GetAuthHeaderResponse, error) {
	return func() (*pkgauth.GetAuthHeaderResponse, error) {
		return &pkgauth.GetAuthHeaderResponse{
			Headers: map[string][]string{"Authorization": {"Bearer " + token}},
		}, nil
	}
}

func refusedAnswer() func() (*pkgauth.GetAuthHeaderResponse, error) {
	return func() (*pkgauth.GetAuthHeaderResponse, error) {
		return &pkgauth.GetAuthHeaderResponse{Error: "session expired", ErrorCode: "session_expired"}, nil
	}
}

// stubCredentials installs creds as the credential provider for the run.
func stubCredentials(t *testing.T, answers ...func() (*pkgauth.GetAuthHeaderResponse, error)) *stubCreds {
	t.Helper()
	creds := &stubCreds{answers: answers}
	restore := newCredentials
	newCredentials = func(_ *app.App) credentialProvider { return creds }
	t.Cleanup(func() { newCredentials = restore })
	return creds
}

// hostedOpts seeds a hosted profile against a stub control plane and stubs a
// credential, returning the options an authenticated control-plane call
// needs. The stub control plane and credential are not asserted on here; the
// point is a run that authenticates cleanly.
func hostedOpts(t *testing.T) options {
	t.Helper()
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))
	return options{}
}

// Listing is a control-plane read and must not depend on the AWS-side template
// and issuer pin, which only the provisioning paths use.
func TestOpenControlPlane_IgnoresConnectPlatformOverrides(t *testing.T) {
	t.Setenv("FORMAE_CONNECT_ISSUER", "not a url")
	if _, err := openControlPlane(context.Background(), hostedOpts(t)); err != nil {
		t.Fatalf("a malformed AWS-side override broke a control-plane read: %v", err)
	}
}

func registerOnlyArgs() []string {
	return []string{"aws", "--account", testAccount, "--role-arn", contractRoleArn, "--no-input",
		"--output-consumer", "machine", "--output-schema", "json"}
}

func decodeOut(t *testing.T, out string) map[string]any {
	t.Helper()
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got), "output is not json: %s", out)
	return got
}

// The clean-machine property: a machine that has never been configured gets
// hosted_required through the machine protocol, and deciding that never
// manufactures a profile. Both env pairs are cleared, so this is the exact
// state a new machine is in.
func TestContractCleanMachineGetsHostedRequiredAndBootstrapsNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORMAE_CONFIG_DIR", dir)
	clearConnectEnv(t)
	for _, k := range []string{"FORMAE_CLOUD_URL", "FORMAE_CLOUD_ISSUER"} {
		k := k
		if old, ok := os.LookupEnv(k); ok {
			t.Cleanup(func() { _ = os.Setenv(k, old) })
		}
		require.NoError(t, os.Unsetenv(k))
	}
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, "aws", "--account", testAccount, "--quick-create", "--no-input",
		"--output-consumer", "machine", "--output-schema", "json")

	require.Error(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "hosted_required", got["code"])
	assert.NoFileExists(t, store.New(dir).ProfilePath("default"),
		"connect bootstrapped a classic localhost profile")
}

// The negative control that gives the property its teeth: a command that does
// bootstrap creates the default on the very same directory, so the absence
// above is about connect and not about a temp dir nothing ever wrote to.
func TestContractCleanMachineNegativeControl(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORMAE_CONFIG_DIR", dir)

	c := connection.ConnectionCmd()
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetArgs([]string{"resolve", "--output-consumer", "machine", "--output-schema", "json"})
	_ = c.Execute()

	assert.FileExists(t, store.New(dir).ProfilePath("default"),
		"the bootstrapping path no longer bootstraps, so the clean-machine assertion proves nothing")
}

func TestContractYAMLVariant(t *testing.T) {
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, "aws", "--account", testAccount, "--role-arn", contractRoleArn, "--no-input",
		"--output-consumer", "machine", "--output-schema", "yaml")

	require.NoError(t, err, "out: %s", out)
	assert.Contains(t, out, "phase: registered")
	assert.Contains(t, out, "schemaVersion: 2")
	assert.Contains(t, out, "status: registered_unverified")
}

// Human output renders the same facts as prose, never the machine document.
func TestContractHumanOutputCarriesNoJSON(t *testing.T) {
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, "aws", "--account", testAccount, "--role-arn", contractRoleArn, "--no-input")

	require.NoError(t, err, "out: %s", out)
	assert.NotContains(t, out, "schemaVersion")
	assert.NotContains(t, out, "{")
	assert.Contains(t, out, "registered aws account "+testAccount)
	assert.Contains(t, out, contractRoleArn)
	assert.Contains(t, out, "registered aws account")
}

func TestContractClassicProfileIsHostedRequired(t *testing.T) {
	cp := newControlPlane(t)
	seedProfile(t, cp, classicProfile)
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, registerOnlyArgs()...)

	require.Error(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "hosted_required", got["code"])
	assert.Empty(t, cp.requests(), "a classic profile makes no control-plane request")
}

// A machine that has never been configured gets hosted_required, and the run
// manufactures no classic default profile deciding it.
func TestContractBareMachineIsHostedRequiredAndBootstrapsNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORMAE_CONFIG_DIR", dir)
	pointEnvAt(t, nil)
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, registerOnlyArgs()...)

	require.Error(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "hosted_required", got["code"])
	assert.NoFileExists(t, store.New(dir).ProfilePath("default"),
		"connect bootstrapped a classic localhost profile")
}

func TestContractSetupForbiddenIsNotAuthorized(t *testing.T) {
	cp := newControlPlane(t)
	cp.setupStatus = http.StatusForbidden
	cp.setupBody = `{"error":{"code":"forbidden"}}`
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, registerOnlyArgs()...)

	require.Error(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "not_authorized", got["code"])
	assert.Empty(t, cp.posts(), "a refused setup registers nothing")
}

// A 404 from the setup endpoint is ambiguous on its own; the listing the
// caller can already fetch disambiguates it.
func TestContractSetup404Disambiguation(t *testing.T) {
	t.Run("listed installation means the control plane is too old", func(t *testing.T) {
		cp := newControlPlane(t)
		cp.setupStatus = http.StatusNotFound
		cp.setupBody = `{"error":{"code":"not_found"}}`
		cp.meBody = meBodyListing(t, contractInstallation)
		seedProfile(t, cp, hostedProfile(contractInstallation))
		stubCredentials(t, bearerAnswer("t1"))

		out, err := runConnect(t, registerOnlyArgs()...)

		require.Error(t, err)
		got := decodeOut(t, out)
		assert.Equal(t, "control_plane_too_old", got["code"])
	})

	t.Run("an authoritative listing without it means not visible", func(t *testing.T) {
		cp := newControlPlane(t)
		cp.setupStatus = http.StatusNotFound
		cp.setupBody = `{"error":{"code":"not_found"}}`
		cp.meBody = meBodyListing(t, otherInstallation)
		seedProfile(t, cp, hostedProfile(contractInstallation))
		stubCredentials(t, bearerAnswer("t1"))

		out, err := runConnect(t, registerOnlyArgs()...)

		require.Error(t, err)
		got := decodeOut(t, out)
		assert.Equal(t, "not_authorized", got["code"])
		details, ok := got["details"].(map[string]any)
		require.True(t, ok, "the refusal carries details: %s", out)
		assert.Equal(t, "not_visible", details["reason"])
		assert.Contains(t, got["message"], "login", "the guidance names refreshing the grants")
	})

	t.Run("a non-authoritative listing concludes nothing about authorization", func(t *testing.T) {
		cp := newControlPlane(t)
		cp.setupStatus = http.StatusNotFound
		cp.setupBody = `{"error":{"code":"not_found"}}`
		cp.meBody = `{"results":[],"nextPageToken":"abc"}` // one page of several
		seedProfile(t, cp, hostedProfile(contractInstallation))
		stubCredentials(t, bearerAnswer("t1"))

		out, err := runConnect(t, registerOnlyArgs()...)

		require.Error(t, err)
		got := decodeOut(t, out)
		assert.NotEqual(t, "not_authorized", got["code"],
			"an incomplete listing licenses no claim about authorization: %s", out)
		assert.NotEqual(t, "control_plane_too_old", got["code"])
	})
}

func TestContractNotReadyCarriesTheState(t *testing.T) {
	cp := newControlPlane(t)
	cp.setupStatus = http.StatusConflict
	cp.setupBody = `{"error":{"code":"installation_not_ready","message":"not ready","details":{"state":"provisioning"}}}`
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, registerOnlyArgs()...)

	require.Error(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "installation_not_ready", got["code"])
	details, ok := got["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "provisioning", details["state"])
}

// The issuer the setup response names must be the pinned one; anything else
// stops the run before any further step.
func TestContractIssuerPin(t *testing.T) {
	t.Run("a foreign issuer is refused", func(t *testing.T) {
		cp := newControlPlane(t)
		cp.setupBody = strings.Replace(defaultSetupBody(t, nil),
			"https://oidc.cloud.formae.ai", "https://oidc.evil.example", 1)
		seedProfile(t, cp, hostedProfile(contractInstallation))
		stubCredentials(t, bearerAnswer("t1"))

		out, err := runConnect(t, registerOnlyArgs()...)

		require.Error(t, err)
		got := decodeOut(t, out)
		assert.Equal(t, "untrusted_issuer", got["code"])
		assert.Empty(t, cp.posts(), "an untrusted issuer registers nothing")
	})

	t.Run("a slash-terminated spelling of the pinned issuer passes", func(t *testing.T) {
		cp := newControlPlane(t)
		cp.setupBody = strings.Replace(defaultSetupBody(t, nil),
			"https://oidc.cloud.formae.ai", "https://oidc.cloud.formae.ai/", 1)
		seedProfile(t, cp, hostedProfile(contractInstallation))
		stubCredentials(t, bearerAnswer("t1"))

		out, err := runConnect(t, registerOnlyArgs()...)

		require.NoError(t, err, "out: %s", out)
		got := decodeOut(t, out)
		assert.Equal(t, "registered", got["phase"])
	})
}

// The credential is force-refreshed at both control-plane boundaries, and a
// credential that expires between them stops the run before the registration
// POST: no request carries a stale bearer.
func TestContractCredentialExpiryBetweenTheBoundaries(t *testing.T) {
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	creds := stubCredentials(t, bearerAnswer("fresh-one"), refusedAnswer())

	out, err := runConnect(t, registerOnlyArgs()...)

	require.Error(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "auth_failed", got["code"])
	assert.Empty(t, cp.posts(), "no registration POST may carry a stale bearer")
	require.Equal(t, 2, creds.calls, "both boundaries mint their own credential")
	assert.Equal(t, []bool{true, true}, creds.forced, "both boundaries force-refresh")
}

func TestContractRegisterSucceeds(t *testing.T) {
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, registerOnlyArgs()...)

	require.NoError(t, err, "out: %s", out)
	got := decodeOut(t, out)
	assert.Equal(t, float64(2), got["schemaVersion"])
	assert.Equal(t, "registered", got["phase"])
	assert.Equal(t, "registered_unverified", got["status"])
	assert.Equal(t, "aws", got["cloud"])
	assert.Equal(t, testAccount, got["account"])
	assert.Equal(t, contractRoleArn, got["roleArn"])

	posts := cp.posts()
	require.Len(t, posts, 1)
	assert.Equal(t, "/api/v1/installations/"+contractInstallation+"/cloud-connections", posts[0].Path)
	assert.JSONEq(t,
		`{"cloud":"aws","account":"`+testAccount+`","roleArn":"`+contractRoleArn+`"}`,
		posts[0].Body)
	assert.Equal(t, "Bearer t1", posts[0].Auth)
}

// The hint naming this account on another installation warns and proceeds:
// in --no-input the warning rides the machine document.
func TestContractMultiInstallationHintWarnsAndProceeds(t *testing.T) {
	cp := newControlPlane(t)
	cp.setupBody = defaultSetupBody(t, []map[string]any{{
		"cloud":            "aws",
		"account":          testAccount,
		"installationId":   otherInstallation,
		"installationName": "staging",
		"tenantName":       "acme",
		"orgName":          "acme-inc",
	}})
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, registerOnlyArgs()...)

	require.NoError(t, err, "out: %s", out)
	got := decodeOut(t, out)
	assert.Equal(t, "registered_unverified", got["status"])
	warnings, ok := got["warnings"].([]any)
	require.True(t, ok, "the warning rides the document: %s", out)
	joined := fmt.Sprintf("%v", warnings)
	assert.Contains(t, joined, "staging")
	require.Len(t, cp.posts(), 1, "the run proceeds to registration")
}

// A role ARN that fails validation is refused before any control-plane
// request: no setup read, no registration, nothing for a stub to see.
func TestContractRoleArnValidationPrecedesEveryWrite(t *testing.T) {
	tests := []struct {
		name string
		arn  string
		code string
	}{
		{name: "malformed", arn: "not-an-arn", code: "unsupported_partition"},
		{name: "govcloud", arn: "arn:aws-us-gov:iam::" + testAccount + ":role/r", code: "unsupported_partition"},
		{name: "china", arn: "arn:aws-cn:iam::" + testAccount + ":role/r", code: "unsupported_partition"},
		{name: "wrong account", arn: "arn:aws:iam::999999999999:role/r", code: "account_mismatch"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cp := newControlPlane(t)
			seedProfile(t, cp, hostedProfile(contractInstallation))
			stubCredentials(t, bearerAnswer("t1"))

			out, err := runConnect(t, "aws", "--account", testAccount, "--role-arn", tc.arn, "--no-input",
				"--output-consumer", "machine", "--output-schema", "json")

			require.Error(t, err)
			got := decodeOut(t, out)
			assert.Equal(t, tc.code, got["code"])
			assert.Empty(t, cp.requests(), "validation must precede every control-plane request")
		})
	}
}

// A role name differing from the installation's cloudRoleName registers the
// ARN the user named, with a warning naming both.
func TestContractNameMismatchRegistersTheActualArnWithAWarning(t *testing.T) {
	ownArn := "arn:aws:iam::" + testAccount + ":role/my-own-role"
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, "aws", "--account", testAccount, "--role-arn", ownArn, "--no-input",
		"--output-consumer", "machine", "--output-schema", "json")

	require.NoError(t, err, "out: %s", out)
	got := decodeOut(t, out)
	assert.Equal(t, ownArn, got["roleArn"], "the actual ARN is registered, not the expected one")
	warnings, ok := got["warnings"].([]any)
	require.True(t, ok, "the mismatch warning rides the document: %s", out)
	joined := fmt.Sprintf("%v", warnings)
	assert.Contains(t, joined, "my-own-role")
	assert.Contains(t, joined, contractRoleName)

	posts := cp.posts()
	require.Len(t, posts, 1)
	assert.Contains(t, posts[0].Body, ownArn)
}

// --quick-create --no-input emits the links document and registers nothing.
func TestContractQuickCreateNoInputEmitsLinksAndRegistersNothing(t *testing.T) {
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, "aws", "--account", testAccount, "--quick-create", "--no-input",
		"--output-consumer", "machine", "--output-schema", "json")

	require.NoError(t, err, "out: %s", out)
	got := decodeOut(t, out)
	assert.Equal(t, float64(2), got["schemaVersion"])
	assert.Equal(t, "links", got["phase"])
	assert.Equal(t, "aws", got["cloud"])
	assert.Equal(t, testAccount, got["account"])
	assert.Equal(t, contractInstallation, got["installation"])
	assert.Contains(t, got["stackUrl"], "formae-connect-"+contractInstallation)
	assert.Contains(t, got["stackUrl"], "param_CreateProvider=true")
	assert.Equal(t, true, got["createProvider"])
	assert.NotContains(t, out, "providerStackUrl")
	assert.Equal(t, contractRoleArn, got["expectedRoleArn"])
	assert.Contains(t, got["resumeCommand"], "--role-arn")
	assert.Empty(t, cp.posts(), "quick-create registers nothing")
}

// --provider-exists rides the machine document and flips the link parameter,
// so a conversational harness can ask the question and pass the answer.
func TestContractQuickCreateProviderExists(t *testing.T) {
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	out, err := runConnect(t, "aws", "--account", testAccount, "--quick-create", "--provider-exists",
		"--no-input", "--output-consumer", "machine", "--output-schema", "json")

	require.NoError(t, err, "out: %s", out)
	got := decodeOut(t, out)
	assert.Contains(t, got["stackUrl"], "param_CreateProvider=false")
	assert.Equal(t, false, got["createProvider"])
}

// When the hint knows the account, interactive quick-create asks whether the
// provider already exists and carries the answer into the link.
func TestContractQuickCreateAsksProviderQuestionWhenHintKnowsTheAccount(t *testing.T) {
	interactiveTTY(t)
	cp := newControlPlane(t)
	cp.setupBody = defaultSetupBody(t, []map[string]any{{
		"cloud": "aws", "account": testAccount,
		"installationId": contractInstallation, "installationName": "inst",
		"tenantName": "default", "orgName": "acme",
	}})
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))
	stubConfirms(t, true)
	asked := stubProviderExistsPrompt(t, true, nil)
	stubRoleArnPrompt(t, "", nil)

	out, err := runConnect(t, "aws", "--account", testAccount, "--quick-create")

	require.NoError(t, err, "out: %s", out)
	assert.Equal(t, 1, *asked, "the provider question is asked when the hint knows the account")
	assert.Contains(t, out, "param_CreateProvider=false")
}

// A fresh account asks nothing: the common case stays one link, zero
// questions, provider created by default.
func TestContractQuickCreateSkipsProviderQuestionForFreshAccount(t *testing.T) {
	interactiveTTY(t)
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))
	stubConfirms(t, true)
	asked := stubProviderExistsPrompt(t, true, nil)
	stubRoleArnPrompt(t, "", nil)

	out, err := runConnect(t, "aws", "--account", testAccount, "--quick-create")

	require.NoError(t, err, "out: %s", out)
	assert.Equal(t, 0, *asked, "no question for a fresh account")
	assert.Contains(t, out, "param_CreateProvider=true")
}

// Interactive quick-create finishes on a bare Enter: the expected ARN is
// registered without a paste. A pasted ARN still wins when it differs.
func TestContractQuickCreateEnterRegistersExpectedArn(t *testing.T) {
	interactiveTTY(t)
	cp := newControlPlane(t)
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))
	stubConfirms(t, true)
	stubRoleArnPrompt(t, "", nil)

	out, err := runConnect(t, "aws", "--account", testAccount, "--quick-create")

	require.NoError(t, err, "out: %s", out)
	posts := cp.posts()
	require.Len(t, posts, 1, "Enter registers the expected ARN")
	assert.Contains(t, posts[0].Body, contractRoleArn)
}

// 409 answers are disambiguated by reading the listing: the same ARN is the
// idempotent success, a different one is a conflict naming both.
func TestContractRegistrationConflict(t *testing.T) {
	t.Run("same arn is already_registered", func(t *testing.T) {
		cp := newControlPlane(t)
		cp.registerStatus = http.StatusConflict
		cp.registerBody = `{"error":{"code":"cloud_connection_exists"}}`
		cp.connectionsBody = `{"results":[{"cloud":"aws","account":"` + testAccount + `","roleArn":"` + contractRoleArn + `"}]}`
		seedProfile(t, cp, hostedProfile(contractInstallation))
		stubCredentials(t, bearerAnswer("t1"))

		out, err := runConnect(t, registerOnlyArgs()...)

		require.NoError(t, err, "out: %s", out)
		got := decodeOut(t, out)
		assert.Equal(t, "already_registered", got["status"])
	})

	t.Run("a different arn is registration_conflict naming both", func(t *testing.T) {
		other := "arn:aws:iam::" + testAccount + ":role/some-other-role"
		cp := newControlPlane(t)
		cp.registerStatus = http.StatusConflict
		cp.registerBody = `{"error":{"code":"cloud_connection_exists"}}`
		cp.connectionsBody = `{"results":[{"cloud":"aws","account":"` + testAccount + `","roleArn":"` + other + `"}]}`
		seedProfile(t, cp, hostedProfile(contractInstallation))
		stubCredentials(t, bearerAnswer("t1"))

		out, err := runConnect(t, registerOnlyArgs()...)

		require.Error(t, err)
		got := decodeOut(t, out)
		assert.Equal(t, "registration_conflict", got["code"])
		details, ok := got["details"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, other, details["registeredRoleArn"])
		assert.Equal(t, contractRoleArn, details["statedRoleArn"])
	})
}

// registerAgainstConflict runs a registration the control plane refuses with
// 409, against a connections listing built by one of the listingXWithout
// helpers below, and returns the error the run finished with.
func registerAgainstConflict(t *testing.T, connectionsBody string) error {
	t.Helper()
	cp := newControlPlane(t)
	cp.registerStatus = http.StatusConflict
	cp.registerBody = `{"error":{"code":"cloud_connection_exists"}}`
	cp.connectionsBody = connectionsBody
	seedProfile(t, cp, hostedProfile(contractInstallation))
	stubCredentials(t, bearerAnswer("t1"))

	_, err := runConnect(t, registerOnlyArgs()...)
	return err
}

// listingIncompleteWithout is a connections listing that carries no
// connection for account and dropped a record along the way, so Complete is
// false: this run cannot tell "not registered" from "not fully read".
func listingIncompleteWithout(account string) string {
	other := otherAccount(account)
	return `{"results":[{"cloud":"aws","account":"` + other + `","roleArn":"arn:aws:iam::` + other + `:role/other"},` +
		`{"cloud":"digitalocean","account":"555555555555"}]}`
}

// listingCompleteWithout is a connections listing that was read in full (so
// Complete is true) and still carries no connection for account.
func listingCompleteWithout(account string) string {
	other := otherAccount(account)
	return `{"results":[{"cloud":"aws","account":"` + other + `","roleArn":"arn:aws:iam::` + other + `:role/other"}]}`
}

// otherAccount is any account distinct from the one passed in, so a listing
// can carry a real, non-matching connection rather than an empty list.
func otherAccount(account string) string {
	if account == "999999999999" {
		return "888888888888"
	}
	return "999999999999"
}

// Absence from a listing that is known to be partial settles nothing, so the
// caller is told it could not compare rather than that the row conflicts. It
// must not carry the registration_conflict code either: that code says the
// control plane's refusal has been corroborated, which a partial listing
// cannot do.
func TestRegister_IncompleteAndUnmatchedCannotCompare(t *testing.T) {
	err := registerAgainstConflict(t, listingIncompleteWithout("123456789012"))
	if err == nil || !strings.Contains(err.Error(), "not visible to compare") {
		t.Fatalf("got %v", err)
	}
	var fail *printer.Failure
	if errors.As(err, &fail) {
		t.Fatalf("an incomplete listing must not be reported as a corroborated conflict, got code %q", fail.Code)
	}
}

// Absence from a complete listing is conclusive: the control plane refused the
// registration as a duplicate and the existing row is genuinely not there. The
// message must not claim it "could not compare": the listing was read in
// full, so the absence is evidence, not a gap.
func TestRegister_CompleteAndUnmatchedIsADuplicate(t *testing.T) {
	err := registerAgainstConflict(t, listingCompleteWithout("123456789012"))
	var fail *printer.Failure
	if !errors.As(err, &fail) || fail.Code != printer.CodeRegistrationConflict {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), "not visible to compare") {
		t.Fatalf("a complete listing settles the question; the message must not hedge: %v", err)
	}
}
