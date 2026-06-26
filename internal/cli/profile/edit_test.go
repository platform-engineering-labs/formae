// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package profile

import (
	"reflect"
	"testing"
)

func TestEditorCommand(t *testing.T) {
	cases := []struct {
		editor   string
		path     string
		wantName string
		wantArgs []string
	}{
		{
			editor:   "vi",
			path:     "/tmp/profile.pkl",
			wantName: "vi",
			wantArgs: []string{"/tmp/profile.pkl"},
		},
		{
			editor:   "code --wait",
			path:     "/tmp/profile.pkl",
			wantName: "code",
			wantArgs: []string{"--wait", "/tmp/profile.pkl"},
		},
		{
			editor:   "emacs -nw",
			path:     "/tmp/profile.pkl",
			wantName: "emacs",
			wantArgs: []string{"-nw", "/tmp/profile.pkl"},
		},
		{
			// Empty EDITOR falls back to vi.
			editor:   "",
			path:     "/tmp/profile.pkl",
			wantName: "vi",
			wantArgs: []string{"/tmp/profile.pkl"},
		},
		{
			// Whitespace-only EDITOR also falls back to vi.
			editor:   "   ",
			path:     "/tmp/profile.pkl",
			wantName: "vi",
			wantArgs: []string{"/tmp/profile.pkl"},
		},
	}

	for _, tc := range cases {
		name, args := editorCommand(tc.editor, tc.path)
		if name != tc.wantName {
			t.Errorf("editorCommand(%q, %q): name = %q, want %q", tc.editor, tc.path, name, tc.wantName)
		}
		if !reflect.DeepEqual(args, tc.wantArgs) {
			t.Errorf("editorCommand(%q, %q): args = %v, want %v", tc.editor, tc.path, args, tc.wantArgs)
		}
	}
}
