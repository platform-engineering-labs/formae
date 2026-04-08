// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build !linux

package auth

import "os/exec"

// setSysProcAttr is a no-op on non-Linux platforms. Pdeathsig is
// Linux-specific; on macOS the subprocess is cleaned up via cmd.Wait
// and explicit Kill in Close().
func setSysProcAttr(cmd *exec.Cmd) {}
