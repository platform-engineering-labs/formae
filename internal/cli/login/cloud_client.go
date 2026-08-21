// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package login

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"time"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// The hosted control plane's installations endpoint is the only thing that
// says which installations a caller's grants cover, and the caller removes
// profiles for the installations it does not name. So this client's job is not
// just to fetch records: it is to say whether the answer it returns is
// complete enough to license a removal.
//
// Removal needs exactly one thing to be true — that for every profile this
// formae recorded against the origin, we can decide with certainty whether its
// installation still appears in the caller's grants. Two failure modes are
// deliberately kept apart, because collapsing them is a defect in either
// direction:
//
//   - The response as a whole is incomplete or untrustworthy — a body with no
//     results list, an unrecognised top-level key, a pagination header. Then no
//     installation's presence can be decided, so the run must subtract nothing:
//     Snapshot.Authoritative is false. Ignoring this is how a client prunes on
//     the strength of page 1 the day the endpoint starts paginating.
//
//   - A record cannot be matched against a recorded profile at all — no
//     canonical installation id, no usable endpoint, an id returned twice. Some
//     installation's presence is then genuinely unknown, so the run again may
//     not subtract. Only the record itself is dropped from the list.
//
// A record we can identify but which carries something we do not recognise —
// an unknown field, an unfamiliar state — is neither of those. It is present,
// so every installation's presence is still decidable, and the run keeps its
// authority. Collapsing that into the cases above would let one odd record
// silently switch pruning off for every user.
//
// A non-authoritative snapshot still carries its valid records. Creating and
// repairing a profile are non-destructive, so partial knowledge may add; only
// complete knowledge may subtract.

const (
	// The timeouts are the hub client's shape with a longer budget: this call
	// runs interactively right after a sign-in, against an endpoint that reads
	// a caller's grants rather than serving a static artifact.
	cloudDialTimeout           = 3 * time.Second
	cloudTLSHandshakeTimeout   = 3 * time.Second
	cloudResponseHeaderTimeout = 10 * time.Second
	cloudTotalTimeout          = 20 * time.Second
)

const (
	// maxInstallations is the most records one response may carry. It is far
	// above any plausible grant set, so reaching it means the endpoint is not
	// answering the way its contract says it does.
	maxInstallations = 500

	// maxRecordBytes is the per-record allowance the body cap is sized from: a
	// generous budget, well above the size of the fields this client reads. No
	// record is measured against it.
	maxRecordBytes = 2 << 10

	// maxResponseBytes bounds the body as a resource limit, so a body is
	// refused before anything parses it. Its size is the two above multiplied
	// out, but nothing holds a record to maxRecordBytes: the two caps are
	// independent bounds on the same response and either may be the one that
	// trips.
	maxResponseBytes = maxInstallations * maxRecordBytes

	// maxWarnedRunes bounds any value from the response that a warning repeats
	// back, so a broken or hostile control plane cannot choose how much text
	// lands in the user's terminal.
	maxWarnedRunes = 64
)

// errUnexpectedRedirect reports a redirect the client refused to follow. It is
// a sentinel so the refusal is not mistaken for a transport failure and
// classified as transient: a redirect is an answer, and a wrong one.
var errUnexpectedRedirect = errors.New("the control plane redirected the request")

// Installation is one installation the caller's grants cover, as the control
// plane describes it.
type Installation struct {
	InstallationID   string `json:"installationId"`
	InstallationName string `json:"installationName"`
	TenantName       string `json:"tenantName"`
	OrgName          string `json:"orgName"`
	Endpoint         string `json:"endpoint"`
	IssuerPath       string `json:"issuerPath"`
	State            string `json:"state"`
}

// Snapshot carries the installations and whether the response was complete
// and fully valid. Only a complete response licenses removing anything.
type Snapshot struct {
	Installations []Installation
	Authoritative bool
	Warnings      []string
}

// CloudClient reads the installations the caller's grants cover.
type CloudClient interface {
	ListInstallations(ctx context.Context, bearer string) (Snapshot, error)
}

// cloudAuthError reports that the control plane refused the credentials
// (HTTP 401/403). Signing in again is the fix, so the caller says so rather
// than retrying.
type cloudAuthError struct{ Cause error }

func (e *cloudAuthError) Error() string { return e.Cause.Error() }
func (e *cloudAuthError) Unwrap() error { return e.Cause }

