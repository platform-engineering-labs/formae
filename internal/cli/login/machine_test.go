// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgauth "github.com/platform-engineering-labs/formae/pkg/auth"
)

// The MCP drives a sign-in as two calls: one that returns the URL a user has to
// open, and one that waits for the flow to finish. Between them it holds the
// child process, because the pending login is in-process memory in the auth
// plugin and no second process can resume it.
//
// That needs a protocol, and prose is not one. These tests pin the documents.
// A consumer built against a sketch of a producer is what shipped the argv defect
// this effort has already paid for once.

// machineDocs decodes the documents a machine-mode run wrote, in order.
func machineDocs(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	var docs []map[string]any
	dec := json.NewDecoder(strings.NewReader(out.String()))
	for {
		var d map[string]any
		err := dec.Decode(&d)
		if err == io.EOF {
			break
		}
		require.NoError(t, err, "machine output is not a stream of JSON documents: %q", out.String())
		docs = append(docs, d)
	}
	return docs
}

// machineStep is a cloud sign-in whose output is machine documents.
func machineStep(t *testing.T, f *syncFixture) syncStep {
	t.Helper()
	step := cloudStep(t, f)
	step.Emit = machineEmitter(f.out, "json")
	// In machine mode stdout carries documents and nothing else, so the prose
	// sink is separate from the document sink.
	step.Out = io.Discard
	return step
}

func TestMachine_BrowserFlowEmitsStartedThenComplete(t *testing.T) {
	f := cleanStoreFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), machineStep(t, f), false))

	docs := machineDocs(t, f.out)
	require.Len(t, docs, 2, "expected exactly a started and a complete document")

	assert.Equal(t, float64(1), docs[0]["schemaVersion"])
	assert.Equal(t, "started", docs[0]["phase"])
	assert.Equal(t, "browser", docs[0]["method"])
	assert.Equal(t, "https://issuer.example/authorize?req=abc", docs[0]["browserUrl"])

	assert.Equal(t, float64(1), docs[1]["schemaVersion"])
	assert.Equal(t, "complete", docs[1]["phase"])
	assert.Equal(t, "jane", docs[1]["subjectName"])
	assert.Equal(t, cloudProfileName(), docs[1]["active"])

	profiles, ok := docs[1]["profiles"].(map[string]any)
	require.True(t, ok, "the completion document carries no profiles object")
	assert.Equal(t, []any{cloudProfileName()}, profiles["created"])
}

// The started document must be written before the flow is waited on. A consumer
// that only sees it after LoginWait returns has no URL to show the user while the
// sign-in is pending, which is the entire reason the protocol has two documents.
func TestMachine_StartedIsWrittenBeforeTheWait(t *testing.T) {
	f := cleanStoreFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	c := signedIn()
	var atWait string
	c.onLoginWait = func() { atWait = f.out.String() }

	require.NoError(t, runLoginAndSync(context.Background(), c, machineStep(t, f), false))

	assert.Contains(t, atWait, `"phase":"started"`,
		"the started document was not on the wire when the flow began waiting")
	assert.NotContains(t, atWait, `"phase":"complete"`)
}

func TestMachine_DeviceFlowCarriesTheCodeAndExpiry(t *testing.T) {
	f := cleanStoreFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	c := signedIn()
	c.loginStartResp = &pkgauth.LoginStartResponse{
		Status:           "started",
		Method:           "device",
		VerificationURI:  "https://issuer.example/device",
		UserCode:         "WDJB-MJHT",
		ExpiresInSeconds: 900,
		SessionID:        "sess-1",
	}

	require.NoError(t, runLoginAndSync(context.Background(), c, machineStep(t, f), true))

	docs := machineDocs(t, f.out)
	require.Len(t, docs, 2)
	assert.Equal(t, "device", docs[0]["method"])
	assert.Equal(t, "https://issuer.example/device", docs[0]["verificationUri"])
	assert.Equal(t, "WDJB-MJHT", docs[0]["userCode"])
	assert.Equal(t, float64(900), docs[0]["expiresInSeconds"])
}

