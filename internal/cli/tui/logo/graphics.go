// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logo

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
)

// encodeKitty returns the Kitty APC graphics-protocol escape sequence for the
// composite banner image (propeller + wordmark) for the given theme and version.
// The PNG is base64-encoded and split into 4096-byte chunks within APC payloads
// per the Kitty protocol specification (a=T f=100 m=more).
// Returns the complete escape sequence as a string; does NOT write to stdout.
func encodeKitty(dark bool, version string) string {
	img, err := buildBannerImage(dark, version)
	if err != nil {
		return ""
	}

	var imgBuf bytes.Buffer
	if err := png.Encode(&imgBuf, img); err != nil {
		return ""
	}

	encoded := base64.StdEncoding.EncodeToString(imgBuf.Bytes())

	// Kitty graphics protocol: APC G <params>;<payload> ST
	// Split payload into chunks of 4096 for large images.
	const chunkSize = 4096

	var seq bytes.Buffer
	for i := 0; i < len(encoded); i += chunkSize {
		end := i + chunkSize
		if end > len(encoded) {
			end = len(encoded)
		}
		chunk := encoded[i:end]

		more := 1
		if end >= len(encoded) {
			more = 0
		}

		if i == 0 {
			// First chunk: a=T (transmit+display), f=100 (PNG format), m=more.
			// No c=/r= forcing — natural size preserves aspect ratio correctly.
			fmt.Fprintf(&seq, "\033_Ga=T,f=100,m=%d;%s\033\\", more, chunk)
		} else {
			// Continuation chunks
			fmt.Fprintf(&seq, "\033_Gm=%d;%s\033\\", more, chunk)
		}
	}

	return seq.String()
}

// encodeITerm2 returns the iTerm2 inline-image escape sequence for the composite
// banner image (propeller + wordmark) for the given theme and version.
// The entire PNG is base64-encoded and wrapped in a single atomic OSC 1337
// escape sequence — fragmented writes can cause terminals to misparse the
// sequence (prototype fix from commit 0e057261).
// Returns the complete escape sequence as a string; does NOT write to stdout.
func encodeITerm2(dark bool, version string) string {
	img, err := buildBannerImage(dark, version)
	if err != nil {
		return ""
	}

	var imgBuf bytes.Buffer
	if err := png.Encode(&imgBuf, img); err != nil {
		return ""
	}

	rawBytes := imgBuf.Bytes()
	encoded := base64.StdEncoding.EncodeToString(rawBytes)

	// Build the complete escape sequence in a buffer to ensure atomic write.
	// Fragmented writes can cause terminals to misparse the sequence.
	// inline=1 with natural size — no forced grid, no preserveAspectRatio=0.
	var seq bytes.Buffer
	fmt.Fprintf(&seq,
		"\033]1337;File=inline=1;size=%d:%s\a",
		len(rawBytes), encoded)

	return seq.String()
}
