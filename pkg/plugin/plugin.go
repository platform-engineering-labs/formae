// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"github.com/masterminds/semver"
)

type Plugin interface {
	Name() string
	Type() Type
	Version() *semver.Version
}
