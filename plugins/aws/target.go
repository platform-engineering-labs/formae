// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"fmt"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/config"
)

var TargetBehavior = targetBehavior{}
var _ resource.TargetBehavior = targetBehavior{}

type targetBehavior struct{}

// UpdateResourceAllowed Call when an update attempts to change a resource's target
func (t targetBehavior) UpdateResourceAllowed(current *model.Target, desired *model.Target) error {
	currentCfg := config.FromTarget(current)
	desiredCfg := config.FromTarget(desired)

	if currentCfg.Region != desiredCfg.Region {
		return fmt.Errorf("current target region and desired target region must match")
	}

	return nil
}

// UpdateTargetAllowed Call when updating a target
func (t targetBehavior) UpdateTargetAllowed(current *model.Target, desired *model.Target) error {
	currentCfg := config.FromTarget(current)
	desiredCfg := config.FromTarget(desired)

	if currentCfg.Region != desiredCfg.Region {
		return fmt.Errorf("target region cannot be modified")
	}

	return nil
}