// cloudTransientError reports a definitely-temporary inability to get an
// answer: timeouts and HTTP 408/429/5xx. Nothing is known about the caller's
// grants, so the caller warns and leaves every profile alone.
type cloudTransientError struct{ Cause error }

func (e *cloudTransientError) Error() string { return e.Cause.Error() }
func (e *cloudTransientError) Unwrap() error { return e.Cause }

// newCloudClient returns a client for the control plane at baseURL.
//
// The transport is built here rather than taken from the app: the network
// plugin's client exists to reach a private agent over a user's tsnet, and the
// control plane is on the public internet. Tunnelling this call through a
// user's network plugin would send a bearer somewhere it was never issued for.
func newCloudClient(baseURL string) CloudClient {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout: cloudDialTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   cloudTLSHandshakeTimeout,
		ResponseHeaderTimeout: cloudResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &httpCloudClient{
		baseURL: baseURL,
		http: &http.Client{
			Transport: transport,
			Timeout:   cloudTotalTimeout,
			// net/http carries headers set on the original request across a
			// redirect, so following one would hand the bearer to whatever
			// host the response named. An authenticated GET against our own
			// API has no legitimate reason to redirect, so any redirect is
			// refused rather than filtered.
			CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				return fmt.Errorf("%w to %q", errUnexpectedRedirect, clip(req.URL.Redacted(), maxWarnedRunes))
			},
		},
	}
}

type httpCloudClient struct {
	baseURL string
	http    *http.Client
}

func (c *httpCloudClient) ListInstallations(ctx context.Context, bearer string) (Snapshot, error) {
	u, err := url.JoinPath(c.baseURL, "api", "v1", "me", "installations")
	if err != nil {
		return Snapshot{}, fmt.Errorf("invalid control-plane URL %q: %w", c.baseURL, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("build installations request: %w", err)
	}
	// The bearer is the whole credential, and the only header this client
	// adds. It is never logged and never repeated back in an error.
	req.Header.Set("Authorization", bearer)

	resp, err := c.http.Do(req)
	if err != nil {
		// Caller-driven cancellation propagates unchanged so the wider command
		// flow can react to it. It is checked against the caller's context
		// rather than the error, because the client's own timeout also
		// surfaces as a deadline error.
		if ctx.Err() != nil {
			return Snapshot{}, ctx.Err()
		}
		if errors.Is(err, errUnexpectedRedirect) {
			return Snapshot{}, err
		}
		if isTimeout(err) {
			return Snapshot{}, &cloudTransientError{Cause: err}
		}
		return Snapshot{}, fmt.Errorf("list installations: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return Snapshot{}, &cloudAuthError{Cause: fmt.Errorf(
			"the control plane rejected this session (HTTP %d); run `formae login` to sign in again",
			resp.StatusCode)}
	case resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500:
		return Snapshot{}, &cloudTransientError{Cause: fmt.Errorf(
			"the control plane returned HTTP %d", resp.StatusCode)}
	default:
		return Snapshot{}, fmt.Errorf(
			"the control plane returned unexpected HTTP %d for the installations request", resp.StatusCode)
	}

	// The body is read one byte past the cap so a full body and a truncated
	// one can be told apart, and an oversized body is refused before anything
	// parses it. Reading exactly the cap cannot make that distinction, and a
	// truncated prefix can itself be valid JSON — which, for a client whose
	// caller removes profiles for absent installations, would read a fragment
	// of a list as the whole of it.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return Snapshot{}, &cloudTransientError{Cause: fmt.Errorf("read the installations response: %w", err)}
	}
	if len(data) > maxResponseBytes {
		return Snapshot{}, fmt.Errorf(
			"the installations response is larger than %d bytes, which is more than %d installations can account for",
			maxResponseBytes, maxInstallations)
	}

	return parseSnapshot(data, resp.Header)
}

