// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package messages

// PauseDiscovery is a synchronous call that pauses the Discovery actor.
// Discovery will stop starting new list operations, wait for outstanding
// operations to complete, and then respond. This ensures mutual exclusivity
// with other operations like user apply/destroy commands.
//
// The Discovery actor maintains a reference count of active pauses, so multiple
// callers can pause concurrently and Discovery will only resume when all have
// sent ResumeDiscovery.
type PauseDiscovery struct{}

// PauseDiscoveryResponse is the response to a PauseDiscovery call, sent after
// all outstanding list operations have completed.
type PauseDiscoveryResponse struct{}

// ResumeDiscovery notifies the Discovery actor that a pause request is complete
// and Discovery can resume if there are no other active pauses.
//
// This decrements the reference count. When the count reaches zero, Discovery
// will resume starting new list operations.
type ResumeDiscovery struct{}