// A session already open short-circuits: there is nothing for the user to do, so
// there is no started document at all. A consumer has to be able to tell that
// from a flow it must wait on — it is what makes driving setup twice harmless.
func TestMachine_AlreadyAuthenticatedEmitsOnlyCompletion(t *testing.T) {
	f := cleanStoreFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	c := signedIn()
	c.loginStartResp = &pkgauth.LoginStartResponse{
		Status:      "already_authenticated",
		Subject:     "sub-1",
		SubjectName: "jane",
	}

	require.NoError(t, runLoginAndSync(context.Background(), c, machineStep(t, f), false))

	docs := machineDocs(t, f.out)
	require.Len(t, docs, 1, "an open session needs no started document")
	assert.Equal(t, "complete", docs[0]["phase"])
	assert.Equal(t, "already_authenticated", docs[0]["status"])
	assert.False(t, c.loginWaitCalled, "an open session must not be waited on")
}

// A refusal after the URL was handed out is the ordinary failure, not an edge
// case: LoginWait is where a timeout, a rejected exchange, an invalid id_token
// and a failed session write all land. So the stream is a started document
// followed by a failure envelope, and a consumer that assumed otherwise would
// break on the most common way a sign-in fails.
func TestMachine_AFailureAfterTheURLKeepsTheStartedDocument(t *testing.T) {
	f := cleanStoreFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	c := signedIn()
	c.loginWaitResp = &pkgauth.LoginWaitResponse{
		ErrorCode: pkgauth.ErrorCodeSessionExpired,
	}

	err := runLoginAndSync(context.Background(), c, machineStep(t, f), false)
	require.Error(t, err)

	_, perr := reportLogin(f.out, "json", err)
	require.NoError(t, perr)

	docs := machineDocs(t, f.out)
	require.Len(t, docs, 2)
	assert.Equal(t, "started", docs[0]["phase"])
	assert.Equal(t, "auth_failed", docs[1]["code"])
}

// The plugin's own code is the only thing that can say why a sign-in was refused,
// and the MCP branches on it: not_logged_in and session_expired mean "sign in
// again", where issuer_unreachable and unsupported do not. runLogin used to
// collapse it into a formatted string, which would have made every refusal look
// the same to a consumer.
func TestMachine_AuthFailureCarriesThePluginCode(t *testing.T) {
	for _, code := range []pkgauth.ErrorCode{
		pkgauth.ErrorCodeUnsupported,
		pkgauth.ErrorCodeNotLoggedIn,
		pkgauth.ErrorCodeSessionExpired,
		pkgauth.ErrorCodeIssuerUnreachable,
	} {
		t.Run(string(code), func(t *testing.T) {
			f := cleanStoreFixture(t)
			c := signedIn()
			c.loginStartResp = &pkgauth.LoginStartResponse{ErrorCode: code}

			err := runLoginAndSync(context.Background(), c, machineStep(t, f), false)
			require.Error(t, err)

			out := &bytes.Buffer{}
			_, perr := reportLogin(out, "json", err)
			require.NoError(t, perr)

			var doc map[string]any
			require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
			assert.Equal(t, "auth_failed", doc["code"])
			details, ok := doc["details"].(map[string]any)
			require.True(t, ok, "no details on an auth_failed envelope")
			assert.Equal(t, string(code), details["pluginCode"])
		})
	}
}

// A plugin that is not installed is reported as its own code, so a consumer can
// name the remedy rather than reporting a generic failure.
func TestMachine_AMissingPluginIsItsOwnCode(t *testing.T) {
	out := &bytes.Buffer{}
	_, _, err := authPluginFor(oidcAuthType, json.RawMessage(`{"type":"oidc"}`), t.TempDir())
	require.Error(t, err)

	_, perr := reportLogin(out, "json", err)
	require.NoError(t, perr)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
	assert.Equal(t, "plugin_missing", doc["code"])
}

