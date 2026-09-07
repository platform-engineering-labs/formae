// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package credential

// MinFormaeVersion is the minimum formae agent version compatible with this
// SDK. Raising it stops every published broker whose manifest floor is lower
// from loading, so it moves only for a wire-breaking protocol change.
const MinFormaeVersion = "0.89.0"

// SDKVersion is the version of this credential SDK package. It appears in
// compatibility diagnostics to tell broker authors what to upgrade to.
const SDKVersion = "0.1.0"
