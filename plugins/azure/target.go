// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"fmt"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/plugins/azure/pkg/config"
)

type targetBehavior struct{}

func (t targetBehavior) UpdateResourceAllowed(current *model.Target, desired *model.Target) error {
	currentCfg := config.FromTarget(current)
	desiredCfg := config.FromTarget(desired)

	// Prevent subscription changes during resource updates
	if currentCfg.SubscriptionId != desiredCfg.SubscriptionId {
		return fmt.Errorf("target subscription cannot be modified")
	}

	return nil
}

func (t targetBehavior) UpdateTargetAllowed(current *model.Target, desired *model.Target) error {
	currentCfg := config.FromTarget(current)
	desiredCfg := config.FromTarget(desired)

	// Prevent subscription changes
	if currentCfg.SubscriptionId != desiredCfg.SubscriptionId {
		return fmt.Errorf("target subscription cannot be modified")
	}

	return nil
}
