// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package pklrun is formae's evaluator engine on top of apple/pkl-go. It owns
// how formae runs pkl: which binary to invoke, loading a PklProject, and —
// crucially — running `pkl project resolve` automatically when a project's
// PklProject.deps.json is missing, so callers never have to resolve by hand.
//
// It is the single home for the project-aware evaluator that was previously
// copy-pasted across internal/schema/pkl, pkg/plugin/descriptors, and
// pkg/plugin/testutil. The package lives under pkg/plugin because that is the
// only module importable by both the root formae module and the plugin
// packages (the root module depends on pkg/plugin, never the reverse).
package pklrun

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/apple/pkl-go/pkl"
)

const (
	projectFile = "PklProject"
	depsFile    = "PklProject.deps.json"
)

// config holds the resolved options for NewProjectEvaluator.
type config struct {
	pklCmd   []string                      // nil/empty → resolve "pkl" via PATH
	evalOpts []func(*pkl.EvaluatorOptions) // forwarded to the pkl evaluator
}

// Option configures NewProjectEvaluator.
type Option func(*config)

// WithPklCommand overrides the pkl binary used for both `project resolve` and
// evaluation. The root formae module passes its bundled, sibling-of-formae
// binary (see BundledPklCommand); a nil or empty command falls back to PATH.
func WithPklCommand(cmd []string) Option {
	return func(c *config) { c.pklCmd = cmd }
}

// WithEvaluatorOptions appends pkl-go evaluator options (e.g.
// pkl.PreconfiguredOptions, output format, resource readers).
func WithEvaluatorOptions(o ...func(*pkl.EvaluatorOptions)) Option {
	return func(c *config) { c.evalOpts = append(c.evalOpts, o...) }
}

// NewProjectEvaluator builds a project-aware pkl evaluator for projectDir.
//
// If projectDir/PklProject.deps.json is missing, it first runs
// `pkl project resolve <projectDir>` to generate it — this removes the manual
// step that otherwise surfaces as a NoSuchFileException when loading the
// project. The returned cleanup function closes the underlying evaluator
// manager and must be called by the caller.
func NewProjectEvaluator(ctx context.Context, projectDir string, opts ...Option) (pkl.Evaluator, func(), error) {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}

	if err := ensureProjectResolved(projectDir, cfg.pklCmd); err != nil {
		return nil, nil, err
	}

	return newSafeProjectEvaluator(ctx, projectDir, cfg.pklCmd, cfg.evalOpts...)
}

// ProjectResolve unconditionally runs `pkl project resolve <projectDir>`,
// regenerating PklProject.deps.json. Use it after writing or changing a
// PklProject (e.g. ProjectInit, schema generators); for the lazy
// resolve-only-if-missing behavior used when loading a project for evaluation,
// prefer NewProjectEvaluator. WithPklCommand selects the binary; evaluator
// options are ignored.
func ProjectResolve(projectDir string, opts ...Option) error {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	return runPklProjectResolve(projectDir, cfg.pklCmd)
}

// ensureProjectResolved runs `pkl project resolve` when projectDir has a
// PklProject but no resolved PklProject.deps.json. When the deps file already
// exists it is a no-op, so resolution happens at most once per project dir.
func ensureProjectResolved(projectDir string, pklCmd []string) error {
	deps := filepath.Join(projectDir, depsFile)
	switch _, err := os.Stat(deps); {
	case err == nil:
		return nil // already resolved
	case os.IsNotExist(err):
		return runPklProjectResolve(projectDir, pklCmd)
	default:
		return fmt.Errorf("failed to stat %s: %w", deps, err)
	}
}

// runPklProjectResolve invokes `pkl project resolve <dir>`. Combined
// stdout/stderr is included in the returned error so failures (missing binary,
// malformed PklProject, network issues fetching remote deps) surface at the
// call site instead of being silently dropped.
func runPklProjectResolve(dir string, pklCmd []string) error {
	bin, pre := pklBinary(pklCmd)
	cmd := exec.Command(bin, append(append([]string{}, pre...), "project", "resolve", dir)...)
	if errors.Is(cmd.Err, exec.ErrDot) {
		cmd.Err = nil
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pkl project resolve failed in %s: %w\nOutput: %s", dir, err, string(output))
	}
	return nil
}

// pklBinary splits a pkl command override into its binary and leading args,
// defaulting to "pkl" on PATH when no override is given.
func pklBinary(pklCmd []string) (string, []string) {
	if len(pklCmd) > 0 {
		return pklCmd[0], pklCmd[1:]
	}
	return "pkl", nil
}

// newSafeProjectEvaluator creates a project-aware PKL evaluator without the race
// condition in pkl-go's NewProjectEvaluator. That function internally creates two
// evaluators on the same manager and defer-closes the first one. If the pkl subprocess
// sends a late message for the closed evaluator, the manager's listen loop exits
// entirely (calls return instead of continue), killing all message processing.
// See: https://github.com/apple/pkl-go/blob/v0.12.0/pkl/evaluator_exec.go#L57-L84
//
// This function keeps both evaluators alive until the returned cleanup function is
// called, which closes the entire manager.
func newSafeProjectEvaluator(ctx context.Context, projectDir string, pklCmd []string, opts ...func(*pkl.EvaluatorOptions)) (pkl.Evaluator, func(), error) {
	var manager pkl.EvaluatorManager
	if len(pklCmd) > 0 {
		manager = pkl.NewEvaluatorManagerWithCommand(pklCmd)
	} else {
		manager = pkl.NewEvaluatorManager()
	}

	projectEvaluator, err := manager.NewEvaluator(ctx, opts...)
	if err != nil {
		_ = manager.Close()
		return nil, nil, fmt.Errorf("failed to create project evaluator: %w", err)
	}

	projectPath := (&url.URL{Scheme: "file", Path: projectDir}).JoinPath(projectFile)
	project, err := pkl.LoadProjectFromEvaluator(ctx, projectEvaluator, &pkl.ModuleSource{Uri: projectPath})
	if err != nil {
		_ = manager.Close()
		return nil, nil, fmt.Errorf("failed to load project: %w", err)
	}

	newOpts := append([]func(*pkl.EvaluatorOptions){pkl.WithProject(project)}, opts...)
	evaluator, err := manager.NewEvaluator(ctx, newOpts...)
	if err != nil {
		_ = manager.Close()
		return nil, nil, fmt.Errorf("failed to create evaluator: %w", err)
	}

	return evaluator, func() { _ = manager.Close() }, nil
}

// BundledPklCommand returns the sibling pkl binary next to exePath, or nil to
// let callers fall back to PATH. Using PATH risks picking up a pkl version that
// doesn't support stdlib features formae schemas rely on (e.g.
// pkl.reflect.Property.allAnnotations needs 0.31+). Symlinks are resolved so
// /usr/local/bin/formae -> /opt/pel/bin/formae finds /opt/pel/bin/pkl.
func BundledPklCommand(exePath string) []string {
	if exePath == "" {
		return nil
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	bundled := filepath.Join(filepath.Dir(exePath), "pkl")
	if info, err := os.Stat(bundled); err == nil && !info.IsDir() {
		return []string{bundled}
	}
	return nil
}
