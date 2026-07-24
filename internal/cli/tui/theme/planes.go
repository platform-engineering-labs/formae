// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

// Glyphs holds the semantic symbols a theme renders. Decorative separators and
// borders are intentionally not themeable (fixed chrome).
type Glyphs struct {
	OpCreate  string
	OpUpdate  string
	OpDelete  string
	OpReplace string
	OpDetach  string
	OpKeep    string

	StatusDone       string
	StatusFailed     string
	StatusPending    string
	StatusInProgress string
	StatusSkipped    string
	StatusCanceled   string

	AckDone string
	AckSkip string
	AckWarn string
	AckFail string

	TreeBranch   string
	TreeLast     string
	ExpandOpen   string
	ExpandClosed string
	CheckboxOn   string
	CheckboxOff  string
	Transition   string
}

// Progress describes the determinate progress bar.
type Progress struct {
	FillDone       string
	FillInProgress string
	FillPending    string
	Animation      string // "pulse" | "blink" | "static"
}

// Spinner describes the indeterminate activity spinner.
type Spinner struct {
	Frames      []string
	Preset      string // "dot" | "line" | "circle" | "braille"; Frames wins if both set
	IntervalMs  int
	StaticFrame string
}

// ConfirmationBar is a behavior toggle for the pre-execution summary bar.
type ConfirmationBar struct {
	Color string // "brand" | "severity"
}
