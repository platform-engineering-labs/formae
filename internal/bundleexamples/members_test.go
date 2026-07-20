// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package bundleexamples

import "testing"

func has(xs []string, w string) bool {
	for _, x := range xs {
		if x == w {
			return true
		}
	}
	return false
}

// TestMembersRealStandardShape uses the exact requirements block shape of
// formae-plugin-standard's Opkgfile at its latest stable tag.
func TestMembersRealStandardShape(t *testing.T) {
	std := `
amends "orbital:/opkg.pkl"

name        = "standard"
maintainer  = new { name = "platform" }
requirements = new Listing<requirement.Requirement> {
  new { name = "aws"        method = "depends" operator = "ANY" }
  new { name = "azure"      method = "depends" operator = "ANY" }
  new { name = "gcp"        method = "depends" operator = "ANY" }
  new {
    name = "k8s"
    method = "depends"
  }
  new { name = "auth-basic" method = "depends" operator = "ANY" }
}

metadata {
  ["display"] {
    ["kind"]     = "metapackage"
  }
}
`
	got := Members(std)
	for _, w := range []string{"aws", "azure", "gcp", "k8s", "auth-basic"} {
		if !has(got, w) {
			t.Fatalf("missing %q; got %v", w, got)
		}
	}
	if has(got, "standard") {
		t.Fatalf("must not include metapackage name 'standard'; got %v", got)
	}
	if has(got, "platform") {
		t.Fatalf("must not include out-of-block name 'platform'; got %v", got)
	}
	if has(got, "display") {
		t.Fatalf("must not include metadata keys; got %v", got)
	}
}

func TestMembersEmptyAndNoBlock(t *testing.T) {
	if len(Members("")) != 0 {
		t.Fatal("empty -> no members")
	}
	if len(Members(`name = "standard"`)) != 0 {
		t.Fatal("no requirements block -> no members")
	}
}
