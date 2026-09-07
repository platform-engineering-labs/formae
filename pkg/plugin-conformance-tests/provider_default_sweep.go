// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The omit-and-observe sweep records what a provider actually does with every
// field a schema annotates hasProviderDefault. The CRUD suite already performs
// the experiment — its fixtures declare a subset of the schema, so every
// annotated field a fixture leaves out is created omitted — and it already
// reads the resource back twice, once from the create echo and once after a
// forced cloud sync. The sweep captures those two reads per annotated field
// instead of discarding them.
//
// What the record decides and what it does not: a field the provider populates
// when omitted has an empirically justified annotation, and a value that moves
// between the two reads without formae writing it names a co-actor. A field
// that never appears decides nothing on its own — the co-actor that would
// populate it is absent from an isolated fixture by construction — so an
// unexercised annotation routes to the documentation pass rather than to
// removal.
//
// Timescale is the honest limit. The two reads are seconds apart (widen with
// FORMAE_TEST_SETTLE_SECONDS), which catches asynchronous population but not
// values a provider moves on a maintenance-window cadence.

const (
	// envProviderDefaultObservations names the file the sweep artifact is
	// written to. Unset means no sweep: the CRUD suite behaves exactly as it
	// did before.
	envProviderDefaultObservations = "FORMAE_TEST_PROVIDER_DEFAULT_OBSERVATIONS"

	// envSettleWindowSeconds widens the gap between the create echo and the
	// post-sync read, so a provider that populates a field asynchronously has
	// had time to do it before the second observation is taken.
	envSettleWindowSeconds = "FORMAE_TEST_SETTLE_SECONDS"

	// settleWindowCap bounds the wait. The conformance matrix runs ~100 jobs
	// against one shared account, so an oversized settle window stalls the
	// whole run rather than only its own case.
	settleWindowCap = 300 * time.Second
)

// getSettleWindow returns how long to wait before the post-create sync, from
// FORMAE_TEST_SETTLE_SECONDS. Unset, non-numeric, or non-positive means no
// wait, which is the default the CRUD suite has always run with.
func getSettleWindow() time.Duration {
	secs, err := strconv.Atoi(os.Getenv(envSettleWindowSeconds))
	if err != nil || secs <= 0 {
		return 0
	}
	window := time.Duration(secs) * time.Second
	if window > settleWindowCap {
		return settleWindowCap
	}
	return window
}

// Observation states. Absence, an explicit null, and an explicit empty carry
// different patch meanings, so they stay distinguishable rather than
// collapsing into one "unset".
const (
	obsAbsent          = "absent"
	obsNull            = "null"
	obsEmptyCollection = "empty-collection"
	obsEmptyString     = "empty-string"
	obsValue           = "value"
)

// obsRank orders observation states from weakest to strongest for aggregation
// across list elements.
var obsRank = map[string]int{
	obsAbsent:          0,
	obsNull:            1,
	obsEmptyString:     2,
	obsEmptyCollection: 3,
	obsValue:           4,
}

// ProviderDefaultObservation is one annotated field's record from one fixture.
type ProviderDefaultObservation struct {
	TestCase     string `json:"testCase"`
	ResourceType string `json:"resourceType"`
	// Path is the schema hint path: dot-separated, with list indices collapsed.
	Path string `json:"path"`
	// Declared reports whether the fixture supplied a value at this path. A
	// false here is what makes the row an omit-and-observe result.
	Declared bool `json:"declared"`
	// CreateEcho is the state read back straight after apply.
	CreateEcho string `json:"createEcho"`
	// AfterSync is the state read back after a forced sync with the cloud.
	AfterSync string `json:"afterSync"`
	// Moved reports that the value differs between the two reads. formae wrote
	// nothing in between, so a true here names a co-actor or a late provider
	// write.
	Moved bool `json:"moved"`

	// Sibling hints that put a field on one of the audit's separate branches:
	// a writeOnly field is never read back, and a createOnly field is not
	// reverted by a forced apply even though it still rejects a soft one.
	CreateOnly bool `json:"createOnly,omitempty"`
	WriteOnly  bool `json:"writeOnly,omitempty"`
	Opaque     bool `json:"opaque,omitempty"`
}

type providerDefaultSweep struct {
	mu   sync.Mutex
	rows []ProviderDefaultObservation
}

func newSweep() *providerDefaultSweep {
	return &providerDefaultSweep{}
}

// newSweepFromEnv returns a sweep when an artifact path is configured, and nil
// otherwise. A nil sweep is inert, so callers need no guard.
func newSweepFromEnv() *providerDefaultSweep {
	if os.Getenv(envProviderDefaultObservations) == "" {
		return nil
	}
	return newSweep()
}

