// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package messages

// PolicyAttached notifies the AutoReconciler that an auto-reconcile policy
// has been attached to a stack (either inline or standalone). This schedules
// reconciliations for that stack based on the policy interval.
type PolicyAttached struct {
	StackLabel      string
	IntervalSeconds int64
}

// PolicyRemoved notifies the AutoReconciler that an auto-reconcile policy
// has been detached from a stack. This cancels any scheduled reconciliations
// for that stack.
type PolicyRemoved struct {
	StackLabel string
}
