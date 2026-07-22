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

func TestRenderAgentStats_ErrorsPanelCapsAtTen(t *testing.T) {
	th := theme.New("formae")
	stats := makeFullStats()
	// Build 13 resource error entries
	errs := map[string]int{}
	for i := range 13 {
		errs[fmt.Sprintf("AWS::Type%02d::Error", i)] = i + 1
	}
	stats.ResourceErrors = errs

	out := renderAgentStats(th, stats, 120)
	plain := stripANSIAgent(out)

	// Should contain "and 3 more errors"
	assert.Contains(t, plain, "and 3 more errors", "truncation footer expected for errors panel")

	// Count error type rows (lines containing "AWS::Type" inside errors panel)
	lines := strings.Split(plain, "\n")
	typeRows := 0
	for _, l := range lines {
		if strings.Contains(l, "AWS::Type") && strings.Contains(l, "Error") {
			typeRows++
		}
	}
	assert.Equal(t, 10, typeRows, "exactly 10 error rows expected")
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
	th := theme.New("formae")
	a := panelSpec{title: "A", color: th.Palette.Border, lines: []string{"AAA"}}
	b := panelSpec{title: "B", color: th.Palette.Border, lines: []string{"BBB"}}
	result := renderPanelRow(th, 200, 30, 2, a, b)
	// When panels fit, they're joined horizontally → both on the same line.
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

func TestPanelRow_EqualizesHeightAndGaps(t *testing.T) {
	th := theme.New("formae")
	short := panelSpec{title: "S", color: th.Palette.Border, lines: []string{"one"}}
	tall := panelSpec{title: "T", color: th.Palette.Border, lines: []string{"one", "two", "three"}}
	result := renderPanelRow(th, 200, 30, 2, short, tall)
	lines := strings.Split(result, "\n")
	// Height-equalized: every rendered row spans both boxes (no row where the
	// short box has already closed while the tall one continues), so all lines
	// share one width.
	width := lipgloss.Width(lines[0])
	for _, l := range lines {
		assert.Equal(t, width, lipgloss.Width(l), "all rows must be equal width: %q", l)
	}
}

func TestPanelRow_StacksVerticallyWhenTooNarrow(t *testing.T) {
	th := theme.New("formae")
	// panelW wider than the terminal forces a vertical stack.
	spec := panelSpec{title: "X", color: th.Palette.Border, lines: []string{strings.Repeat("X", 60)}}
	result := renderPanelRow(th, 10, 80, 2, spec, spec)
	lines := strings.Split(result, "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	for _, l := range lines {
		assert.LessOrEqual(t, lipgloss.Width(l), 84,
			"no single line should have both panels side by side: %q", l)
	}
}
