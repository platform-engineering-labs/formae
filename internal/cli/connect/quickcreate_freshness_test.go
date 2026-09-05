// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build releasecheck

package connect

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"testing"
)

// The pinned coordinates are immutable by design: Object Lock means the
// version this fetches can never change under us. The cost of that is
// silence in the other direction. Publishing a new template does not move
// the pin, so the repo can hold a template the CLI will never serve, and
// nothing about it looks wrong: connect succeeds, and the artifacts are
// simply built from the older template.
//
// So the gate checks two things against the live bucket. The pinned version
// must still fetch and still hash to what is recorded, which catches a
// tampered or vanished object; and the pinned version must be the current
// one for its key, which catches a publication the pin never picked up.
//
// Anonymous reads are enough: the bucket policy grants public GetObject and
// GetObjectVersion on exactly these keys, and a GET without a versionId
// returns the current version and names it in x-amz-version-id.
func TestPinnedTemplateIsTheCurrentPublication(t *testing.T) {
	pinnedURL := defaultTemplateBase + "/" + roleTemplateKey + "?versionId=" + roleTemplateVersionID

	body, _, err := fetch(pinnedURL)
	if err != nil {
		t.Fatalf("the pinned template version does not fetch: %v", err)
	}
	if got := sha256hex(body); got != roleTemplateSHA256 {
		t.Errorf("pinned template digest = %s, recorded %s; the recorded digest no longer describes what the pin serves",
			got, roleTemplateSHA256)
	}

	_, currentVersion, err := fetch(defaultTemplateBase + "/" + roleTemplateKey)
	if err != nil {
		t.Fatalf("the template key does not fetch: %v", err)
	}
	if currentVersion != "" && currentVersion != roleTemplateVersionID {
		t.Errorf("pin is stale: %s is published as version %s but the CLI serves %s. "+
			"A template change has been published that customers will never receive; "+
			"re-pin roleTemplateVersionID and roleTemplateSHA256 from the current publication.",
			roleTemplateKey, currentVersion, roleTemplateVersionID)
	}
}

func fetch(url string) (body []byte, versionID string, err error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", &statusError{url: url, code: resp.StatusCode}
	}
	b, err := io.ReadAll(resp.Body)
	return b, resp.Header.Get("x-amz-version-id"), err
}

type statusError struct {
	url  string
	code int
}

func (e *statusError) Error() string {
	return http.StatusText(e.code) + " fetching " + e.url
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
