// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/base64"
	"fmt"
	"strings"
)

const (
	EmbedSentinelStart = '\x1e' // U+001E RECORD SEPARATOR
	EmbedSentinelEnd   = '\x1f' // U+001F UNIT SEPARATOR
)

// EmbedSpan is a single RS<base64>US span located in a $template string.
// Start/End are byte offsets such that template[Start:End] is the whole framed
// span (including both sentinels).
type EmbedSpan struct {
	Start        int
	End          int
	EnvelopeJSON string
}

// FrameEnvelope wraps an envelope JSON string as RS + base64(json) + US.
func FrameEnvelope(envelopeJSON string) string {
	return string(EmbedSentinelStart) +
		base64.StdEncoding.EncodeToString([]byte(envelopeJSON)) +
		string(EmbedSentinelEnd)
}

// ScanEmbedSpans finds every well-formed RS<base64>US span. It returns an error
// if, after removing well-formed spans, any stray RS/US byte remains (a literal
// that itself carried a control char — a corruption we refuse to ship).
func ScanEmbedSpans(template string) ([]EmbedSpan, error) {
	var spans []EmbedSpan
	i := 0
	for {
		start := strings.IndexByte(template[i:], EmbedSentinelStart)
		if start < 0 {
			break
		}
		start += i
		end := strings.IndexByte(template[start+1:], EmbedSentinelEnd)
		if end < 0 {
			return nil, fmt.Errorf("embed: opening sentinel without closing sentinel at offset %d", start)
		}
		end += start + 1
		b64 := template[start+1 : end]
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("embed: span at offset %d is not valid base64: %w", start, err)
		}
		spans = append(spans, EmbedSpan{Start: start, End: end + 1, EnvelopeJSON: string(decoded)})
		i = end + 1
	}
	// Stray-sentinel guard: no RS/US may remain outside the spans we matched.
	if HasStrayEmbedSentinel(template) {
		return nil, fmt.Errorf("embed: stray RS/US byte in template literal (not part of a framed span)")
	}
	return spans, nil
}

// HasStrayEmbedSentinel reports whether removing every well-formed RS<base64>US
// span still leaves an RS or US byte behind.
func HasStrayEmbedSentinel(template string) bool {
	var b strings.Builder
	i := 0
	for {
		start := strings.IndexByte(template[i:], EmbedSentinelStart)
		if start < 0 {
			b.WriteString(template[i:])
			break
		}
		start += i
		end := strings.IndexByte(template[start+1:], EmbedSentinelEnd)
		if end < 0 {
			b.WriteString(template[i:])
			break
		}
		end += start + 1
		if _, err := base64.StdEncoding.DecodeString(template[start+1 : end]); err != nil {
			// Not a real span; treat the RS as stray by writing up to and incl it.
			b.WriteString(template[i : start+1])
			i = start + 1
			continue
		}
		b.WriteString(template[i:start]) // keep text before the span, drop the span
		i = end + 1
	}
	s := b.String()
	return strings.IndexByte(s, EmbedSentinelStart) >= 0 || strings.IndexByte(s, EmbedSentinelEnd) >= 0
}
