// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package status

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/platform-engineering-labs/formae/internal/api"
	"github.com/platform-engineering-labs/formae/internal/api/apitest"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/statuswatch"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// clientAdapter implements statuswatch.Client on top of the real *api.Client.
type clientAdapter struct{ c *api.Client }

func (a clientAdapter) GetCommandsStatus(query string, n int, _ bool) (*apimodel.ListCommandStatusResponse, []string, error) {
	resp, err := a.c.GetFormaCommandsStatus(query, "e2e-client", n)
	return resp, nil, err
}

func TestStatusWatchTUI_EndToEnd(t *testing.T) {
	// Seed the fake with one InProgress command that has two resources:
	// one Success ("web-1") and one InProgress ("my-bucket").
	fake := &apitest.FakeMetastructure{
		ListResponses: []apitest.WrappedListResponse{
			{
				ListCommandStatusResponse: &apimodel.ListCommandStatusResponse{
					Commands: []apimodel.Command{
						{
							CommandID: "cmd-e2e-1",
							Command:   "apply",
							Mode:      "reconcile",
							State:     "InProgress",
							StartTs:   time.Now().Add(-10 * time.Second),
							ResourceUpdates: []apimodel.ResourceUpdate{
								{
									ResourceLabel: "web-1",
									ResourceType:  "AWS::EC2::Instance",
									Operation:     "create",
									State:         "Success",
								},
								{
									ResourceLabel: "my-bucket",
									ResourceType:  "AWS::S3::Bucket",
									Operation:     "create",
									State:         "InProgress",
									StatusMessage: "Creating resource...",
								},
							},
						},
					},
				},
			},
			// Provide extra responses for subsequent poll ticks.
			{
				ListCommandStatusResponse: &apimodel.ListCommandStatusResponse{
					Commands: []apimodel.Command{
						{
							CommandID: "cmd-e2e-1",
							Command:   "apply",
							Mode:      "reconcile",
							State:     "InProgress",
							StartTs:   time.Now().Add(-10 * time.Second),
							ResourceUpdates: []apimodel.ResourceUpdate{
								{
									ResourceLabel: "web-1",
									ResourceType:  "AWS::EC2::Instance",
									Operation:     "create",
									State:         "Success",
								},
								{
									ResourceLabel: "my-bucket",
									ResourceType:  "AWS::S3::Bucket",
									Operation:     "create",
									State:         "InProgress",
									StatusMessage: "Creating resource...",
								},
							},
						},
					},
				},
			},
			{
				ListCommandStatusResponse: &apimodel.ListCommandStatusResponse{
					Commands: []apimodel.Command{
						{
							CommandID: "cmd-e2e-1",
							Command:   "apply",
							Mode:      "reconcile",
							State:     "InProgress",
							StartTs:   time.Now().Add(-10 * time.Second),
							ResourceUpdates: []apimodel.ResourceUpdate{
								{
									ResourceLabel: "web-1",
									ResourceType:  "AWS::EC2::Instance",
									Operation:     "create",
									State:         "Success",
								},
								{
									ResourceLabel: "my-bucket",
									ResourceType:  "AWS::S3::Bucket",
									Operation:     "create",
									State:         "InProgress",
									StatusMessage: "Creating resource...",
								},
							},
						},
					},
				},
			},
		},
	}

	// Build the real API server with the fake metastructure.
	srv := api.NewServer(t.Context(), fake, nil, nil, nil, nil)
	// NewTestServer wraps it with httptest and returns the base URL.
	// Pass Port=80 so formatEndpoint returns the URL unchanged (no extra :port).
	baseURL := apitest.NewTestServer(t, srv.Handler())

	// Construct the real API client pointing at the test server.
	client := api.NewClient(pkgmodel.APIConfig{URL: baseURL, Port: 80}, nil, nil)

	m := statuswatch.New(theme.New("formae"), clientAdapter{client}, statuswatch.Options{
		MaxResults:   10,
		PollInterval: 100 * time.Millisecond,
	})

	tm := tuitest.Run(t, m, 100, 30)

	// The list view should render the command ID from the real HTTP round trip.
	tuitest.WaitForContains(t, tm, "cmd-e2e-1")

	// Drill into the detail view.
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	tuitest.WaitForContains(t, tm, "my-bucket")
	tuitest.WaitForContains(t, tm, "AWS::S3::Bucket")

	// Navigate back to list view then quit.
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}