// parseSnapshot turns a response body and its headers into a snapshot,
// deciding both what the response says and whether it may be believed
// completely.
func parseSnapshot(data []byte, header http.Header) (Snapshot, error) {
	envelope, repeated, err := decodeEnvelope(data)
	if err != nil {
		return Snapshot{}, err
	}

	snapshot := Snapshot{Authoritative: true}

	// A pagination marker in the headers says the body is a page, so the
	// records missing from it are missing from this page and not from the
	// caller's grants.
	for _, name := range []string{"Link", "Content-Range"} {
		if len(header.Values(name)) > 0 {
			snapshot.Authoritative = false
			snapshot.Warnings = append(snapshot.Warnings, fmt.Sprintf(
				"the installations response carries a %s header, which this formae does not understand; "+
					"it may be one page of several, so no profile will be removed by this run", name))
		}
	}

	for _, key := range repeated {
		snapshot.Authoritative = false
		snapshot.Warnings = append(snapshot.Warnings, fmt.Sprintf(
			"the installations response carries the %q field more than once, so this run has at most one of the "+
				"lists it was sent; no profile will be removed by it", clip(key, maxWarnedRunes)))
	}

	unknown := make([]string, 0, len(envelope))
	for key := range envelope {
		if key != "results" {
			unknown = append(unknown, key)
		}
	}
	slices.Sort(unknown)
	for _, key := range unknown {
		snapshot.Authoritative = false
		snapshot.Warnings = append(snapshot.Warnings, fmt.Sprintf(
			"the installations response carries an unrecognised %q field, which this formae does not understand; "+
				"it may say the list is incomplete, so no profile will be removed by this run", clip(key, maxWarnedRunes)))
	}

	raw, ok := envelope["results"]
	if !ok {
		snapshot.Authoritative = false
		snapshot.Warnings = append(snapshot.Warnings,
			"the installations response carries no results list, so this run cannot tell which installations "+
				"your grants cover; no profile will be removed by it")
		return snapshot, nil
	}

	// A missing list and a null one are not an empty list. Only an empty array
	// says "your grants cover nothing here" — the one answer that licenses
	// removing every profile for this origin — so anything else that is not an
	// array is refused that meaning. A nil slice after a successful decode is
	// a null, since encoding/json allocates an empty slice for [].
	var results []json.RawMessage
	if err := json.Unmarshal(raw, &results); err != nil || results == nil {
		snapshot.Authoritative = false
		snapshot.Warnings = append(snapshot.Warnings,
			"the installations response carries no list of installations where one was expected, so this run "+
				"cannot tell which installations your grants cover; no profile will be removed by it")
		return snapshot, nil
	}
	if len(results) > maxInstallations {
		return Snapshot{}, fmt.Errorf(
			"the installations response carries %d installations, more than the %d one response may return",
			len(results), maxInstallations)
	}

	// Each record is decoded on its own. Decoding the list in one piece would
	// let one element's type error abort the whole body, which would throw
	// away every valid record beside it — and those records are ones the
	// caller could still have written or repaired.
	installations := make([]Installation, 0, len(results))
	for i, rawRecord := range results {
		installation, err := decodeInstallation(rawRecord)
		if err != nil {
			snapshot.Authoritative = false
			snapshot.Warnings = append(snapshot.Warnings, fmt.Sprintf(
				"ignoring installation record %d of the installations response: %v; "+
					"this run cannot tell whether that installation is one of yours, so it removes no profile",
				i+1, err))
			continue
		}
		installations = append(installations, installation)
	}

	return withoutDuplicates(snapshot, installations), nil
}

// decodeEnvelope reads the top level of the body as its raw members, and
// reports every member name that appeared more than once.
//
// The members are read one token at a time rather than unmarshalled into a
// map because a map cannot say what it dropped: a body naming results twice
// decodes to whichever list came last, so every installation in the other one
// would read as absent from a body that still looked complete and wholly
// recognised. The keys are needed anyway — one we do not recognise may be the
// marker saying this is one page of several.
func decodeEnvelope(data []byte) (map[string]json.RawMessage, []string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	opening, err := dec.Token()
	if err != nil {
		return nil, nil, fmt.Errorf("the installations response is not valid JSON: %w", err)
	}
	if opening != json.Delim('{') {
		return nil, nil, fmt.Errorf("the installations response is not a JSON object")
	}

	members := map[string]json.RawMessage{}
	var repeated []string
	for dec.More() {
		// A token in key position is always a string, so the assertion holds
		// for every object encoding/json will parse at all.
		token, err := dec.Token()
		if err != nil {
			return nil, nil, fmt.Errorf("the installations response is not valid JSON: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return nil, nil, fmt.Errorf("the installations response is not a JSON object")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, nil, fmt.Errorf("the installations response is not valid JSON: %w", err)
		}
		if _, seen := members[key]; seen && !slices.Contains(repeated, key) {
			repeated = append(repeated, key)
		}
		members[key] = value
	}
	if _, err := dec.Token(); err != nil { // the closing brace.
		return nil, nil, fmt.Errorf("the installations response is not valid JSON: %w", err)
	}
	// Anything after the object means the body is not the one answer it claims
	// to be, and there is no telling which part of it to believe. The end of
	// the body is required outright, rather than asked whether another value
	// follows: a decoder answers that question by peeking for a byte that could
	// open one, so a stray `}` or `]` reads as the end of the stream and would
	// wave through everything behind it. Reading a token instead reports the
	// end of the body as io.EOF and every one of those bytes as a parse error,
	// while still allowing the trailing whitespace or newline a server may
	// legitimately send.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, nil, fmt.Errorf("the installations response carries more than one JSON value")
	}

	return members, repeated, nil
}

