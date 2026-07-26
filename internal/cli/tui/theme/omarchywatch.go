// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
)

// ApplyThemeMsg is delivered when the OS Omarchy theme changed; carries the
// freshly-resolved theme for models to apply via ApplyTheme.
type ApplyThemeMsg struct{ Theme *Theme }

// OmarchyWatcher watches the Omarchy current-theme symlink and its parent dir
// for changes and re-resolves the "omarchy" theme on any event. It handles both
// the atomic symlink swap omarchy-theme-set performs (repointing
// ~/.config/omarchy/current/theme) and an in-place colors.toml edit.
type OmarchyWatcher struct {
	w    *fsnotify.Watcher
	warn func(string)
	// watchedTarget is the filepath.EvalSymlinks-resolved theme directory
	// currently under watch (empty if none resolved yet). Tracked so arm()
	// can Remove the stale watch when a theme swap repoints the symlink,
	// instead of leaking an inotify watch on the old target forever.
	watchedTarget string
}

// NewOmarchyWatcher starts watching ~/.config/omarchy/{current, current/theme}.
// The watch is best-effort: paths that do not exist yet are skipped (a later
// create fires on the parent).
func NewOmarchyWatcher() (*OmarchyWatcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	ow := &OmarchyWatcher{w: fw, warn: func(string) {}}
	ow.arm()
	return ow, nil
}

// arm (re-)adds the watch paths. It keeps two stable, never-removed watches —
// the omarchy root and "current" (the directory that CONTAINS the "theme"
// symlink, so a rename of "theme" fires here) — which together catch the
// symlink swap itself. It also churns the watch on the resolved theme target
// (needed to catch in-place colors.toml edits): on every call it re-resolves
// the "theme" symlink and, if the target changed, Removes the watch on the
// previous target before Adding the new one. Without this, a theme swap would
// leave a stale inotify watch on the old target — leaking watch descriptors
// across swaps and firing a spurious ApplyThemeMsg if the old (no-longer-
// current) theme's colors.toml is later edited.
func (o *OmarchyWatcher) arm() {
	cfgRoot := omarchyThemeDir()     // .../omarchy/current/theme
	current := filepath.Dir(cfgRoot) // .../omarchy/current
	parent := filepath.Dir(current)  // .../omarchy
	_ = o.w.Add(parent)              // ignore errors: not-yet-existing paths fire via parent
	_ = o.w.Add(current)

	target, err := filepath.EvalSymlinks(cfgRoot)
	if err != nil {
		// No install yet, or a dangling symlink: leave watchedTarget as-is
		// (best-effort; a later create fires on "current").
		return
	}
	if target == o.watchedTarget {
		return
	}
	if o.watchedTarget != "" {
		_ = o.w.Remove(o.watchedTarget) // ignore error: watch may already be gone
	}
	_ = o.w.Add(target)
	o.watchedTarget = target
}

// WaitCmd blocks for the next relevant filesystem event, re-resolves the
// omarchy theme from scratch (following the symlink afresh), re-arms the watch,
// and returns an ApplyThemeMsg. Returns nil (no message) if the watcher closed.
func (o *OmarchyWatcher) WaitCmd() tea.Cmd {
	return func() tea.Msg {
		for {
			select {
			case _, ok := <-o.w.Events:
				if !ok {
					return nil
				}
				o.arm() // re-add in case the symlink target changed
				// Use the in-package resolver directly (not Resolve, which
				// warns to stderr): a live re-resolve happens while an
				// alt-screen TUI may be open, and any stderr write would
				// corrupt the render. Route warnings through o.warn instead,
				// which defaults to a no-op (see NewOmarchyWatcher).
				return ApplyThemeMsg{Theme: resolveOmarchy(omarchyThemeDir(), o.warn)}
			case _, ok := <-o.w.Errors:
				if !ok {
					return nil
				}
				// Transient watch error: keep waiting.
			}
		}
	}
}

// Close stops the watcher.
func (o *OmarchyWatcher) Close() error { return o.w.Close() }
