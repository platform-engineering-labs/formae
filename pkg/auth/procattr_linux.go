// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package auth

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr ensures the plugin subprocess is killed if the parent dies
// unexpectedly (e.g., kill -9). Pdeathsig sends SIGKILL to the child when
// the parent's thread exits.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