// decodeInstallation decodes and validates one record. It reports only what
// went wrong, never the value that was wrong: every string in a response is
// chosen by the far end, and one of them is an endpoint that may carry
// credentials.
//
// A record's state is deliberately not validated. Deciding what a state means
// belongs to the step that acts on it, and a state this formae has never heard
// of says nothing about whether the list of installations is complete.
func decodeInstallation(raw json.RawMessage) (Installation, error) {
	// Unknown fields are ignored: a new per-installation field is the endpoint
	// growing, not the response admitting it is partial.
	var installation Installation
	if err := json.Unmarshal(raw, &installation); err != nil {
		// The decode error is quoted as well as clipped. encoding/json repeats
		// the input literal back for a numeric target or a map key, so quoting
		// keeps text the far end chose from rewriting the line around it the
		// day a field on this struct stops being a string.
		return Installation{}, fmt.Errorf("it could not be read (%q)", clip(err.Error(), maxWarnedRunes))
	}
	if !pkgmodel.ValidInstallationID(installation.InstallationID) {
		// Without a canonical id the record cannot be matched against a
		// recorded profile at all, so it identifies no installation.
		return Installation{}, errors.New("its installationId is not a well-formed installation id")
	}
	endpoint, err := canonicalOrigin(installation.Endpoint)
	if err != nil {
		// The rule is the one every origin in this command is held to, so a
		// record naming a loopback http endpoint is accepted like any other.
		return Installation{}, errors.New(
			"its endpoint is not a bare https origin, or a bare http one on a loopback host")
	}

	// The endpoint is stored canonically so that one origin has one
	// representation: two records spelling the same endpoint differently
	// describe one place to reach an installation, not two.
	installation.Endpoint = endpoint
	return installation, nil
}

// withoutDuplicates drops every copy of an installation id that appears more
// than once, and takes the run's authority with it.
//
// Both halves matter. An endpoint that returns one id twice has broken the key
// its own answer is organised by, which is no ground to trust the set it
// returned. And keeping one copy would mean writing a profile from a record
// chosen by nothing but its position in the response, when the copies may
// disagree about the very name the profile is derived from.
func withoutDuplicates(snapshot Snapshot, installations []Installation) Snapshot {
	seen := make(map[string]int, len(installations))
	for _, installation := range installations {
		seen[installation.InstallationID]++
	}

	kept := make([]Installation, 0, len(installations))
	reported := make(map[string]bool, len(installations))
	for _, installation := range installations {
		if seen[installation.InstallationID] < 2 {
			kept = append(kept, installation)
			continue
		}
		snapshot.Authoritative = false
		if !reported[installation.InstallationID] {
			reported[installation.InstallationID] = true
			snapshot.Warnings = append(snapshot.Warnings, fmt.Sprintf(
				"the installations response returns installation %s more than once; "+
					"this run cannot tell which record describes it, so it removes no profile",
				installation.InstallationID))
		}
	}

	snapshot.Installations = kept
	return snapshot
}

// isTimeout reports whether err is a timeout: a dial, handshake, or
// response-header timeout from the transport, or the client's own total
// deadline.
func isTimeout(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded)
}

// clip bounds a value taken from a response before a warning repeats it back,
// so the far end cannot choose how much text lands in a user's terminal. Call
// sites quote the result with %q where it is a value rather than prose, so it
// can neither hide itself nor rewrite the line around it.
func clip(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}
