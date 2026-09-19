// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import "io"

// MarshalEDF/UnmarshalEDF implementations for all cross-node message types.
// These use encodeMsgpack/decodeMsgpack for MessagePack + zstd compression.
//
// MarshalEDF on VALUE receiver, UnmarshalEDF on POINTER receiver
// (required by Ergo's RegisterTypeOf).

func (r ReadResource) MarshalEDF(w io.Writer) error    { return encodeMsgpack(w, &r) }
func (r *ReadResource) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, r) }

func (c CreateResource) MarshalEDF(w io.Writer) error    { return encodeMsgpack(w, &c) }
func (c *CreateResource) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, c) }

func (u UpdateResource) MarshalEDF(w io.Writer) error    { return encodeMsgpack(w, &u) }
func (u *UpdateResource) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, u) }

func (d DeleteResource) MarshalEDF(w io.Writer) error    { return encodeMsgpack(w, &d) }
func (d *DeleteResource) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, d) }

func (m ListResources) MarshalEDF(w io.Writer) error    { return encodeMsgpack(w, &m) }
func (m *ListResources) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m Listing) MarshalEDF(w io.Writer) error    { return encodeMsgpack(w, &m) }
func (m *Listing) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (c PluginOperatorCheckStatus) MarshalEDF(w io.Writer) error    { return encodeMsgpack(w, &c) }
func (c *PluginOperatorCheckStatus) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, c) }

func (tp TrackedProgress) MarshalEDF(w io.Writer) error    { return encodeMsgpack(w, &tp) }
func (tp *TrackedProgress) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, tp) }

func (m PluginAnnouncement) MarshalEDF(w io.Writer) error    { return encodeMsgpack(w, &m) }
func (m *PluginAnnouncement) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m StartPluginOperation) MarshalEDF(w io.Writer) error    { return encodeMsgpack(w, &m) }
func (m *StartPluginOperation) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m PluginOperatorShutdown) MarshalEDF(w io.Writer) error    { return encodeMsgpack(w, &m) }
func (m *PluginOperatorShutdown) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (r ResumeWaitingForResource) MarshalEDF(w io.Writer) error    { return encodeMsgpack(w, &r) }
func (r *ResumeWaitingForResource) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, r) }

func (m PluginOperatorRetry) MarshalEDF(w io.Writer) error    { return encodeMsgpack(w, &m) }
func (m *PluginOperatorRetry) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }
