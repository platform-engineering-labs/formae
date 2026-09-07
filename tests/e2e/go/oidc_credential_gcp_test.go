// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"os"
	"strings"
	"testing"
	"time"
)

// gcpProjectEnv names the project the GCP half of the oidc-credential chain
// federates into. It is its own project rather than the one the GCP plugin's
// suites use, because provx fixes the workload identity pool and provider ids
// per project and refuses to converge a provider that trusts a different
// issuer: a project already carrying the production-issuer connection cannot
// also carry this one.
const gcpProjectEnv = "E2E_GCP_PROJECT"

// gcpE2EProject returns the project, or skips.
//
// A skip rather than a failure because the credentials are conditional in the
// workflow, the way Azure's are: a fork without them should report that this
// did not run, not that it broke. The message names the variable, so a skip in
// a run that was supposed to have credentials is legible as the misconfigured
// job it is.
func gcpE2EProject(t *testing.T) string {
	t.Helper()

	project := os.Getenv(gcpProjectEnv)
	if project == "" {
		t.Skipf("%s is not set, so there is no GCP project to federate into", gcpProjectEnv)
	}
	return project
}

// connectProvisionsGcpTrust drives `formae connect gcp` against a stub control
// plane and returns the workload identity provider it provisioned.
//
// The pool and the provider are left standing: both are fixed per project and
// shared between installations, and provx's own Delete leaves them for that
// reason. The IAM bindings are not left standing. The subject is this run's,
// so they would accumulate one privileged principal per run, each holding
// roles/editor and roles/resourcemanager.projectIamAdmin on a project whose
// issuer signs with a standing key — a growing grant with no expiry and no
// owner.
func connectProvisionsGcpTrust(t *testing.T, bin, project string) string {
	t.Helper()

	stub := StartConnectStub(t, connectSetup{
		CloudSubject: oidcEchoSubject(),
		// GCP carries no role, but the setup read requires all three
		// coordinates: the control plane serves one document for every cloud.
		CloudRoleName: oidcConnectRoleName(),
		Issuer:        oidcEchoIssuer,
	})
	configPath := WriteHostedConnectConfig(t, t.TempDir(),
		stagedOidcPluginDir(t, oidcAuthPluginDirEnv), stub.URL)

	doc := RunConnect(t, bin, ConnectEnv(stub.URL, oidcEchoIssuer),
		"gcp",
		"--config", configPath,
		"--project", project,
		"--no-input",
	)

	if doc.Phase != "registered" {
		t.Fatalf("connect phase: got %q, want %q", doc.Phase, "registered")
	}
	if doc.Cloud != "gcp" {
		t.Errorf("connect cloud: got %q, want %q", doc.Cloud, "gcp")
	}
	if doc.Account != project {
		t.Errorf("connect account: got %q, want %q", doc.Account, project)
	}
	if doc.RoleArn != "" {
		t.Errorf("connect reported a roleArn %q on a GCP registration", doc.RoleArn)
	}
	if !strings.HasPrefix(doc.WorkloadIdentityProvider, "//iam.googleapis.com/projects/") {
		t.Fatalf("connect workloadIdentityProvider %q is not a provider resource name", doc.WorkloadIdentityProvider)
	}

	// Registered only once the provider name is known, since the principal is
	// derived from it. A run that fails between here and the end still revokes
	// what it granted.
	t.Cleanup(func() {
		RevokeGCPPrincipal(t, project, GCPPrincipalFor(t, doc.WorkloadIdentityProvider, oidcEchoSubject()))
	})

	registrations := stub.Registrations()
	if len(registrations) != 1 {
		t.Fatalf("stub control plane received %d registrations, want 1: %+v", len(registrations), registrations)
	}
	got := registrations[0]
	coordinate, present := got.Coordinate()
	if got.Cloud != "gcp" || got.Account != project || !present || coordinate != doc.WorkloadIdentityProvider {
		t.Errorf("registration: got cloud %q account %q workloadIdentityProvider %q (present %v), want cloud gcp, account %s, provider %s",
			got.Cloud, got.Account, coordinate, present, project, doc.WorkloadIdentityProvider)
	}
	// Absent, not merely empty: the control plane's schema is a discriminated
	// union that rejects a field belonging to another variant, so a roleArn
	// sent as "" would be refused just as one carrying a value would. The
	// decoder keeps the two apart rather than collapsing both to "".
	if got.ForeignCoordinatePresent() {
		t.Errorf("registration carried a roleArn field on a GCP connection")
	}

	return doc.WorkloadIdentityProvider
}

// TestOidcCredential_GcpTokenExchangesForRealCredentials is the GCP sibling of
// the AWS chain, and proves the same property against a different federation
// mechanism: `formae connect` provisions the workload identity pool, the
// provider trusting the broker's issuer, and the project bindings; then the
// broker mints a token for the provider's own resource name, Google's STS
// exchanges it for a federated access token, and that token reads the project.
//
// The project read is not decoration. A token exchange proves the provider
// trusts the issuer, subject and audience; only spending the result proves
// connect also granted the federated principal something, which is the half
// the exchange alone would leave unchecked.
func TestOidcCredential_GcpTokenExchangesForRealCredentials(t *testing.T) {
	bin := FormaeBinary(t)
	project := gcpE2EProject(t)

	provider := connectProvisionsGcpTrust(t, bin, project)

	agent := StartAgent(t, bin,
		WithPluginDir(stagedOidcPluginDir(t, oidcPluginDirEnv)),
		WithEnv(
			"E2E_OIDC_SUBJECT="+oidcEchoSubject(),
			// On GCP the audience is the provider's own resource name, and
			// Google pins the provider's allowed audiences to that same
			// string, so the token and the exchange cannot disagree.
			"E2E_OIDC_AUDIENCE="+provider,
			"E2E_OIDC_GCP_PROJECT="+project,
		),
	)
	agent.WaitForOidcBroker(t, oidcEchoNamespace, 60*time.Second)

	echo := applyOidcEchoFixture(t, bin, agent)

	if got := echoOutput(t, echo, "tokenError"); got != "" {
		t.Fatalf("tokenError: got %q, want empty", got)
	}

	header, claims := decodeJWT(t, echoOutput(t, echo, "token"))
	for _, check := range []struct {
		doc      map[string]any
		key      string
		expected string
	}{
		{header, "alg", "RS256"},
		{header, "kid", oidcEchoKeyID},
		{claims, "iss", oidcEchoIssuer},
		{claims, "sub", oidcEchoSubject()},
		{claims, "aud", provider},
	} {
		if got := jwtString(t, check.doc, check.key); got != check.expected {
			t.Errorf("token %s: got %q, want %q", check.key, got, check.expected)
		}
	}

	if got := echoOutput(t, echo, "exchangeError"); got != "" {
		t.Fatalf("exchangeError: got %q, want empty", got)
	}
	if got := echoOutput(t, echo, "exchangeIdentity"); !strings.HasPrefix(got, "projects/") {
		t.Errorf("exchangeIdentity %q does not name the project the credentials read", got)
	}
	requireCredentialsOutlive(t, echoOutput(t, echo, "exchangeExpiration"))

	// As on AWS: the probe proved the token is accepted and the credential it
	// buys can read the project, and this proves the real plugin can manage
	// the project with it. It runs afterwards because the probe is also the
	// readiness gate, absorbing the delay while the bindings connect just
	// wrote propagate.
	requireRealPluginManagesAResource(t, bin, agent,
		oidcRealGCPFormaFor(t, project, provider), oidcRealGCPResourceLabel)
}
