// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package simview

import (
	"os"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func TestMain(m *testing.M) {
	tuitest.PinRendering()
	os.Exit(m.Run())
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

// makeFixtureCmd returns a rich apimodel.Command that exercises all groups,
// cascade sub-lines, policy keep/detach, replace pairing, and enough resource
// rows to trigger pagination (>10).
func makeFixtureCmd() apimodel.Command {
	return apimodel.Command{
		CommandID: "cmd-fixture",
		Command:   "apply",
		Mode:      "reconcile",
		TargetUpdates: []apimodel.TargetUpdate{
			{TargetLabel: "aws-us-east-1", Operation: "create"},
			{TargetLabel: "aws-eu-west-1", Operation: "update", Discoverable: true},
			{TargetLabel: "aws-ap-south-1", Operation: "replace"},
		},
		StackUpdates: []apimodel.StackUpdate{
			{StackLabel: "production", Operation: "create"},
		},
		PolicyUpdates: []apimodel.PolicyUpdate{
			{PolicyLabel: "auto-reconcile", PolicyType: "ttl", StackLabel: "production", Operation: "create"},
			{PolicyLabel: "staging-reconcile", PolicyType: "auto-reconcile", StackLabel: "staging", Operation: "update"},
			{PolicyLabel: "old-policy", PolicyType: "ttl", StackLabel: "staging", Operation: "detach"},
			{
				PolicyLabel:       "shared-policy",
				PolicyType:        "ttl",
				StackLabel:        "shared",
				Operation:         "skip",
				ReferencingStacks: []string{"production", "development"},
			},
		},
		ResourceUpdates: []apimodel.ResourceUpdate{
			// 2 deletes (one cascade)
			{ResourceID: "r-del1", ResourceLabel: "old-cache", ResourceType: "AWS::ElastiCache::CacheCluster", StackName: "production", Operation: "delete"},
			{ResourceID: "r-del2", ResourceLabel: "legacy-queue", ResourceType: "AWS::SQS::Queue", StackName: "production", Operation: "delete", IsCascade: true, CascadeSource: "old-cache"},
			// 2 updates
			{ResourceID: "r-upd1", ResourceLabel: "primary", ResourceType: "AWS::RDS::DBInstance", StackName: "production", Operation: "update"},
			{ResourceID: "r-upd2", ResourceLabel: "web-config", ResourceType: "AWS::SSM::Parameter", StackName: "production", Operation: "update"},
			// 1 replace pair
			{ResourceID: "r-rep-del", ResourceLabel: "web-server", ResourceType: "AWS::EC2::Instance", StackName: "production", Operation: "delete", GroupID: "grp-web"},
			{ResourceID: "r-rep-cre", ResourceLabel: "web-server", ResourceType: "AWS::EC2::Instance", StackName: "production", Operation: "create", GroupID: "grp-web"},
			// 7 creates (to push total resources to 12, triggering pagination)
			{ResourceID: "r-cre1", ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket", StackName: "production", Operation: "create"},
			{ResourceID: "r-cre2", ResourceLabel: "web-1", ResourceType: "AWS::EC2::Instance", StackName: "production", Operation: "create"},
			{ResourceID: "r-cre3", ResourceLabel: "cdn", ResourceType: "AWS::CloudFront::Distribution", StackName: "production", Operation: "create"},
			{ResourceID: "r-cre4", ResourceLabel: "app-server", ResourceType: "AWS::EC2::Instance", StackName: "production", Operation: "create"},
			{ResourceID: "r-cre5", ResourceLabel: "log-group", ResourceType: "AWS::CloudWatch::LogGroup", StackName: "production", Operation: "create"},
			{ResourceID: "r-cre6", ResourceLabel: "api-gw", ResourceType: "AWS::ApiGateway::RestApi", StackName: "production", Operation: "create"},
			{ResourceID: "r-cre7", ResourceLabel: "cache-layer", ResourceType: "AWS::ElastiCache::CacheCluster", StackName: "production", Operation: "create"},
		},
	}
}

func makeFixtureSim() *apimodel.Simulation {
	cmd := makeFixtureCmd()
	return &apimodel.Simulation{
		ChangesRequired: true,
		Command:         cmd,
	}
}

func makeModel(width, height int) Model {
	th := theme.New("formae")
	sim := makeFixtureSim()
	opts := Options{
		Kind: KindApply,
		Mode: "reconcile",
	}
	m := New(th, sim, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return mm.(Model)
}

// TestSimView_GoldenApply checks the initial 100x32 render against a golden file.
func TestSimView_GoldenApply(t *testing.T) {
	m := makeModel(100, 32)
	tuitest.RequireGolden(t, []byte(m.View()))
}

// TestSimView_PaginationShowsMore navigates to the "show more" row in the
// Resources group and presses enter; asserts that 10 more rows become visible.
func TestSimView_PaginationShowsMore(t *testing.T) {
	m := makeModel(100, 40)

	// Resources group has 12 rows → first 10 shown + show-more.
	// Nav list: targets(3) + stack(1) + policies(4) + resources(10) + show-more = 19 navigable entries.
	// Navigate to show-more of resources group.
	nav := m.navLines()
	showMoreIdx := -1
	for i, n := range nav {
		if n.kind == navShowMore && n.rowKind == kindResource {
			showMoreIdx = i
			break
		}
	}
	require.GreaterOrEqual(t, showMoreIdx, 0, "show-more row for resources must exist")

	// Navigate cursor to show-more
	for m.cursor < showMoreIdx {
		var mm tea.Model
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = mm.(Model)
	}
	assert.Equal(t, showMoreIdx, m.cursor, "cursor should be on show-more")

	// Press enter to expand
	var mm tea.Model
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)

	assert.Equal(t, 20, m.visible[kindResource], "visible[resource] should expand by 10 to 20")
}

// TestSimView_SortByColumn presses → then s and asserts the Resources group
// is sorted by Label (ascending).
func TestSimView_SortByColumn(t *testing.T) {
	m := makeModel(100, 40)

	// Navigate cursor into resources group (past targets + stack + policies)
	nav := m.navLines()
	resourceRowIdx := -1
	for i, n := range nav {
		if n.kind == navRow && n.rowKind == kindResource {
			resourceRowIdx = i
			break
		}
	}
	require.GreaterOrEqual(t, resourceRowIdx, 0, "must find a resource nav row")

	for m.cursor < resourceRowIdx {
		var mm tea.Model
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = mm.(Model)
	}

	// Press → to highlight "Label" column (col 1)
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = mm.(Model)
	assert.Equal(t, colLabel, m.sortHi[kindResource], "→ should highlight label column")

	// Press s to sort by label
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = mm.(Model)
	assert.Equal(t, colLabel, m.sortCol[kindResource], "s should activate label sort column")

	// Verify resources group rows are sorted by label
	g := m.groupForKind(kindResource)
	require.NotNil(t, g)
	labels := make([]string, len(g.rows))
	for i, r := range g.rows {
		labels[i] = r.label
	}
	for i := 1; i < len(labels); i++ {
		assert.LessOrEqual(t, labels[i-1], labels[i], "rows should be sorted by label ascending at index %d", i)
	}
}

// TestSimView_CursorNavigation presses Down 3 times and asserts the cursor is on
// row index 3 (0-based), which shows the Selection background in View.
func TestSimView_CursorNavigation(t *testing.T) {
	m := makeModel(100, 40)
	assert.Equal(t, 0, m.cursor, "cursor starts at 0")

	for i := 0; i < 3; i++ {
		var mm tea.Model
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = mm.(Model)
	}
	assert.Equal(t, 3, m.cursor, "cursor should be 3 after three Down presses")

	// Verify the 4th navigable row (nav index 3 = the Stacks "production" row
	// in the fixture) carries the full-width Selection background. The formae
	// theme's Selection dark value is #3A3A3A → truecolor bg "48;2;58;58;58".
	// Note: the sort-column header highlight also uses the Selection bg, so we
	// locate the cursor DATA row (the stack row "+ create production") and
	// assert it carries the background.
	const selBg = "48;2;58;58;58"
	raw := m.View()
	cursorLine := ""
	for _, line := range strings.Split(raw, "\n") {
		pl := plain(line)
		if strings.Contains(pl, "production") && strings.Contains(pl, "create") {
			cursorLine = line
			break
		}
	}
	require.NotEmpty(t, cursorLine, "the '+ create production' stack row must be rendered")
	assert.Contains(t, cursorLine, selBg,
		"the Stacks row (nav index 3) must carry the full-width Selection background")
}

// TestSimView_ConfirmAndAbortKeys verifies 'y' sets DecisionConfirmed and 'q' sets DecisionAborted.
func TestSimView_ConfirmAndAbortKeys(t *testing.T) {
	t.Run("confirm_y", func(t *testing.T) {
		m := makeModel(100, 32)
		assert.Equal(t, DecisionAborted, m.Decision(), "initial decision is aborted")
		mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		assert.Equal(t, DecisionConfirmed, mm.(Model).decision, "y sets DecisionConfirmed")
		assert.NotNil(t, cmd, "y returns a tea.Cmd (quit)")
	})

	t.Run("abort_q", func(t *testing.T) {
		m := makeModel(100, 32)
		mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		assert.Equal(t, DecisionAborted, mm.(Model).decision, "q keeps DecisionAborted")
		assert.NotNil(t, cmd, "q returns a tea.Cmd (quit)")
	})

	t.Run("abort_esc", func(t *testing.T) {
		m := makeModel(100, 32)
		mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		assert.Equal(t, DecisionAborted, mm.(Model).decision, "esc keeps DecisionAborted")
		assert.NotNil(t, cmd, "esc returns a tea.Cmd (quit)")
	})
}

// TestSimView_ViewLineCountMatchesHeight checks exact-fill at multiple sizes.
func TestSimView_ViewLineCountMatchesHeight(t *testing.T) {
	sizes := []struct{ w, h int }{
		{80, 10},
		{100, 24},
		{100, 32},
	}
	for _, sz := range sizes {
		t.Run("size", func(t *testing.T) {
			m := makeModel(sz.w, sz.h)
			v := m.View()
			lines := strings.Split(v, "\n")
			assert.Equal(t, sz.h, len(lines),
				"View() must produce exactly %d lines at %dx%d, got %d",
				sz.h, sz.w, sz.h, len(lines))
		})
	}

	// Ack screen exact-fill
	for _, sz := range sizes {
		t.Run("ack_screen", func(t *testing.T) {
			m := makeDescriptionAckModel(sz.w, sz.h)
			v := m.View()
			lines := strings.Split(v, "\n")
			assert.Equal(t, sz.h, len(lines),
				"ack View() must produce exactly %d lines at %dx%d, got %d",
				sz.h, sz.w, sz.h, len(lines))
		})
	}

	// Destroy cascade multi-line footer exact-fill
	for _, sz := range sizes {
		t.Run("destroy_cascade", func(t *testing.T) {
			th := theme.New("formae")
			sim := makeDestroyWithCascades()
			opts := Options{Kind: KindDestroy}
			m := New(th, sim, opts)
			var mm tea.Model = m
			mm, _ = mm.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
			m = mm.(Model)
			v := m.View()
			lines := strings.Split(v, "\n")
			assert.Equal(t, sz.h, len(lines),
				"cascade destroy View() must produce exactly %d lines at %dx%d, got %d",
				sz.h, sz.w, sz.h, len(lines))
		})
	}
}

// TestSimView_RenderingIntegrity checks no ANSI fragments and no line overflows.
func TestSimView_RenderingIntegrity(t *testing.T) {
	const w = 100

	t.Run("collapsed", func(t *testing.T) {
		m := makeModel(w, 40)
		raw := m.View()
		plainView := plain(raw)

		t.Run("no_ansi_fragment_garbage", func(t *testing.T) {
			lines := strings.Split(plainView, "\n")
			for i, line := range lines {
				assert.NotRegexp(t, `\[[0-9;]+[A-Za-z]`, line,
					"line %d contains ANSI fragment garbage: %q", i+1, line)
			}
		})

		t.Run("no_line_overflows_viewport_width", func(t *testing.T) {
			lines := strings.Split(plainView, "\n")
			for i, line := range lines {
				rw := lipgloss.Width(line)
				assert.LessOrEqual(t, rw, w,
					"line %d overflows viewport (width %d): %q", i+1, rw, line)
			}
		})
	})

	t.Run("expanded_card", func(t *testing.T) {
		// Build a model with a resource with patch data, expand it, check integrity.
		cmd := makeFixtureCmd()
		for i := range cmd.ResourceUpdates {
			if cmd.ResourceUpdates[i].ResourceLabel == "primary" {
				cmd.ResourceUpdates[i].PatchDocument = []byte(`[{"op":"replace","path":"/InstanceClass","value":"db.t3.large"}]`)
				cmd.ResourceUpdates[i].Properties = []byte(`{"InstanceClass":"db.t3.large"}`)
				cmd.ResourceUpdates[i].OldProperties = []byte(`{"InstanceClass":"db.t3.medium"}`)
			}
		}

		th := theme.New("formae")
		sim := &apimodel.Simulation{ChangesRequired: true, Command: cmd}
		opts := Options{Kind: KindApply, Mode: "reconcile"}
		m := New(th, sim, opts)
		var mm tea.Model = m
		mm, _ = mm.Update(tea.WindowSizeMsg{Width: w, Height: 50})
		m = mm.(Model)

		// Navigate to "primary" and expand
		nav := m.navLines()
		for i, n := range nav {
			if n.kind == navRow && n.rowKind == kindResource && n.rowKey == "resource/production/primary" {
				for m.cursor < i {
					var mm2 tea.Model
					mm2, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
					m = mm2.(Model)
				}
				break
			}
		}
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
		m = mm.(Model)

		raw := m.View()
		plainView := plain(raw)

		t.Run("no_ansi_fragment_garbage", func(t *testing.T) {
			lines := strings.Split(plainView, "\n")
			for i, line := range lines {
				assert.NotRegexp(t, `\[[0-9;]+[A-Za-z]`, line,
					"line %d contains ANSI fragment garbage: %q", i+1, line)
			}
		})

		t.Run("no_line_overflows_viewport_width", func(t *testing.T) {
			lines := strings.Split(plainView, "\n")
			for i, line := range lines {
				rw := lipgloss.Width(line)
				assert.LessOrEqual(t, rw, w,
					"line %d overflows viewport (width %d): %q", i+1, rw, line)
			}
		})

		t.Run("card_visible_in_view", func(t *testing.T) {
			assert.Contains(t, plainView, "Operation:", "expanded card should be visible in view")
			assert.Contains(t, plainView, "InstanceClass", "card changes should be visible in view")
		})
	})
}

// makeDescriptionAckSim returns a sim with Description.Confirm=true.
func makeDescriptionAckSim() *apimodel.Simulation {
	cmd := makeFixtureCmd()
	return &apimodel.Simulation{
		ChangesRequired: true,
		Command:         cmd,
	}
}

// makeDescriptionAckOpts returns Options with Description.Confirm=true.
func makeDescriptionAckOpts() Options {
	return Options{
		Kind: KindApply,
		Mode: "reconcile",
		Description: apimodel.Description{
			Text:    "This forma deploys the production database cluster. Applying this will cause a brief connectivity interruption during the RDS failover.",
			Confirm: true,
		},
	}
}

// makeDescriptionAckModel builds a model with Confirm description at given size.
func makeDescriptionAckModel(width, height int) Model {
	th := theme.New("formae")
	sim := makeDescriptionAckSim()
	opts := makeDescriptionAckOpts()
	m := New(th, sim, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return mm.(Model)
}

// TestSimView_GoldenDescriptionAck checks the ack screen golden at 100x32.
func TestSimView_GoldenDescriptionAck(t *testing.T) {
	m := makeDescriptionAckModel(100, 32)
	tuitest.RequireGolden(t, []byte(m.View()))
}

// TestSimView_AckEnterProceeds verifies enter on ack screen shows preview screen.
func TestSimView_AckEnterProceeds(t *testing.T) {
	m := makeDescriptionAckModel(100, 32)
	// Initially on ack screen
	plainAck := plain(m.View())
	assert.Contains(t, plainAck, "Press enter to continue", "ack screen must show enter prompt")

	// Press enter — should transition to preview screen
	var mm tea.Model
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	plainPreview := plain(m.View())
	assert.NotContains(t, plainPreview, "Press enter to continue", "preview screen must not show ack text")
	// preview should show the simulation content
	assert.Contains(t, plainPreview, "create", "preview screen must show simulation content")
}

// TestSimView_GoldenSimulateOnlyFooter checks the simulate-only footer golden at 100x32.
func TestSimView_GoldenSimulateOnlyFooter(t *testing.T) {
	th := theme.New("formae")
	sim := makeFixtureSim()
	opts := Options{
		Kind:         KindApply,
		Mode:         "reconcile",
		SimulateOnly: true,
	}
	m := New(th, sim, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	m = mm.(Model)
	tuitest.RequireGolden(t, []byte(m.View()))
}

// TestSimView_SimulateOnlyIgnoresY verifies that y does NOT quit when SimulateOnly=true,
// and that the default decision remains DecisionAborted.
func TestSimView_SimulateOnlyIgnoresY(t *testing.T) {
	th := theme.New("formae")
	sim := makeFixtureSim()
	opts := Options{
		Kind:         KindApply,
		Mode:         "reconcile",
		SimulateOnly: true,
	}
	m := New(th, sim, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	m = mm.(Model)

	// Default decision is aborted
	assert.Equal(t, DecisionAborted, m.Decision(), "initial decision must be DecisionAborted")

	// Press y — must be a no-op (no quit, decision stays default)
	var cmd tea.Cmd
	mm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = mm.(Model)
	assert.Nil(t, cmd, "y must NOT return a quit cmd when SimulateOnly=true")
	assert.Equal(t, DecisionAborted, m.Decision(), "y must NOT change decision when SimulateOnly=true")

	// q should still quit
	mm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	assert.NotNil(t, cmd, "q must still return a quit cmd when SimulateOnly=true")
	assert.Equal(t, DecisionAborted, mm.(Model).Decision(), "q leaves decision as DecisionAborted")
}

// makeDestroyWithCascades builds a Simulation for a destroy with cascade resources.
func makeDestroyWithCascades() *apimodel.Simulation {
	return &apimodel.Simulation{
		ChangesRequired: true,
		Command: apimodel.Command{
			CommandID: "cmd-destroy-cascade",
			Command:   "destroy",
			Mode:      "reconcile",
			ResourceUpdates: []apimodel.ResourceUpdate{
				{ResourceID: "r-db", ResourceLabel: "primary-db", ResourceType: "AWS::RDS::DBInstance", StackName: "production", Operation: "delete"},
				{ResourceID: "r-ws", ResourceLabel: "web-server", ResourceType: "AWS::EC2::Instance", StackName: "production", Operation: "delete", IsCascade: true, CascadeSource: "primary-db"},
				{ResourceID: "r-lg", ResourceLabel: "log-group", ResourceType: "AWS::CloudWatch::LogGroup", StackName: "production", Operation: "delete", IsCascade: true, CascadeSource: "web-server"},
				{ResourceID: "r-ah", ResourceLabel: "api-handler", ResourceType: "AWS::Lambda::Function", StackName: "production", Operation: "delete"},
			},
		},
	}
}

// TestSimView_GoldenDestroyCascadeBanner checks the destroy cascade banner golden at 100x32.
func TestSimView_GoldenDestroyCascadeBanner(t *testing.T) {
	th := theme.New("formae")
	sim := makeDestroyWithCascades()
	opts := Options{
		Kind:   KindDestroy,
		Source: "query: stack:staging",
	}
	m := New(th, sim, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	m = mm.(Model)
	tuitest.RequireGolden(t, []byte(m.View()))
}

// TestSimView_DestroyConfirmCountsDependents verifies the destroy cascade footer
// distinguishes direct vs cascade deletes.
func TestSimView_DestroyConfirmCountsDependents(t *testing.T) {
	th := theme.New("formae")
	sim := makeDestroyWithCascades()
	opts := Options{
		Kind:   KindDestroy,
		Source: "query: stack:staging",
	}
	m := New(th, sim, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	m = mm.(Model)
	plainView := plain(m.View())

	// Should mention total resource count (4) and cascade count (2)
	assert.Contains(t, plainView, "4 resource(s)", "footer must mention total resource count")
	// cascade count and dependency reason must appear (may be split across lines)
	assert.Contains(t, plainView, "2 will be deleted", "footer must mention cascade delete count")
	assert.Contains(t, plainView, "depend on other targeted resources", "footer must explain cascade reason")
}

// TestSimView_SourceInHeader verifies that Options.Source renders in the header.
func TestSimView_SourceInHeader(t *testing.T) {
	th := theme.New("formae")
	sim := makeFixtureSim()
	opts := Options{
		Kind:   KindApply,
		Mode:   "reconcile",
		Source: "query: stack:production",
	}
	m := New(th, sim, opts)
	var mm tea.Model = m
	mm, _ = mm.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	m = mm.(Model)
	plainView := plain(m.View())
	// Source must appear in the header line (first or second line)
	headerLines := strings.Split(plainView, "\n")[:3]
	headerText := strings.Join(headerLines, "\n")
	assert.Contains(t, headerText, "query: stack:production", "Options.Source must appear in header")
}
