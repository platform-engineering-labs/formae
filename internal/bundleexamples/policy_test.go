// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package bundleexamples

import (
	"reflect"
	"testing"
)

func TestPolicyClassification(t *testing.T) {
	for _, m := range []string{"aws", "azure", "gcp", "k8s"} {
		if PolicyFor(m) != ExamplesRequired {
			t.Fatalf("%q should be ExamplesRequired", m)
		}
	}
	if PolicyFor("auth-basic") != ExamplesNone {
		t.Fatal("auth-basic should be ExamplesNone")
	}
	if PolicyFor("something-new") != ExamplesUnknown {
		t.Fatal("an unclassified member should be ExamplesUnknown")
	}
}

func TestRequiredMembers(t *testing.T) {
	if got, want := RequiredMembers(), []string{"aws", "azure", "gcp", "k8s"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RequiredMembers() = %v, want %v", got, want)
	}
}

// CheckMembers fails closed when the standard bundle carries a member that no
// one has classified in the policy - a new member must be an explicit decision,
// never a silent skip.
func TestCheckMembersFailsOnUnknown(t *testing.T) {
	if err := CheckMembers([]string{"aws", "azure", "gcp", "k8s", "auth-basic"}); err != nil {
		t.Fatalf("all-classified members should pass: %v", err)
	}
	err := CheckMembers([]string{"aws", "brand-new"})
	if err == nil {
		t.Fatal("an unclassified member must fail closed")
	}
}

// MissingRequired is the regression guard: if a required member's examples go
// missing (not staged), the build must fail rather than ship an incomplete
// bundle.
func TestMissingRequiredCatchesDroppedExamples(t *testing.T) {
	all := map[string]bool{"aws": true, "azure": true, "gcp": true, "k8s": true}
	if got := MissingRequired(all); len(got) != 0 {
		t.Fatalf("nothing should be missing when all required present; got %v", got)
	}
	dropped := map[string]bool{"aws": true, "azure": true, "k8s": true} // gcp went missing
	if got := MissingRequired(dropped); !reflect.DeepEqual(got, []string{"gcp"}) {
		t.Fatalf("MissingRequired should catch dropped gcp; got %v", got)
	}
}
