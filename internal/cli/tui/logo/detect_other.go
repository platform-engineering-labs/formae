// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build !unix

package logo

// queryKittyGraphicsTTY is unavailable off unix: the probe needs a controlling
// terminal it can poll. The banner falls back to braille.
func queryKittyGraphicsTTY() probeResult { return probeResult{} }
