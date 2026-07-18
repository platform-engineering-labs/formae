// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package status

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ansiReAgent strips well-formed ANSI CSI sequences.
var ansiReAgent = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSIAgent(s string) string { return ansiReAgent.ReplaceAllString(s, "") }

// TestMain ensures deterministic lipgloss rendering for golden tests.
// NOTE: status_test.go already lives in the same package; we cannot have two
// TestMain functions. The tuitest.PinRendering() call must only appear once.
// If status_test.go defines TestMain, this file should NOT define it.
// Since status_test.go does NOT define TestMain, we define it here.
func TestMain(m *testing.M) {
	tuitest.PinRendering()
	os.Exit(m.Run())
}

func makeFullStats() apimodel.Stats {
	return apimodel.Stats{
		Version: "1.2.3",
		AgentID: "agent-abc123",
		Clients: 2,
		Commands: map[string]int{
			"apply":   8,
			"destroy": 4,
		},
		States: map[string]int{
			"Success":    10,
			"Failed":     1,
			"InProgress": 1,
		},
		Stacks: 3,
		ManagedResources: map[string]int{
			"AWS": 47,
		},
		UnmanagedResources: map[string]int{
			"AWS": 3,
		},
		Targets: map[string]int{
			"AWS": 2,
		},
		ResourceTypes: map[string]int{
			"AWS::S3::Bucket":       8,
			"AWS::EC2::Instance":    15,
			"AWS::RDS::DBInstance":  3,
			"AWS::Lambda::Function": 12,
			"AWS::IAM::Role":        9,
		},
		ResourceErrors: map[string]int{
			"AWS::RDS::DBInstance": 1,
		},
		Plugins: []apimodel.PluginInfo{
			{Namespace: "AWS", Version: "2.1.0"},
		},
	}
}

func TestRenderAgentStats_Golden(t *testing.T) {
	th := theme.New("formae")
	stats := makeFullStats()
	out := renderAgentStats(th, stats, 120)
	tuitest.RequireGolden(t, []byte(out))
}

func TestRenderAgentStats_TopNTruncation(t *testing.T) {
	th := theme.New("formae")
	stats := makeFullStats()
	// Build 15 resource types
	types := map[string]int{}
	for i := range 15 {
		types[fmt.Sprintf("AWS::Type%02d::Resource", i)] = i + 1
	}
	stats.ResourceTypes = types
	stats.ResourceErrors = nil // suppress errors panel

	out := renderAgentStats(th, stats, 120)
	plain := stripANSIAgent(out)

	// Should contain "and 5 more types"
	assert.Contains(t, plain, "and 5 more types", "truncation footer expected")

	// Count how many "AWS::Type" rows appear (header "Total Types" doesn't count)
	lines := strings.Split(plain, "\n")
	typeRows := 0
	for _, l := range lines {
		if strings.Contains(l, "AWS::Type") {
			typeRows++
		}
	}
	assert.Equal(t, 10, typeRows, "exactly 10 resource type rows expected")
}

func TestRenderAgentStats_NoErrorsPanelElided(t *testing.T) {
	th := theme.New("formae")
	stats := makeFullStats()
	stats.ResourceErrors = nil // or empty map
	out := renderAgentStats(th, stats, 120)
	plain := stripANSIAgent(out)
	assert.NotContains(t, plain, "Resource Errors", "errors panel should not appear when empty")
}

func TestRenderAgentStats_NarrowStacksVertically(t *testing.T) {
	th := theme.New("formae")
	stats := makeFullStats()
	out := renderAgentStats(th, stats, 60)
	plain := stripANSIAgent(out)

	// At narrow width, panels stack vertically.
	// Verify that no single line contains both "Structure" and "Commands".
	lines := strings.Split(plain, "\n")
	for _, l := range lines {
		if strings.Contains(l, "Structure") && strings.Contains(l, "Commands") {
			t.Errorf("narrow terminal: found two panel titles on one line: %q", l)
		}
		if strings.Contains(l, "Commands") && strings.Contains(l, "Resources") {
			t.Errorf("narrow terminal: found two panel titles on one line: %q", l)
		}
	}
}

func TestRenderAgentStats_UnmanagedThresholds(t *testing.T) {
	th := theme.New("formae")

	cases := []struct {
		name      string
		managed   int
		unmanaged int
		symbol    string
	}{
		{"zero-pct", 100, 0, "✓"},   // 0% → Done ✓
		{"forty-pct", 60, 40, "⚠"},  // 40% → Warning ⚠
		{"eighty-pct", 20, 80, "✗"}, // 80% → Error ✗
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stats := makeFullStats()
			stats.ManagedResources = map[string]int{"AWS": tc.managed}
			stats.UnmanagedResources = map[string]int{"AWS": tc.unmanaged}
			stats.ResourceErrors = nil

			out := renderAgentStats(th, stats, 120)
			plain := stripANSIAgent(out)
			assert.Contains(t, plain, tc.symbol,
				"unmanaged threshold symbol %q expected", tc.symbol)
		})
	}
}

func TestPanelRow_JoinsHorizontallyWhenFits(t *testing.T) {
	a := "AAA"
	b := "BBB"
	result := panelRow(200, a, b)
	// When panels fit, lipgloss joins horizontally → result is one line
	lines := strings.Split(result, "\n")
	found := false
	for _, l := range lines {
		if strings.Contains(l, "AAA") && strings.Contains(l, "BBB") {
			found = true
			break
		}
	}
	assert.True(t, found, "horizontal join should put both panels on the same line")
}

func TestPanelRow_StacksVerticallyWhenTooNarrow(t *testing.T) {
	// Make panels wider than total width
	wide := strings.Repeat("X", 80)
	result := panelRow(10, wide, wide)
	lines := strings.Split(result, "\n")
	// Vertical stack: first panel's content on early lines, second on later lines
	require.GreaterOrEqual(t, len(lines), 2)
	// The two wide lines should NOT be on the same line
	for _, l := range lines {
		assert.LessOrEqual(t, lipgloss.Width(l), 80+1,
			"no single line should have both panels side by side: %q", l)
	}
}
