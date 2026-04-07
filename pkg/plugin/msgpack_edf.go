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

func (m ReadResource) MarshalEDF(w io.Writer) error  { return encodeMsgpack(w, &m) }
func (m *ReadResource) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m CreateResource) MarshalEDF(w io.Writer) error  { return encodeMsgpack(w, &m) }
func (m *CreateResource) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m UpdateResource) MarshalEDF(w io.Writer) error  { return encodeMsgpack(w, &m) }
func (m *UpdateResource) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m DeleteResource) MarshalEDF(w io.Writer) error  { return encodeMsgpack(w, &m) }
func (m *DeleteResource) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m ListResources) MarshalEDF(w io.Writer) error  { return encodeMsgpack(w, &m) }
func (m *ListResources) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m Listing) MarshalEDF(w io.Writer) error  { return encodeMsgpack(w, &m) }
func (m *Listing) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m PluginOperatorCheckStatus) MarshalEDF(w io.Writer) error  { return encodeMsgpack(w, &m) }
func (m *PluginOperatorCheckStatus) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m TrackedProgress) MarshalEDF(w io.Writer) error  { return encodeMsgpack(w, &m) }
func (m *TrackedProgress) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m PluginAnnouncement) MarshalEDF(w io.Writer) error  { return encodeMsgpack(w, &m) }
func (m *PluginAnnouncement) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m StartPluginOperation) MarshalEDF(w io.Writer) error  { return encodeMsgpack(w, &m) }
func (m *StartPluginOperation) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m PluginOperatorShutdown) MarshalEDF(w io.Writer) error  { return encodeMsgpack(w, &m) }
func (m *PluginOperatorShutdown) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m ResumeWaitingForResource) MarshalEDF(w io.Writer) error  { return encodeMsgpack(w, &m) }
func (m *ResumeWaitingForResource) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }

func (m PluginOperatorRetry) MarshalEDF(w io.Writer) error  { return encodeMsgpack(w, &m) }
func (m *PluginOperatorRetry) UnmarshalEDF(data []byte) error { return decodeMsgpack(data, m) }
