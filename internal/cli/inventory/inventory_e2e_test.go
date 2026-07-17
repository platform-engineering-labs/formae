// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package inventory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/api"
	"github.com/platform-engineering-labs/formae/internal/api/apitest"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/inventoryview"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// waitForAll waits until ALL the given strings appear in the accumulated TUI
// output. The renderer skips frames that are identical to the previous render,
// so each call to waitForAll must be preceded by a model-changing event (key
// press, tab switch, etc.) if the view has not changed since the last read.
func waitForAll(t *testing.T, tm *teatest.TestModel, wants ...string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		for _, w := range wants {
			if !bytes.Contains(bts, []byte(w)) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(5*time.Second))
}

// newE2EApp builds an *app.App configured to talk to the given baseURL.
// Port 80 + http scheme causes formatEndpoint to pass the URL through unchanged.
func newE2EApp(baseURL string) *app.App {
	return &app.App{
		Config: &pkgmodel.Config{
			Cli: pkgmodel.CliConfig{
				API: pkgmodel.APIConfig{
					URL:  baseURL,
					Port: 80,
				},
				DisableUsageReporting: true,
			},
		},
	}
}

// seedFake builds a FakeMetastructure seeded with all four entity types for
// the full-browse test:
//   - Resources: one Forma-managed, one unmanaged-stack resource
//   - Targets: two targets
//   - Stacks: two stacks; "stack-with-ttl" carries an inline TTL policy
//   - Policies: two standalone policies with attached stacks
func seedFake(t *testing.T) *apitest.FakeMetastructure {
	t.Helper()
	ttlPolicy, err := json.Marshal(map[string]any{
		"Type":       "ttl",
		"Label":      "my-ttl",
		"TTLSeconds": float64(7200), // 2 h
	})
	require.NoError(t, err)
	autoPolicy, err := json.Marshal(map[string]any{
		"Type":            "auto-reconcile",
		"Label":          "my-recon",
		"IntervalSeconds": float64(3600), // 1 h
	})
	require.NoError(t, err)

	return &apitest.FakeMetastructure{
		ExtractResponses: []apitest.WrappedExtractResponse{
			{Forma: &pkgmodel.Forma{
				Resources: []pkgmodel.Resource{
					{
						Label:    "e2e-managed-bucket",
						Type:     "AWS::S3::Bucket",
						Stack:    "prod",
						Target:   "aws-us-east-1",
						NativeID: "arn:aws:s3:::e2e-managed-bucket",
						Managed:  true,
					},
					{
						Label:    "e2e-unmanaged-queue",
						Type:     "AWS::SQS::Queue",
						Stack:    "unmanaged",
						Target:   "aws-us-east-1",
						NativeID: "https://sqs.us-east-1.amazonaws.com/123/e2e-unmanaged-queue",
						Managed:  false,
					},
				},
			}},
		},
		TargetResponses: []apitest.WrappedTargetResponse{
			{Targets: []*pkgmodel.Target{
				{Label: "aws-us-east-1", Namespace: "AWS", Discoverable: true},
				{Label: "aws-eu-west-1", Namespace: "AWS", Discoverable: false},
			}},
		},
		StackResponses: []apitest.WrappedStackResponse{
			{Stacks: []*pkgmodel.Stack{
				{
					Label:       "prod",
					Description: "Production stack",
					CreatedAt:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
					Policies:    []json.RawMessage{ttlPolicy},
				},
				{
					Label:       "staging",
					Description: "Staging stack",
					CreatedAt:   time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
				},
			}},
		},
		PolicyResponses: []apitest.WrappedPolicyResponse{
			{Policies: []apimodel.PolicyInventoryItem{
				{
					Label:          "global-ttl",
					Type:           "ttl",
					Config:         ttlPolicy,
					AttachedStacks: []string{"prod", "staging"},
				},
				{
					Label:          "global-recon",
					Type:           "auto-reconcile",
					Config:         autoPolicy,
					AttachedStacks: []string{"staging"},
				},
			}},
		},
	}
}

