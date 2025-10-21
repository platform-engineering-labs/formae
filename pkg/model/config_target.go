// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

type ConfigTarget struct {
	Label     string `pkl:"Label"`
	Namespace string `pkl:"Namespace"`
	Config    any    `pkl:"Config,omitempty"`
}