// Nothing but documents reaches the document stream. A banner or an ack line
// interleaved with JSON makes the whole stream unparseable.
func TestMachine_NoProseReachesTheDocumentStream(t *testing.T) {
	f := cleanStoreFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), machineStep(t, f), false))

	for _, line := range strings.Split(strings.TrimSpace(f.out.String()), "\n") {
		assert.True(t, strings.HasPrefix(line, "{"),
			"a non-document line reached the machine stream: %q", line)
	}
	assert.NotContains(t, f.out.String(), "Open this URL")
	assert.NotContains(t, f.out.String(), "✓")
}

// The credential never reaches a document. The MCP gets it from
// `connection resolve`, as a masked value; a sign-in reports identity and
// profiles, which is what the caller acts on.
func TestMachine_NoCredentialReachesADocument(t *testing.T) {
	f := cleanStoreFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), machineStep(t, f), false))
	assert.NotContains(t, f.out.String(), testToken)
}

// Human output is unchanged. The whole machine path is additive, and the prose a
// person reads at their own terminal is what it was.
func TestMachine_HumanOutputIsUnchanged(t *testing.T) {
	f := cleanStoreFixture(t)
	f.answer(installation(installOne, "prod", stateActive))

	step := cloudStep(t, f) // no Emit: the human path.
	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), step, false))

	out := f.out.String()
	assert.Contains(t, out, "Open this URL to sign in:")
	assert.Contains(t, out, "signed in as jane")
	assert.NotContains(t, out, `"schemaVersion"`)
}

// The warnings the sync produced are surfaced, because a sign-in that silently
// skipped a profile is exactly what a caller has to be told.
func TestMachine_SyncWarningsReachTheCompletionDocument(t *testing.T) {
	f := cleanStoreFixture(t)
	// A state this formae does not understand: the profile is left exactly as it
	// is and the run says so. (Two same-named installations do not collide — the
	// derived name carries a suffix from the installation id.)
	f.answer(
		installation(installOne, "prod", stateActive),
		installation(installTwo, "staging", "warping"),
	)

	require.NoError(t, runLoginAndSync(context.Background(), signedIn(), machineStep(t, f), false))

	docs := machineDocs(t, f.out)
	require.Len(t, docs, 2)
	warnings, ok := docs[1]["warnings"].([]any)
	require.True(t, ok, "no warnings on the completion document")
	assert.NotEmpty(t, warnings)
}

// Nothing this repository did not write reaches a failure envelope.
//
// Measured before this was enforced: a profile whose password line carried a bad
// escape put the password itself onto stdout, because Pkl quotes the source line
// it failed on and the envelope carried err.Error() verbatim. A consumer never
// reads that field — it branches on the code — so the text was pure exposure.
func TestMachine_AnUndeclaredErrorRevealsNothingItWasGiven(t *testing.T) {
	secret := "SYNTHETIC-SECRET-abc123"
	pklish := "failed to evaluate PKL configuration file: –– Pkl Error ––\n" +
		"Invalid character escape sequence `\\q`.\n\n" +
		"7 | [\"password\"] = \"" + secret + "\\q\"\n    ^^^^^^^^^^^^"

	out := &bytes.Buffer{}
	_, err := reportLogin(out, "json", errors.New(pklish))
	require.NoError(t, err)

	assert.NotContains(t, out.String(), secret,
		"an inline password reached the failure envelope")
	assert.NotContains(t, out.String(), "Pkl Error",
		"the producer's own error text reached the failure envelope")

	// And it is still a usable envelope: the code is what a consumer reads.
	var doc map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
	assert.Equal(t, "internal", doc["code"])
}

// An auth refusal carries the plugin's code and not its prose.
func TestMachine_AnAuthRefusalCarriesTheCodeNotThePluginsWords(t *testing.T) {
	out := &bytes.Buffer{}
	_, err := reportLogin(out, "json", &AuthError{
		Code:    "session_expired",
		Message: "PLUGIN-PROSE-do-not-forward",
	})
	require.NoError(t, err)

	assert.NotContains(t, out.String(), "PLUGIN-PROSE-do-not-forward")

	var doc map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
	assert.Equal(t, "auth_failed", doc["code"])
	details, ok := doc["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "session_expired", details["pluginCode"])
}
