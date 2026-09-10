// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package datastore

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCommandAdmissionValidation(t *testing.T) {
	valid := CommandAdmission{PrincipalScope: "installation:user", IdempotencyKey: "retry", RequestDigest: strings.Repeat("a", 64), Receipt: json.RawMessage(`{"review":"one"}`), Guards: []RevisionGuard{{Key: "stack:z", Revision: 2}, {Key: "stack:a", Revision: 0}, {Key: "stack:z", Revision: 2}}}
	got, err := NormalizeCommandAdmission(valid)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Guards, []RevisionGuard{{Key: "stack:a", Revision: 0}, {Key: "stack:z", Revision: 2}}) {
		t.Fatal(got.Guards)
	}
	if valid.Guards[0].Key != "stack:z" {
		t.Fatal("mutated caller guards")
	}
	for _, name := range []string{"principal", "key", "digest", "receipt", "guards", "negative", "contradiction", "long", "nul"} {
		t.Run(name, func(t *testing.T) {
			bad := valid
			bad.Guards = append([]RevisionGuard(nil), valid.Guards...)
			switch name {
			case "principal":
				bad.PrincipalScope = ""
			case "key":
				bad.IdempotencyKey = ""
			case "digest":
				bad.RequestDigest = "bad"
			case "receipt":
				bad.Receipt = json.RawMessage(`null`)
			case "guards":
				bad.Guards = nil
			case "negative":
				bad.Guards[0].Revision = -1
			case "contradiction":
				bad.Guards[2].Revision = 3
			case "long":
				bad.IdempotencyKey = strings.Repeat("x", 201)
			case "nul":
				bad.Guards[0].Key = "stack:\x00"
			}
			if _, err := NormalizeCommandAdmission(bad); !errors.Is(err, ErrInvalidAdmission) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestCommandAdmissionReceiptBound(t *testing.T) {
	for _, size := range []int{48 * 1024, 48*1024 + 1} {
		a := CommandAdmission{PrincipalScope: "p", IdempotencyKey: "k", RequestDigest: strings.Repeat("a", 64), Guards: []RevisionGuard{{Key: "stack:a"}}, Receipt: json.RawMessage(`{"value":"` + strings.Repeat("x", size-len(`{"value":""}`)) + `"}`)}
		_, err := NormalizeCommandAdmission(a)
		if size == 48*1024 {
			if err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, ErrInvalidAdmission) {
			t.Fatalf("oversized receipt accepted: %v", err)
		}
	}
}

func TestCommandAdmissionSupportedScopeBudget(t *testing.T) {
	a := CommandAdmission{PrincipalScope: "p", IdempotencyKey: "k", RequestDigest: strings.Repeat("a", 64), Receipt: json.RawMessage(`{}`)}
	for i := 0; i < 20_000; i++ {
		for _, kind := range []string{"label", "stack", "resource", "target"} {
			a.Guards = append(a.Guards, RevisionGuard{Key: fmt.Sprintf("%s:%d", kind, i)})
		}
	}
	for i := 0; i < 5; i++ {
		a.Guards = append(a.Guards, RevisionGuard{Key: fmt.Sprintf("domain:%d", i)})
	}
	if _, err := NormalizeCommandAdmission(a); err != nil {
		t.Fatal(err)
	}
	// Repeated dependencies consume one unique scope, not multiple work slots.
	a.Guards = make([]RevisionGuard, MaxAdmissionGuards+1)
	for i := range a.Guards {
		a.Guards[i].Key = "one"
	}
	result, err := NormalizeCommandAdmission(a)
	if err != nil || len(result.Guards) != 1 {
		t.Fatalf("deduplication: %v, %d", err, len(result.Guards))
	}
	for i := range a.Guards {
		a.Guards[i].Key = fmt.Sprintf("unique:%d", i)
	}
	if _, err = NormalizeCommandAdmission(a); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("unbounded unique scopes: %v", err)
	}
}
