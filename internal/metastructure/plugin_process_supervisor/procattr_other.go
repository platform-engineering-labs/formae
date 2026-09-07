// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build !linux

package plugin_process_supervisor

import "syscall"

// pluginSysProcAttr is a no-op on non-Linux platforms. Pdeathsig is
// Linux-specific; elsewhere the plugin subprocess is cleaned up via the
// supervisor's explicit termination on shutdown.
func pluginSysProcAttr() *syscall.SysProcAttr {
	return nil
}
