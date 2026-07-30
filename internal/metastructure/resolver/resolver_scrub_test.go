// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resolver

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/logging"
)

// marshalWithLogging must never emit an opaque value's plaintext, even when the
// marshal it is logging about has failed.
func TestMarshalWithLogging_DoesNotLogOpaquePlaintextOnError(t *testing.T) {
	capture := logging.NewTestLogCaptureQuiet()
	slog.SetDefault(slog.New(slog.NewTextHandler(capture, &slog.HandlerOptions{Level: slog.LevelDebug})))

	pr := &propertyResolver{}
	// A resolved opaque ref envelope, plus a value json.Marshal cannot encode,
	// so the error branch that logs the value is exercised.
	value := map[string]any{
		"$ref":          "formae://secret",
		"$visibility":   "Opaque",
		"$value":        "super-secret",
		"unmarshalable": make(chan int),
	}

	if _, err := pr.marshalWithLogging(value, "reference resolution", "config.auth"); err == nil {
		t.Fatal("expected a marshal error to exercise the logging branch")
	}

	found := false
	for _, entry := range capture.GetEntries() {
		if strings.Contains(entry, "super-secret") {
			t.Fatalf("plaintext secret leaked into a log entry: %q", entry)
		}
		if strings.Contains(entry, "<redacted>") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected at least one log entry to contain %q, but none did", "<redacted>")
	}
}
