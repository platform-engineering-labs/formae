//go:build unit

package model

import (
	"testing"
)

func TestFrameEnvelope_RoundTrips(t *testing.T) {
	env := `{"$res":true,"$label":"kvs","$type":"AWS::CloudFront::KeyValueStore","$stack":"default","$property":"id","$visibility":"Clear"}`
	tmpl := "cf.kvs('" + FrameEnvelope(env) + "')"

	spans, err := ScanEmbedSpans(tmpl)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	if spans[0].EnvelopeJSON != env {
		t.Errorf("decoded envelope mismatch:\n got %q\nwant %q", spans[0].EnvelopeJSON, env)
	}
	if tmpl[spans[0].Start] != EmbedSentinelStart || tmpl[spans[0].End-1] != EmbedSentinelEnd {
		t.Errorf("span offsets not on sentinels")
	}
}

func TestScanEmbedSpans_TwoSpans(t *testing.T) {
	a := FrameEnvelope(`{"$res":true,"$label":"a"}`)
	b := FrameEnvelope(`{"$res":true,"$label":"b"}`)
	spans, err := ScanEmbedSpans("x" + a + "y" + b + "z")
	if err != nil || len(spans) != 2 {
		t.Fatalf("want 2 spans no err, got %d / %v", len(spans), err)
	}
}

func TestScanEmbedSpans_StraySentinelRejected(t *testing.T) {
	// A lone RS in user text, not part of a well-formed span.
	_, err := ScanEmbedSpans("legit\x1etext-no-close")
	if err == nil {
		t.Fatal("want error on stray sentinel, got nil")
	}
}

func TestScanEmbedSpans_NoSpans(t *testing.T) {
	spans, err := ScanEmbedSpans("const x = 1;")
	if err != nil || len(spans) != 0 {
		t.Fatalf("want 0 spans no err, got %d / %v", len(spans), err)
	}
}
