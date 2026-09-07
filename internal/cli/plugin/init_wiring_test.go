// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type spyDownloader struct {
	calls     int
	gotDir    string
	returnErr error
}

func (s *spyDownloader) Download(ctx context.Context, outputDir string) error {
	s.calls++
	s.gotDir = outputDir
	return s.returnErr
}

type recordingServer struct {
	*httptest.Server
	requestCount int32
	lastPath     string
}

func newRecordingServer(t *testing.T, status int, body string) *recordingServer {
	rs := &recordingServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&rs.requestCount, 1)
		rs.lastPath = r.URL.Path
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(rs.Close)
	return rs
}

func newWiringOpts(t *testing.T, hubURL string, downloader TemplateDownloader, overrides func(*PluginInitOptions)) *PluginInitOptions {
	t.Helper()
	opts := &PluginInitOptions{
		Name:               "foo",
		Namespace:          "FOO",
		Description:        "test",
		Category:           "other",
		Author:             "Tester",
		ModulePath:         "github.com/test/formae-plugin-foo",
		License:            "Apache-2.0",
		OutputDir:          filepath.Join(t.TempDir(), "scaffold"),
		NoInput:            true,
		Hub:                hubURL,
		TemplateDownloader: downloader,
	}
	if overrides != nil {
		overrides(opts)
	}
	return opts
}

func TestWiring_Conflict_AbortsBeforeScaffold(t *testing.T) {
	srv := newRecordingServer(t, http.StatusOK,
		`{"name":"foo","github_repo_url":"https://github.com/x/y"}`)
	dl := &spyDownloader{}
	opts := newWiringOpts(t, srv.URL, dl, nil)

	err := runPluginInit(context.Background(), opts)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "foo")
	assert.Contains(t, err.Error(), "https://github.com/x/y")
	assert.Equal(t, int32(1), atomic.LoadInt32(&srv.requestCount), "hub must have been called exactly once")
	assert.Equal(t, "/api/v1/plugins/foo", srv.lastPath)
	assert.Equal(t, 0, dl.calls, "downloader must NOT be called on conflict")
}

func TestWiring_Available_ProceedsToScaffold(t *testing.T) {
	srv := newRecordingServer(t, http.StatusNotFound,
		`{"error":{"code":"plugin_not_found"}}`)
	dl := &spyDownloader{}
	opts := newWiringOpts(t, srv.URL, dl, nil)

	_ = runPluginInit(context.Background(), opts)
	// Don't assert on the return value: with the spy returning nil,
	// later steps (transformTemplateFiles, finalizeLicense) operate on
	// an empty dir and may fail. We assert on the contract this test
	// defends.

	assert.Equal(t, int32(1), atomic.LoadInt32(&srv.requestCount), "hub must have been called")
	assert.Equal(t, 1, dl.calls, "downloader must be called exactly once on 404")
	assert.Equal(t, opts.OutputDir, dl.gotDir)
}

func TestWiring_TransientHubError_ProceedsToScaffold(t *testing.T) {
	srv := newRecordingServer(t, http.StatusInternalServerError, "")
	dl := &spyDownloader{}
	opts := newWiringOpts(t, srv.URL, dl, nil)

	_ = runPluginInit(context.Background(), opts)

	assert.Equal(t, int32(1), atomic.LoadInt32(&srv.requestCount))
	assert.Equal(t, 1, dl.calls, "downloader must be called even when hub returns 5xx")
}

func TestWiring_ProtocolMismatch_HardFailsBeforeScaffold(t *testing.T) {
	srv := newRecordingServer(t, http.StatusUnauthorized, "")
	dl := &spyDownloader{}
	opts := newWiringOpts(t, srv.URL, dl, nil)

	err := runPluginInit(context.Background(), opts)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.Equal(t, 0, dl.calls, "downloader must NOT be called on protocol mismatch")
}

func TestWiring_NoAvailabilityCheck_ShortCircuitsBadEnv(t *testing.T) {
	t.Setenv("FORMAE_HUB_URL", "not a url://")
	dl := &spyDownloader{}
	opts := newWiringOpts(t, "", dl, func(o *PluginInitOptions) {
		o.NoAvailabilityCheck = true
		o.HubClient = nil // would normally trigger NewHubClient(resolveHubURL(...))
	})

	_ = runPluginInit(context.Background(), opts)

	assert.Equal(t, 1, dl.calls,
		"downloader must be called — feature was opted out, the bad FORMAE_HUB_URL must not block")
}

func TestWiring_NoAvailabilityCheck_DoesNotCallHub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("hub must not receive any request, got %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	dl := &spyDownloader{}
	opts := newWiringOpts(t, srv.URL, dl, func(o *PluginInitOptions) {
		o.NoAvailabilityCheck = true
	})

	_ = runPluginInit(context.Background(), opts)

	assert.Equal(t, 1, dl.calls)
}

// TestWiring_ExplicitHub_DNSFailure_HardFails asserts that when the user
// explicitly passes --hub with a non-existent hostname, a DNS failure
// hard-fails the command (downloader NOT called).
func TestWiring_ExplicitHub_DNSFailure_HardFails(t *testing.T) {
	dl := &spyDownloader{}
	// Use an explicit (non-default) hub URL pointing at an unresolvable host.
	opts := newWiringOpts(t, "http://nonexistent.invalid:1", dl, nil)

	err := runPluginInit(context.Background(), opts)

	require.Error(t, err, "explicit hub with DNS failure must hard-fail")
	assert.Equal(t, 0, dl.calls, "downloader must NOT be called when explicit hub is unreachable")
}

// TestWiring_DefaultHub_DNSFailure_WarnsAndContinues asserts that when the
// default hub URL is used and it's unreachable (simulated via injected client),
// the command warns and continues scaffolding (downloader IS called).
func TestWiring_DefaultHub_DNSFailure_WarnsAndContinues(t *testing.T) {
	dl := &spyDownloader{}
	// Inject a fake HubClient that returns HubUnreachableError directly,
	// simulating what happens when the default hub is unreachable.
	opts := newWiringOpts(t, "", dl, func(o *PluginInitOptions) {
		o.Hub = "" // no explicit hub — will fall through to DefaultHubURL
		o.HubClient = &fakeUnreachableHubClient{}
	})

	_ = runPluginInit(context.Background(), opts)

	assert.Equal(t, 1, dl.calls, "downloader must be called when default hub is unreachable (warn-and-continue)")
}

// fakeUnreachableHubClient always returns HubUnreachableError.
type fakeUnreachableHubClient struct{}

func (f *fakeUnreachableHubClient) CheckPluginAvailability(_ context.Context, _ string) (AvailabilityResult, error) {
	return AvailabilityResult{}, &HubUnreachableError{Cause: fmt.Errorf("simulated: connection refused")}
}
