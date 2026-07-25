// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"time"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// targetHealthObservation classifies a terminal plugin progress result into a
// health observation for the target that served the operation. It returns
// (observation, true) only for outcomes that carry a health signal:
//
//   - Terminal success → reachable (last_seen_at refreshed).
//   - Terminal failure with NetworkFailure or ServiceTimeout error code → unreachable.
//
// All other outcomes (NotFound, other error codes, non-terminal) return the
// zero value and false — no health observation should be emitted.
//
// now is taken as a parameter so callers can supply a deterministic timestamp
// in tests.
func targetHealthObservation(targetLabel string, tp *plugin.TrackedProgress, now time.Time) (pkgmodel.TargetHealthObservation, bool) {
	if tp.FinishedSuccessfully() {
		return pkgmodel.TargetHealthObservation{
			TargetLabel: targetLabel,
			State:       pkgmodel.TargetHealthStateReachable,
			ObservedAt:  now,
			LastSeenAt:  &now,
		}, true
	}

	if tp.Failed() {
		switch tp.ErrorCode {
		case resource.OperationErrorCodeNetworkFailure, resource.OperationErrorCodeServiceTimeout:
			return pkgmodel.TargetHealthObservation{
				TargetLabel:   targetLabel,
				State:         pkgmodel.TargetHealthStateUnreachable,
				ObservedAt:    now,
				LastErrorCode: string(tp.ErrorCode),
			}, true
		}
	}

	return pkgmodel.TargetHealthObservation{}, false
}
