// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"fmt"
	"sort"
	"time"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

type updateKind int

const (
	kindTarget updateKind = iota
	kindStack
	kindPolicy
	kindResource
)

type updateRow struct {
	kind       updateKind
	key        string // stable identity: kind prefix + label — survives poll refreshes
	label      string
	typeName   string // resources: ResourceType; policies: PolicyType
	stack      string
	operation  string
	state      components.State
	stateLabel string // "finishing"/"canceled" override, "" otherwise
	duration   time.Duration
	errMsg     string
	statusMsg  string
	cascadeSrc string
	attempt    int
	maxAttempt int
	// kind-specific card fields
	discoverable      bool     // targets
	description       string   // stacks
	referencingStacks []string // policies
}

type group struct {
	kind  updateKind
	title string // "Targets" | "Stacks" | "Policies" | "Resources"
	rows  []updateRow
}

// Detail column indexes
const (
	detailColStatus = iota
	detailColLabel
	detailColType
	detailColStack
	detailColOperation
	detailColTime
)

func buildGroups(c apimodel.Command) []group {
	groups := []group{}

	// Targets
	if len(c.TargetUpdates) > 0 {
		g := group{
			kind:  kindTarget,
			title: "Targets",
			rows:  []updateRow{},
		}
		for _, u := range c.TargetUpdates {
			r := updateRow{
				kind:         kindTarget,
				key:          fmt.Sprintf("target/%s", u.TargetLabel),
				label:        u.TargetLabel,
				operation:    u.Operation,
				state:        mapUpdateState(u.State),
				stateLabel:   stateLabel(c.State, u.State),
				duration:     time.Duration(u.Duration) * time.Millisecond,
				errMsg:       u.ErrorMessage,
				cascadeSrc:   u.CascadeSource,
				discoverable: u.Discoverable,
			}
			g.rows = append(g.rows, r)
		}
		groups = append(groups, g)
	}

	// Stacks
	if len(c.StackUpdates) > 0 {
		g := group{
			kind:  kindStack,
			title: "Stacks",
			rows:  []updateRow{},
		}
		for _, u := range c.StackUpdates {
			r := updateRow{
				kind:        kindStack,
				key:         fmt.Sprintf("stack/%s", u.StackLabel),
				label:       u.StackLabel,
				operation:   u.Operation,
				state:       mapUpdateState(u.State),
				stateLabel:  stateLabel(c.State, u.State),
				duration:    time.Duration(u.Duration) * time.Millisecond,
				errMsg:      u.ErrorMessage,
				description: u.Description,
			}
			g.rows = append(g.rows, r)
		}
		groups = append(groups, g)
	}

	// Policies
	if len(c.PolicyUpdates) > 0 {
		g := group{
			kind:  kindPolicy,
			title: "Policies",
			rows:  []updateRow{},
		}
		for _, u := range c.PolicyUpdates {
			r := updateRow{
				kind:              kindPolicy,
				key:               fmt.Sprintf("policy/%s", u.PolicyLabel),
				label:             u.PolicyLabel,
				typeName:          u.PolicyType,
				stack:             u.StackLabel,
				operation:         u.Operation,
				state:             mapUpdateState(u.State),
				stateLabel:        stateLabel(c.State, u.State),
				duration:          time.Duration(u.Duration) * time.Millisecond,
				errMsg:            u.ErrorMessage,
				referencingStacks: u.ReferencingStacks,
			}
			g.rows = append(g.rows, r)
		}
		groups = append(groups, g)
	}

	// Resources
	if len(c.ResourceUpdates) > 0 {
		g := group{
			kind:  kindResource,
			title: "Resources",
			rows:  []updateRow{},
		}
		for _, u := range c.ResourceUpdates {
			r := updateRow{
				kind:       kindResource,
				key:        fmt.Sprintf("resource/%s", u.ResourceLabel),
				label:      u.ResourceLabel,
				typeName:   u.ResourceType,
				stack:      u.StackName,
				operation:  u.Operation,
				state:      mapUpdateState(u.State),
				stateLabel: stateLabel(c.State, u.State),
				duration:   time.Duration(u.Duration) * time.Millisecond,
				errMsg:     u.ErrorMessage,
				statusMsg:  u.StatusMessage,
				cascadeSrc: u.CascadeSource,
				attempt:    u.CurrentAttempt,
				maxAttempt: u.MaxAttempts,
			}
			g.rows = append(g.rows, r)
		}
		groups = append(groups, g)
	}

	return groups
}

func visibleRows(g group, limit int) ([]updateRow, int) {
	if limit >= len(g.rows) {
		return g.rows, 0
	}
	return g.rows[:limit], len(g.rows) - limit
}

func validSortCols(kind updateKind) []int {
	switch kind {
	case kindPolicy:
		return []int{detailColStatus, detailColLabel, detailColType, detailColStack, detailColOperation, detailColTime}
	case kindResource:
		return []int{detailColStatus, detailColLabel, detailColType, detailColOperation, detailColTime}
	default: // targets, stacks
		return []int{detailColStatus, detailColLabel, detailColOperation, detailColTime}
	}
}

func sortGroup(rows []updateRow, col int, dir components.SortDirection) {
	less := func(a, b updateRow) bool {
		switch col {
		case detailColStatus:
			return statusRank(a) < statusRank(b)
		case detailColLabel:
			return a.label < b.label
		case detailColType:
			return a.typeName < b.typeName
		case detailColStack:
			return a.stack < b.stack
		case detailColOperation:
			return a.operation < b.operation
		case detailColTime:
			return a.duration < b.duration
		}
		return false
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if dir == components.SortDesc {
			return less(rows[j], rows[i])
		}
		return less(rows[i], rows[j])
	})
}

// statusRank orders update rows by urgency: failed → in-progress → pending → skipped → done.
func statusRank(r updateRow) int {
	switch r.state {
	case components.StateFailed:
		return 0
	case components.StateInProgress:
		return 1
	case components.StatePending:
		return 2
	case components.StateSkipped:
		return 3
	}
	return 4
}
