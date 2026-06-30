// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_process_supervisor

import "syscall"

// pluginSysProcAttr returns process attributes that kill the plugin subprocess
// if the agent dies unexpectedly (e.g. SIGKILL). Pdeathsig sends SIGKILL to the
// child when the parent's thread exits, so an abruptly-killed agent does not
// leave the plugin orphaned holding its Ergo node name — which would make a
// restarted agent fail to re-register the plugin with "resource is taken".
func pluginSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
