// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package status

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ansiRe strips well-formed ANSI CSI sequences.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// fixedNow is a deterministic timestamp for golden tests.
var fixedNow = time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

// makeStatusFixture builds a representative ListCommandStatusResponse with two
// commands in mixed states (one successful, one failed) for golden tests.
func makeStatusFixture() *apimodel.ListCommandStatusResponse {
	start := time.Date(2025, 6, 15, 11, 55, 0, 0, time.UTC)
	end := time.Date(2025, 6, 15, 11, 57, 30, 0, time.UTC)
	startFailed := time.Date(2025, 6, 15, 11, 58, 0, 0, time.UTC)
	endFailed := time.Date(2025, 6, 15, 11, 59, 45, 0, time.UTC)

	return &apimodel.ListCommandStatusResponse{
		Commands: []apimodel.Command{
			{
				CommandID: "cmd-abc123",
				Command:   "apply",
				Mode:      "reconcile",
				State:     "Success",
				StartTs:   start,
				EndTs:     end,
				TargetUpdates: []apimodel.TargetUpdate{
					{TargetLabel: "aws-prod", Operation: "update", State: "Success", Duration: 5000},
				},
				ResourceUpdates: []apimodel.ResourceUpdate{
					{ResourceID: "res-001", ResourceType: "AWS::S3::Bucket", ResourceLabel: "my-bucket", StackName: "prod", Operation: "create", State: "Success", Duration: 15000},
					{ResourceID: "res-002", ResourceType: "AWS::EC2::Instance", ResourceLabel: "web-server", StackName: "prod", Operation: "update", State: "Success", Duration: 45000},
				},
			},
			{
				CommandID: "cmd-def456",
				Command:   "apply",
				Mode:      "patch",
				State:     "Failed",
				StartTs:   startFailed,
				EndTs:     endFailed,
				ResourceUpdates: []apimodel.ResourceUpdate{
					{ResourceID: "res-003", ResourceType: "AWS::RDS::DBInstance", ResourceLabel: "db-primary", StackName: "prod", Operation: "update", State: "Failed", Duration: 105000, ErrorMessage: "timeout waiting for instance"},
					{ResourceID: "res-004", ResourceType: "AWS::Lambda::Function", ResourceLabel: "handler", StackName: "prod", Operation: "update", State: "Canceled", Duration: 0},
				},
			},
		},
	}
}

// ── Machine golden pin (regression: machine path must stay byte-identical) ──

// TestMachineGolden_ListCommandStatusResponse pins the exact bytes produced by
// MachineReadablePrinter over a fixed fixture. This golden MUST NOT change when
// the human-path rendering is modified.
func TestMachineGolden_ListCommandStatusResponse(t *testing.T) {
	fixture := makeStatusFixture()
	var buf bytes.Buffer
	p := printer.NewMachineReadablePrinter[apimodel.ListCommandStatusResponse](&buf, "json")
	err := p.Print(fixture)
	require.NoError(t, err)
	tuitest.RequireGolden(t, buf.Bytes())
}

// ── renderStatusList golden tests ──────────────────────────────────────────

// TestRenderStatusList_Summary_Styled pins the styled (TrueColor profile) output
// of the summary layout.
func TestRenderStatusList_Summary_Styled(t *testing.T) {
	th := theme.New("formae")
	resp := makeStatusFixture()
	out := renderStatusListAt(th, resp, false /*detailed*/, 120, fixedNow)
	tuitest.RequireGolden(t, []byte(out))
}

// TestRenderStatusList_Summary_Piped asserts that the summary output, once
// ANSI escape sequences are stripped, contains the expected plain-text content.
// Piped output auto-strips ANSI at runtime; in tests we pin TrueColor so we
// strip manually and assert the structural content.
func TestRenderStatusList_Summary_Piped(t *testing.T) {
	th := theme.New("formae")
	resp := makeStatusFixture()
	out := renderStatusListAt(th, resp, false /*detailed*/, 100, fixedNow)
	plain := stripANSI(out)

	// Content checks on the plain text
	assert.Contains(t, plain, "cmd-abc123", "must contain first command ID")
	assert.Contains(t, plain, "cmd-def456", "must contain second command ID")
	assert.Contains(t, plain, "apply", "must contain command type")
	assert.Contains(t, plain, "reconcile", "must contain mode")
}

// TestRenderStatusList_Detailed_Styled pins the styled output of the detailed layout.
func TestRenderStatusList_Detailed_Styled(t *testing.T) {
	th := theme.New("formae")
	resp := makeStatusFixture()
	out := renderStatusListAt(th, resp, true /*detailed*/, 120, fixedNow)
	tuitest.RequireGolden(t, []byte(out))
}

// TestRenderStatusList_Detailed_Piped asserts the detailed layout plain-text
// (after stripping ANSI) contains per-resource rows. Piped output auto-strips
// ANSI at runtime; in tests we pin TrueColor so we strip manually.
func TestRenderStatusList_Detailed_Piped(t *testing.T) {
	th := theme.New("formae")
	resp := makeStatusFixture()
	out := renderStatusListAt(th, resp, true /*detailed*/, 100, fixedNow)
	plain := stripANSI(out)

	// Resource labels must appear in the detail output
	assert.Contains(t, plain, "my-bucket", "must contain resource label")
	assert.Contains(t, plain, "db-primary", "must contain failed resource label")

	// Groups should appear
	assert.Contains(t, plain, "Resources", "must contain Resources group header")
}

// TestRenderStatusList_Empty renders an empty response (no commands).
func TestRenderStatusList_Empty(t *testing.T) {
	th := theme.New("formae")
	resp := &apimodel.ListCommandStatusResponse{Commands: nil}
	out := renderStatusListAt(th, resp, false, 100, fixedNow)
	plain := stripANSI(out)
	assert.True(t, strings.Contains(plain, "no commands"),
		"empty response should indicate no commands, got: %q", plain)
}
