// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package command

import (
	"bytes"
	"encoding/json"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"strings"
	"testing"
)

func TestLogShowsRecordedInputsAndOptionalIdentity(t *testing.T) {
	var out bytes.Buffer
	entries := []apimodel.Command{{CommandID: "abc", Message: "Keep capacity", InputProperties: json.RawMessage(`{"replicas":{"value":4}}`), State: "Success"}}
	if err := renderCommandLog(&out, entries, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"abc", "Keep capacity", "replicas", "4", "Success"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "User:") {
		t.Fatal("unattributed command displayed a user")
	}
	entries[0].Subject = "stable-id"
	entries[0].SubjectName = "Sam"
	out.Reset()
	if err := renderCommandLog(&out, entries, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "User: Sam") {
		t.Fatal(out.String())
	}
}

func TestLogDoesNotInterpretTerminalControlCharacters(t *testing.T) {
	var out bytes.Buffer
	entries := []apimodel.Command{{CommandID: "abc", Message: "reason\x1b[2J", Subject: "id", SubjectName: "Sam\x1b[2J", State: "Failed"}}
	if err := renderCommandLog(&out, entries, false); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out.String(), '\x1b') {
		t.Fatalf("terminal escape leaked: %q", out.String())
	}
}

func TestLogExplicitEmptyAndClassifiedInputs(t *testing.T) {
	entries := []apimodel.Command{{CommandID: "missing"}, {CommandID: "empty", InputProperties: json.RawMessage(`{}`)}, {CommandID: "classified", InputProperties: json.RawMessage(`{"secret":{"redacted":true,"source":"supplied"},"replicas":{"value":9007199254740993,"source":"declaration"}}`), ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "accept", State: "Success"}, {Operation: "update", State: "Failed"}}}}
	var out bytes.Buffer
	if err := renderCommandLog(&out, entries, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Input properties: unavailable", "Input properties: none (explicit empty)", "redacted", "supplied", "declaration", "9007199254740993", "Drift acceptance records: 1; provider resource operations: 1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out.String())
		}
	}
}
