// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import (
	"os"
	"os/exec"
	"syscall"
)

var Sys = sys{}

type sys struct{}

func (sys) IsPrivilegedUser() bool {
	return os.Geteuid() == 0
}

func (sys) InvokeSelfWithSudo(args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}

	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return err
	}

	args = append([]string{" ", self}, args...)

	env := os.Environ()

	err = syscall.Exec(sudo, args, env)
	if err != nil {
		return err
	}

	return nil
}