// record captures one fixture's observations for every hasProviderDefault path
// in hints. declared is the fixture's evaluated properties; afterCreate and
// afterSync are the two inventory reads.
func (s *providerDefaultSweep) record(testCase, resourceType string, hints, declared, afterCreate, afterSync map[string]any) {
	if s == nil {
		return
	}
	rows := make([]ProviderDefaultObservation, 0, len(hints))
	for path, hint := range hints {
		hintMap, ok := hint.(map[string]any)
		if !ok {
			continue
		}
		if hpd, ok := hintMap["HasProviderDefault"].(bool); !ok || !hpd {
			continue
		}
		createValues := collectPath(afterCreate, path)
		syncValues := collectPath(afterSync, path)
		rows = append(rows, ProviderDefaultObservation{
			TestCase:     testCase,
			ResourceType: resourceType,
			Path:         path,
			Declared:     len(collectPath(declared, path)) > 0,
			CreateEcho:   aggregateState(createValues),
			AfterSync:    aggregateState(syncValues),
			Moved:        !reflect.DeepEqual(createValues, syncValues),
			CreateOnly:   boolHint(hintMap, "CreateOnly"),
			WriteOnly:    boolHint(hintMap, "WriteOnly"),
			Opaque:       boolHint(hintMap, "Opaque"),
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, rows...)
}

func boolHint(hint map[string]any, key string) bool {
	v, _ := hint[key].(bool)
	return v
}

// observations returns every recorded row in a stable order.
func (s *providerDefaultSweep) observations() []ProviderDefaultObservation {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ProviderDefaultObservation, len(s.rows))
	copy(out, s.rows)
	sort.Slice(out, func(i, j int) bool {
		if out[i].ResourceType != out[j].ResourceType {
			return out[i].ResourceType < out[j].ResourceType
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].TestCase < out[j].TestCase
	})
	return out
}

// writeTo writes the sweep artifact. Sorted, so a run diffs cleanly against
// the previous one.
func (s *providerDefaultSweep) writeTo(path string) error {
	if s == nil {
		return nil
	}
	artifact := struct {
		Comment      string                       `json:"_comment"`
		Observations []ProviderDefaultObservation `json:"observations"`
	}{
		Comment: "Omit-and-observe records for hasProviderDefault fields, from the conformance CRUD run. " +
			"declared=false rows are the omitted-field experiment; moved=true names a writer other than formae.",
		Observations: s.observations(),
	}
	raw, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// observeState reports the observation state of a hint path in a property map.
func observeState(props map[string]any, path string) string {
	return aggregateState(collectPath(props, path))
}

// collectPath walks a dot-separated hint path and returns every value found at
// it. A path crossing a list yields one entry per element that has the
// remaining path, which is why hint paths with collapsed list indices resolve
// to more than one value.
func collectPath(props map[string]any, path string) []any {
	if props == nil {
		return nil
	}
	return collectFrom(any(props), strings.Split(path, "."))
}

func collectFrom(node any, segments []string) []any {
	if len(segments) == 0 {
		return []any{node}
	}
	switch typed := node.(type) {
	case map[string]any:
		child, ok := typed[segments[0]]
		if !ok {
			return nil
		}
		return collectFrom(child, segments[1:])
	case []any:
		var out []any
		for _, element := range typed {
			out = append(out, collectFrom(element, segments)...)
		}
		return out
	default:
		return nil
	}
}

// aggregateState reduces the values found at one path to a single observation.
// The strongest state wins: the question a row answers is whether the provider
// put anything there, so one populated list element makes the path populated.
func aggregateState(values []any) string {
	state := obsAbsent
	for _, v := range values {
		if s := stateOf(v); obsRank[s] > obsRank[state] {
			state = s
		}
	}
	return state
}

func stateOf(value any) string {
	switch typed := value.(type) {
	case nil:
		return obsNull
	case string:
		if typed == "" {
			return obsEmptyString
		}
		return obsValue
	case map[string]any:
		if len(typed) == 0 {
			return obsEmptyCollection
		}
		return obsValue
	case []any:
		if len(typed) == 0 {
			return obsEmptyCollection
		}
		return obsValue
	default:
		return obsValue
	}
}

// schemaHints returns the Schema.Hints map of an evaluated or inventoried
// resource, or nil when the resource carries no schema.
func schemaHints(resource map[string]any) map[string]any {
	schema, ok := resource["Schema"].(map[string]any)
	if !ok {
		return nil
	}
	hints, _ := schema["Hints"].(map[string]any)
	return hints
}

// propertiesOf returns a resource's Properties map, or nil when it has none.
func propertiesOf(resource map[string]any) map[string]any {
	props, _ := resource["Properties"].(map[string]any)
	return props
}
