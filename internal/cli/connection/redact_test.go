//go:build unit

package connection

import (
	"strings"
	"testing"
)

// A test that fails because it found a credential must not print the credential
// while saying so, and neither must one that fails because it did not expect to
// find one at all.
func TestRedactCredentials_RemovesTheTokenAndKeepsTheRest(t *testing.T) {
	out := `{"profile":"prod","credential":"Bearer eyJhbGciOiJSUzI1NiJ9.payload.signature"}`

	got := redactCredentials(out)

	if strings.Contains(got, "eyJhbGciOiJSUzI1NiJ9") {
		t.Errorf("the token survived redaction: %s", got)
	}
	if !strings.Contains(got, `"profile":"prod"`) {
		t.Errorf("redaction removed the context a reader needs: %s", got)
	}
}

// Output with no credential in it is worth reading in full.
func TestRedactCredentials_LeavesOrdinaryOutputAlone(t *testing.T) {
	out := `{"schemaVersion":1,"code":"auth_failed"}`
	if got := redactCredentials(out); got != out {
		t.Errorf("ordinary output was altered: %s", got)
	}
}
