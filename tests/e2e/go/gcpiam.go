// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2/google"
)

// Revoking what `formae connect gcp` granted.
//
// The provisioner deliberately leaves the pool and the provider standing —
// both are shared, and deleting them would revoke every other installation
// connected to the same project — and removes only the IAM bindings for its
// own principal. This does the same, because the alternative is worse than
// leaving one set behind: the subject is per-run, so without this every run
// would deposit another principal holding roles/editor and
// roles/resourcemanager.projectIamAdmin, permanently, on a project whose
// issuer's signing key is a standing fixture.

const (
	// gcpIamPolicyURL is the project-level IAM policy endpoint. v1 is what
	// provx uses for the same bindings.
	gcpIamPolicyURL = "https://cloudresourcemanager.googleapis.com/v1/projects/"

	// gcpPolicyVersion asks for the representation that can express
	// conditional bindings. The bindings here are unconditional, but a policy
	// read at a lower version silently drops any conditional binding it cannot
	// represent, and writing that back would delete somebody else's grant.
	gcpPolicyVersion = 3
)

// gcpPolicy is the project IAM policy, read and written whole. Unknown fields
// are preserved only insofar as they are not needed: the etag makes the
// read-modify-write safe, so a concurrent editor loses the race rather than
// having their change silently dropped.
type gcpPolicy struct {
	Version  int          `json:"version"`
	Etag     string       `json:"etag"`
	Bindings []gcpBinding `json:"bindings"`
}

type gcpBinding struct {
	Role      string          `json:"role"`
	Members   []string        `json:"members"`
	Condition json.RawMessage `json:"condition,omitempty"`
}

// RevokeGCPPrincipal removes every binding member equal to principal from the
// project's IAM policy, and fails the run if it removed nothing.
//
// The empty case is a failure rather than a no-op because of when this is
// called: only once connect has reported the provider it provisioned, by which
// point the binding it granted must exist. Treating "found nothing" as success
// would make a principal string that does not match what Google stores
// indistinguishable from a clean revocation — leaving privileged bindings
// standing under a green run, which is the exact failure this cleanup exists
// to prevent.
//
// A failure does fail the run, deliberately: bindings this run granted and
// could not take back are worth being told about. It records rather than
// aborts, so one failed step does not skip the rest.
func RevokeGCPPrincipal(t *testing.T, project, principal string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		t.Errorf("cleanup: no Google credentials to revoke %s with: %v", principal, err)
		return
	}

	// One retry: the read-modify-write races anything else editing the policy,
	// and the etag turns that race into a 409 rather than a lost update.
	for attempt := range 2 {
		policy, err := getGCPPolicy(ctx, client, project)
		if err != nil {
			t.Errorf("cleanup: reading the IAM policy of %s: %v", project, err)
			return
		}

		filtered, removed := withoutMember(policy.Bindings, principal)
		if !removed {
			t.Errorf("cleanup: %s held no bindings on %s to revoke; connect granted them, so either "+
				"something else removed them or this principal does not name what Google stored",
				principal, project)
			return
		}
		policy.Bindings = filtered
		policy.Version = gcpPolicyVersion

		err = setGCPPolicy(ctx, client, project, policy)
		if err == nil {
			return
		}
		if attempt == 1 || !strings.Contains(err.Error(), "HTTP 409") {
			t.Errorf("cleanup: revoking %s on %s: %v", principal, project, err)
			return
		}
	}
}

// withoutMember drops member from every binding, and drops any binding it
// empties: a binding with no members is not valid to write back.
func withoutMember(bindings []gcpBinding, member string) ([]gcpBinding, bool) {
	var kept []gcpBinding
	removed := false
	for _, b := range bindings {
		var members []string
		for _, m := range b.Members {
			if m == member {
				removed = true
				continue
			}
			members = append(members, m)
		}
		if len(members) == 0 {
			continue
		}
		b.Members = members
		kept = append(kept, b)
	}
	return kept, removed
}

func getGCPPolicy(ctx context.Context, client *http.Client, project string) (*gcpPolicy, error) {
	body, _ := json.Marshal(map[string]any{
		"options": map[string]any{"requestedPolicyVersion": gcpPolicyVersion},
	})
	data, err := gcpIamCall(ctx, client, gcpIamPolicyURL+project+":getIamPolicy", body)
	if err != nil {
		return nil, err
	}
	var policy gcpPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("parsing the IAM policy: %w", err)
	}
	return &policy, nil
}

func setGCPPolicy(ctx context.Context, client *http.Client, project string, policy *gcpPolicy) error {
	body, err := json.Marshal(map[string]any{"policy": policy})
	if err != nil {
		return err
	}
	_, err = gcpIamCall(ctx, client, gcpIamPolicyURL+project+":setIamPolicy", body)
	return err
}

func gcpIamCall(ctx context.Context, client *http.Client, url string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

// GCPPrincipalFor builds the IAM member string for a federated subject in the
// pool the given provider belongs to.
//
// It is derived from the provider resource name connect reported rather than
// assembled from parts the test guesses at, so a cleanup can only ever revoke
// the principal that this run's own registration named.
func GCPPrincipalFor(t *testing.T, providerName, subject string) string {
	t.Helper()

	// //iam.googleapis.com/projects/N/locations/global/workloadIdentityPools/P/providers/Q
	pool, _, found := strings.Cut(providerName, "/providers/")
	if !found {
		t.Fatalf("workload identity provider %q does not name a pool", providerName)
	}
	return "principal:" + pool + "/subject/" + subject
}
