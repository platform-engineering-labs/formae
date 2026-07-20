// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package bundleexamples

import (
	"fmt"
	"sort"
	"strings"
)

// ExamplesPolicy is the checked-in expectation of whether a standard-bundle
// member ships examples. It is a deliberate contract, not an inference from
// whatever a fetch happened to yield: a required member that silently loses its
// examples, or a brand-new member nobody has classified, must fail the build
// rather than quietly ship an incomplete bundle.
type ExamplesPolicy int

const (
	// ExamplesUnknown means the member is absent from the policy (fail closed).
	ExamplesUnknown ExamplesPolicy = iota
	// ExamplesRequired means the member MUST yield staged examples.
	ExamplesRequired
	// ExamplesNone means the member is expected to ship no examples.
	ExamplesNone
)

// examplesPolicy classifies every current standard-bundle member. Adding a
// member to the bundle requires adding it here (or CheckMembers fails closed).
var examplesPolicy = map[string]ExamplesPolicy{
	"aws":        ExamplesRequired,
	"azure":      ExamplesRequired,
	"gcp":        ExamplesRequired,
	"k8s":        ExamplesRequired,
	"auth-basic": ExamplesNone,
}

// PolicyFor returns the examples policy for a standard-bundle member.
func PolicyFor(member string) ExamplesPolicy {
	if p, ok := examplesPolicy[member]; ok {
		return p
	}
	return ExamplesUnknown
}

// RequiredMembers returns, sorted, the members that MUST yield examples.
func RequiredMembers() []string {
	var out []string
	for m, p := range examplesPolicy {
		if p == ExamplesRequired {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

// CheckMembers validates the parsed standard-bundle members against the policy:
// every member must be classified. An unclassified (new/unknown) member fails
// closed so it is an explicit decision, never a silent skip.
func CheckMembers(members []string) error {
	var unknown []string
	for _, m := range members {
		if PolicyFor(m) == ExamplesUnknown {
			unknown = append(unknown, m)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("standard-bundle member(s) not in the examples policy: %s (classify them in internal/bundleexamples/policy.go)", strings.Join(unknown, ", "))
	}
	return nil
}

// MissingRequired returns, sorted, the required members not present in the given
// set (e.g. the members actually staged). A non-empty result means the build
// must fail: a required member's examples went missing.
func MissingRequired(present map[string]bool) []string {
	var out []string
	for _, m := range RequiredMembers() {
		if !present[m] {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}
