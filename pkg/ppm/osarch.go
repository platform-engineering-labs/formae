// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import "runtime"

var SupportedPlatforms = []OSArch{
	{Linux, X8664},
	{Linux, Arm64},
	{Darwin, Arm64},
}

type OS = string

const (
	Linux  OS = "linux"
	Darwin OS = "darwin"
)

type Arch = string

const (
	X8664 Arch = "x8664"
	Arm64 Arch = "arm64"
)

type OSArch struct {
	OS   OS
	Arch Arch
}

func (a *OSArch) String() string {
	return a.OS + "-" + a.Arch
}

func CurrentOsArch() *OSArch {
	oa := OSArch{}

	switch runtime.GOOS {
	case "darwin":
		oa.OS = Darwin
	case "linux":
		oa.OS = Linux
	}

	switch runtime.GOARCH {
	case "amd64":
		oa.Arch = X8664
	case "arm64":
		oa.Arch = Arm64
	}

	return &oa
}