// TestInventoryE2E_FourTabBrowse is the core layer-3 E2E: seeds all four entity
// types, drives the TUI through each tab via key 1-4, and makes targeted
// WaitForContains assertions. Timing-dependent strings are intentionally
// excluded from assertions.
func TestInventoryE2E_FourTabBrowse(t *testing.T) {
	fake := seedFake(t)

	srv := api.NewServer(t.Context(), fake, nil, nil, nil, nil)
	baseURL := apitest.NewTestServer(t, srv.Handler())
	realApp := newE2EApp(baseURL)

	m := inventoryview.New(theme.New("formae"), realApp, inventoryview.Options{
		FocusTab: inventoryview.TabResources,
		MaxRows:  200,
		Now:      time.Now,
	})

	tm := tuitest.Run(t, m, 140, 40)

	// Tab 1 (Resources) — initial load after server round-trip.
	// waitForAll checks multiple strings in a single pass (renderer deduplicates
	// identical frames, so we can't call WaitForContains twice on the same view).
	waitForAll(t, tm, "e2e-managed-bucket", "AWS::S3::Bucket")

	// Tab 2 (Targets) — key event triggers a new render.
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	waitForAll(t, tm, "aws-us-east-1", "aws-eu-west-1")

	// Tab 3 (Stacks) — policy summary shows type, not relative time.
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	waitForAll(t, tm, "prod", "staging", "TTL:")

	// Tab 4 (Policies).
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	waitForAll(t, tm, "global-ttl", "ttl", "global-recon")

	// Enter on policies tab → detail screen shows config + attached stacks.
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitForAll(t, tm, "Attached stacks:", "prod")

	// Esc back to list.
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})

	// Filter: '/' opens the filter bar (shows "/: " prompt); type narrows results.
	// The status line "Showing N of M policies (filtered)" confirms the filter works.
	// We use a single waitForAll call after all key presses so we get the final
	// rendered state including both the filter prompt and the filtered count.
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t', 't', 'l'}})
	waitForAll(t, tm, "/: ", "filtered")

	// Exit filter mode, then exercise quit key binding (q).
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// TestInventoryE2E_ErrorTabIsolation verifies that an error on the Targets tab
// does not pollute the Resources tab: Resources still renders fine after
// navigating to the failing tab and back.
func TestInventoryE2E_ErrorTabIsolation(t *testing.T) {
	fake := &apitest.FakeMetastructure{
		// Resources: healthy.
		ExtractResponses: []apitest.WrappedExtractResponse{
			{Forma: &pkgmodel.Forma{
				Resources: []pkgmodel.Resource{
					{
						Label:    "healthy-bucket",
						Type:     "AWS::S3::Bucket",
						Stack:    "default",
						Target:   "aws-us-east-1",
						NativeID: "arn:aws:s3:::healthy-bucket",
						Managed:  true,
					},
				},
			}},
		},
		// Targets: error.
		TargetResponses: []apitest.WrappedTargetResponse{
			{Error: fmt.Errorf("targets fetch failed: connection reset")},
		},
	}

	srv := api.NewServer(t.Context(), fake, nil, nil, nil, nil)
	baseURL := apitest.NewTestServer(t, srv.Handler())
	realApp := newE2EApp(baseURL)

	m := inventoryview.New(theme.New("formae"), realApp, inventoryview.Options{
		FocusTab: inventoryview.TabResources,
		MaxRows:  200,
		Now:      time.Now,
	})

	tm := tuitest.Run(t, m, 140, 40)

	// Resources tab loads fine.
	tuitest.WaitForContains(t, tm, "healthy-bucket")

	// Switch to Targets tab → error banner text visible.
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	tuitest.WaitForContains(t, tm, "targets fetch failed")

	// Back to Resources → still shows healthy data.
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	tuitest.WaitForContains(t, tm, "healthy-bucket")

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// TestInventoryE2E_QueryThreading verifies that a non-empty query string flows
// through the full stack to the fake's ExtractResources. The fake records every
// query via RecordedExtractQueries.
func TestInventoryE2E_QueryThreading(t *testing.T) {
	fake := &apitest.FakeMetastructure{
		ExtractResponses: []apitest.WrappedExtractResponse{
			{Forma: &pkgmodel.Forma{
				Resources: []pkgmodel.Resource{
					{
						Label:    "prod-bucket",
						Type:     "AWS::S3::Bucket",
						Stack:    "prod",
						Target:   "aws-us-east-1",
						NativeID: "arn:aws:s3:::prod-bucket",
						Managed:  true,
					},
				},
			}},
		},
	}

	srv := api.NewServer(t.Context(), fake, nil, nil, nil, nil)
	baseURL := apitest.NewTestServer(t, srv.Handler())
	realApp := newE2EApp(baseURL)

	m := inventoryview.New(theme.New("formae"), realApp, inventoryview.Options{
		FocusTab: inventoryview.TabResources,
		Query:    "stack:prod",
		MaxRows:  200,
		Now:      time.Now,
	})

	tm := tuitest.Run(t, m, 140, 40)

	// Seeded resource appears, confirming server dispatched the response.
	tuitest.WaitForContains(t, tm, "prod-bucket")

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))

	// The query must have been received by the fake.
	assert.Contains(t, fake.RecordedExtractQueries, "stack:prod",
		"query 'stack:prod' must reach ExtractResources on the server")
}

