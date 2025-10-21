// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource

import "github.com/platform-engineering-labs/formae/pkg/model"

type TargetBehavior interface {
	UpdateResourceAllowed(current *model.Target, desired *model.Target) error
	UpdateTargetAllowed(current *model.Target, desired *model.Target) error
}
