// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package constants

const (
	UnmanagedStack = "$unmanaged"
)

// AllChannels lists every release channel formae publishes to. `formae
// refresh` warms the local package index for all of them so sudo-less
// reads (plugin search/info, update list) see fresh data on any channel.
var AllChannels = []string{"stable", "dev"}
