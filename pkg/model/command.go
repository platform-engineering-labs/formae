// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

type Command string

const (
	CommandApply   Command = "apply"
	CommandEval    Command = "eval"
	CommandDestroy Command = "destroy"
	CommandSync    Command = "sync"
)
