// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin_process_supervisor

import "testing"

// Plugins do not agree on a log format. Ergo-based ones emit the runtime's
// bracketed format; anything built on log/slog emits logfmt with an uppercase
// level. Both name a level, and the supervisor has to read either to report a
// line at the severity its author chose.
func TestPluginLevel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		want   string
	}{
		{"ergo info", "1764458575429677244 [info] <79F4473F.0.1004>: hello", "info"},
		{"ergo error", "1764458575429677244 [error] <79F4473F.0.1004>: boom", "error"},
		{"ergo warning", "1764458575429677244 [warning] <79F4473F.0.1004>: hmm", "warning"},

		// The shape the oidc auth plugin actually emits.
		{
			"slog warn",
			`time=2026-08-25T20:52:51.539Z level=WARN msg="rejected credential" plugin=oidc`,
			"warning",
		},
		{"slog error", `time=2026-08-25T20:52:51.539Z level=ERROR msg="broker died"`, "error"},
		{"slog info", `time=2026-08-25T20:52:51.539Z level=INFO msg="started"`, "info"},
		{"slog debug", `time=2026-08-25T20:52:51.539Z level=DEBUG msg="dialing"`, "debug"},

		// A level is only a level when it is its own field.
		{"level inside a quoted message", `time=1 level=INFO msg="set level=ERROR on the thing"`, "info"},

		// Nothing recognisable: the caller decides what to do with that.
		{"unstructured", "something fell over", ""},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pluginLevel(tc.output); got != tc.want {
				t.Fatalf("pluginLevel(%q) = %q, want %q", tc.output, got, tc.want)
			}
		})
	}
}