// captureStdout replaces os.Stdout with a pipe, calls fn, restores os.Stdout,
// and returns the captured bytes.
func captureStdout(t *testing.T, fn func()) []byte {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)

	origStdout := os.Stdout
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
	}()

	fn()

	w.Close()
	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	require.NoError(t, err)
	r.Close()

	return buf.Bytes()
}

// TestInventoryE2E_NonTTYPrint verifies the non-TTY human-readable path:
//
//	(a) default MaxResults (10) → table contains the seeded labels and the
//	    "Showing X of Y total resources" summary line.
//	(b) MaxResults=1 + MaxResultsSet=true → table capped at 1 row.
func TestInventoryE2E_NonTTYPrint(t *testing.T) {
	// Seed three resources so we can verify capping.
	resources := []pkgmodel.Resource{
		{Label: "bucket-alpha", Type: "AWS::S3::Bucket", Stack: "prod", Target: "t1", NativeID: "arn:s3:::alpha", Managed: true},
		{Label: "bucket-beta", Type: "AWS::S3::Bucket", Stack: "prod", Target: "t1", NativeID: "arn:s3:::beta", Managed: true},
		{Label: "bucket-gamma", Type: "AWS::S3::Bucket", Stack: "prod", Target: "t1", NativeID: "arn:s3:::gamma", Managed: true},
	}

	fake := &apitest.FakeMetastructure{
		ExtractResponses: []apitest.WrappedExtractResponse{
			{Forma: &pkgmodel.Forma{Resources: resources}},
			{Forma: &pkgmodel.Forma{Resources: resources}},
		},
	}

	srv := api.NewServer(t.Context(), fake, nil, nil, nil, nil)
	baseURL := apitest.NewTestServer(t, srv.Handler())

	// Stub isTerminal → false for both sub-tests.
	origIsTerminal := isTerminal
	isTerminal = func(_ io.Writer) bool { return false }
	t.Cleanup(func() { isTerminal = origIsTerminal })

	t.Run("default max-results shows all seeded labels", func(t *testing.T) {
		realApp := newE2EApp(baseURL)
		opts := &InventoryOptions{
			OutputConsumer: printer.ConsumerHuman,
			MaxResults:     10,
			MaxResultsSet:  false,
		}
		output := captureStdout(t, func() {
			err := runResourcesForHumans(realApp, opts)
			require.NoError(t, err)
		})
		got := string(output)
		assert.Contains(t, got, "bucket-alpha")
		assert.Contains(t, got, "bucket-beta")
		assert.Contains(t, got, "bucket-gamma")
		assert.Contains(t, got, "Showing 3 of 3 total resources")
	})

	t.Run("MaxResults=1 caps table to 1 row", func(t *testing.T) {
		realApp := newE2EApp(baseURL)
		opts := &InventoryOptions{
			OutputConsumer: printer.ConsumerHuman,
			MaxResults:     1,
			MaxResultsSet:  true,
		}
		output := captureStdout(t, func() {
			err := runResourcesForHumans(realApp, opts)
			require.NoError(t, err)
		})
		got := string(output)
		assert.Contains(t, got, "Showing 1 of 3 total resources")
	})
}

// TestInventoryE2E_MachinePath verifies that the machine-readable JSON path is
// untouched by TUI logic: machine output parses as JSON with the seeded resources.
func TestInventoryE2E_MachinePath(t *testing.T) {
	fake := &apitest.FakeMetastructure{
		ExtractResponses: []apitest.WrappedExtractResponse{
			{Forma: &pkgmodel.Forma{
				Resources: []pkgmodel.Resource{
					{Label: "json-bucket", Type: "AWS::S3::Bucket", Stack: "prod", Target: "t1", NativeID: "arn:s3:::json-bucket", Managed: true},
				},
			}},
		},
	}

	srv := api.NewServer(t.Context(), fake, nil, nil, nil, nil)
	baseURL := apitest.NewTestServer(t, srv.Handler())

	realApp := newE2EApp(baseURL)
	opts := &InventoryOptions{
		OutputConsumer: printer.ConsumerMachine,
		OutputSchema:   "json",
		MaxResults:     10,
	}

	output := captureStdout(t, func() {
		err := runResources(realApp, opts)
		require.NoError(t, err)
	})

	// Output must be valid JSON containing the seeded resource label.
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(output, &parsed), "machine output must be valid JSON")
	raw, _ := json.Marshal(parsed)
	assert.Contains(t, string(raw), "json-bucket",
		"JSON output must include seeded label 'json-bucket'")
}
