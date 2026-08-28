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
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// The hosted half of a `formae connect` run, stubbed.
//
// Connect cannot reach a cloud account without one. Every path opens a session
// against the control plane before it touches AWS: it resolves a hosted
// profile, drives that profile's auth plugin for a bearer, reads the subject,
// role name and issuer it must provision for, and reports the role it made
// back. This file stands up that control plane, the profile that addresses it,
// and the environment that points the CLI at both. Everything on the AWS side
// of the run — the caller check, the OIDC provider, the role, its trust policy
// — is real.

// oidcAuthStubBearer is the credential the staged auth plugin hands back.
// Its source of truth is tests/e2e/go/fixtures/oidc-auth-stub; the stub
// control plane compares against it, so a request that did not come through
// the auth plugin is refused rather than served.
const oidcAuthStubBearer = "Bearer e2e-oidc-connect-token"

// connectInstallationID is the installation a connect run addresses. The
// profile loader requires 27 base62 characters. Nothing resolves it: the stub
// answers for whichever installation the run names.
const connectInstallationID = "2eOidcConnectE2eInstall0001"

// oidcAuthPluginDirEnv names the plugin tree holding the stub auth plugin,
// staged by `make test-e2e`.
const oidcAuthPluginDirEnv = "E2E_OIDC_AUTH_PLUGIN_DIR"

// connectSetup is the coordinates the control plane produces and connect
// provisions against, verbatim: they travel from this struct into the role's
// name and its trust policy without connect inventing anything of its own.
type connectSetup struct {
	CloudSubject  string `json:"cloudSubject"`
	CloudRoleName string `json:"cloudRoleName"`
	Issuer        string `json:"issuer"`
}

// stubRegistration is one registration the stub control plane received. The
// coordinate fields mirror the CLI's own registration body, so a test can
// assert on what actually crossed the wire.
type stubRegistration struct {
	Cloud                    string `json:"cloud"`
	Account                  string `json:"account"`
	RoleArn                  string `json:"roleArn"`
	WorkloadIdentityProvider string `json:"workloadIdentityProvider"`
}

// connectStub is a running stub control plane.
type connectStub struct {
	URL string

	mu            sync.Mutex
	registrations []stubRegistration
}

// Registrations returns what the stub received, as a copy: a test reads it
// after the run rather than racing the handler for it.
func (s *connectStub) Registrations() []stubRegistration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]stubRegistration(nil), s.registrations...)
}

// StartConnectStub serves the two endpoints a connect run calls, on loopback
// http — which the CLI's origin rule admits for exactly this reason. Only the
// endpoints a successful run reaches are served: anything else 404s, which
// surfaces an unexpected call as a failure instead of a plausible answer.
func StartConnectStub(t *testing.T, setup connectSetup) *connectStub {
	t.Helper()

	stub := &connectStub{}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/installations/{installation}/cloud-connection-setup",
		func(w http.ResponseWriter, r *http.Request) {
			if !stubAuthorized(t, w, r) {
				return
			}
			writeStubJSON(t, w, http.StatusOK, setup)
		})

	mux.HandleFunc("POST /api/v1/installations/{installation}/cloud-connections",
		func(w http.ResponseWriter, r *http.Request) {
			if !stubAuthorized(t, w, r) {
				return
			}
			var registration stubRegistration
			if err := json.NewDecoder(r.Body).Decode(&registration); err != nil {
				t.Errorf("stub control plane: registration body is not JSON: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			stub.mu.Lock()
			stub.registrations = append(stub.registrations, registration)
			stub.mu.Unlock()
			// The created row is the registration echoed back, which the CLI
			// does not parse; the status is the whole of the answer.
			writeStubJSON(t, w, http.StatusCreated, registration)
		})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	stub.URL = server.URL
	return stub
}

// stubAuthorized refuses anything that did not arrive with the credential the
// auth plugin mints. Sending 401 rather than failing the test outright keeps
// the CLI's own refusal the thing under test.
func stubAuthorized(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()

	if r.Header.Get("Authorization") != oidcAuthStubBearer {
		writeStubJSON(t, w, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{"code": "unauthorized"},
		})
		return false
	}
	return true
}

func writeStubJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("stub control plane: writing the response: %v", err)
	}
}

// hostedConnectConfig is the profile a connect run is pointed at with
// --config. It is the shape `formae login` writes, plus a pluginDir so the
// staged auth plugin is the one resolved: the CLI matches an auth plugin by
// the type the auth block names, and the connect gate admits only "oidc".
//
// endpoint addresses the installation's agent and is never dialled by a
// connect run, but it has to parse as an https origin, so it names one that
// cannot resolve rather than one that could.
const hostedConnectConfig = `/*
 * Auto-generated e2e test configuration
 */

amends "formae:/Config.pkl"

pluginDir = %q

cli {
    connection = new Hosted {
        endpoint = "https://e2e-oidc-connect.invalid"
        installation = %q
        auth = new Dynamic {
            type = "oidc"
            role = "cli"
            issuer = %q
            clientId = "formae-cli"
            scopes = "openid profile email offline_access"
        }
    }
}
`

// WriteHostedConnectConfig writes the profile and returns its path. issuer is
// the login issuer the profile's auth block names, which the gate requires to
// be the platform's own: the stub serves both, so it is the stub's origin.
func WriteHostedConnectConfig(t *testing.T, dir, authPluginDir, issuer string) string {
	t.Helper()

	path := filepath.Join(dir, "connect-config.pkl")
	content := fmt.Sprintf(hostedConnectConfig, authPluginDir, connectInstallationID, issuer)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write the connect config: %v", err)
	}
	return path
}

// ConnectEnv is the environment a connect run needs, on top of the test
// process's own.
//
// Two override pairs, and each half of each must be set with the other. The
// control-plane pair aims the bearer: FORMAE_CLOUD_URL is where the request
// goes and FORMAE_CLOUD_ISSUER is the issuer the profile's auth block must
// name to be allowed to produce one. The connect pair pins the AWS-side trust
// artifacts: FORMAE_CONNECT_ISSUER must equal the issuer the control plane
// names, or the run refuses it as untrusted, and FORMAE_CONNECT_TEMPLATE_BASE
// only rides along, since the direct-provision path fetches no template.
func ConnectEnv(controlPlane, connectIssuer string) []string {
	return append(os.Environ(),
		"FORMAE_CLOUD_URL="+controlPlane,
		"FORMAE_CLOUD_ISSUER="+controlPlane,
		"FORMAE_CONNECT_ISSUER="+connectIssuer,
		"FORMAE_CONNECT_TEMPLATE_BASE=https://formae-connect-templates.s3.us-east-1.amazonaws.com",
	)
}

// RegisteredDocument is the machine-protocol document a connect run emits when
// a registration happened.
// Each cloud reports its own trust coordinate and omits the others, so a
// reader takes Cloud first and then the field that cloud carries.
type RegisteredDocument struct {
	SchemaVersion            int      `json:"schemaVersion"`
	Phase                    string   `json:"phase"`
	Status                   string   `json:"status"`
	Cloud                    string   `json:"cloud"`
	Account                  string   `json:"account"`
	RoleArn                  string   `json:"roleArn"`
	WorkloadIdentityProvider string   `json:"workloadIdentityProvider"`
	Warnings                 []string `json:"warnings"`
}

// RunConnect runs the connect command with the given environment and returns
// the registration document it emitted. Machine output is the surface a test
// can assert on: it is a declared protocol rather than rendered prose.
func RunConnect(t *testing.T, bin string, env []string, args ...string) RegisteredDocument {
	t.Helper()

	full := append([]string{"connect"}, args...)
	full = append(full, "--output-consumer", "machine", "--output-schema", "json")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("formae connect failed: %v\nargs: %v\nstdout: %s\nstderr: %s",
			err, full, string(stdout), stderr.String())
	}

	var doc RegisteredDocument
	if err := json.Unmarshal(stdout, &doc); err != nil {
		t.Fatalf("failed to parse the connect document: %v\nstdout: %s", err, string(stdout))
	}
	return doc
}

// DeleteIAMRole removes a role connect provisioned, along with the permission
// posture it attached: IAM refuses to delete a role that still carries
// policies, so they go first. Every step tolerates an absent target, because
// cleanup also runs after a failure that got partway.
func DeleteIAMRole(t *testing.T, roleName string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-west-2"))
	if err != nil {
		t.Errorf("cleanup: failed to load AWS config: %v", err)
		return
	}
	client := iam.NewFromConfig(cfg)

	attached, err := client.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		if strings.Contains(err.Error(), "NoSuchEntity") {
			return
		}
		t.Errorf("cleanup: listing attached policies of %q: %v", roleName, err)
		return
	}
	for _, policy := range attached.AttachedPolicies {
		if _, err := client.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
			RoleName:  aws.String(roleName),
			PolicyArn: policy.PolicyArn,
		}); err != nil {
			t.Errorf("cleanup: detaching %s from %q: %v", aws.ToString(policy.PolicyArn), roleName, err)
		}
	}

	inline, err := client.ListRolePolicies(ctx, &iam.ListRolePoliciesInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		t.Errorf("cleanup: listing inline policies of %q: %v", roleName, err)
		return
	}
	for _, name := range inline.PolicyNames {
		if _, err := client.DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{
			RoleName:   aws.String(roleName),
			PolicyName: aws.String(name),
		}); err != nil {
			t.Errorf("cleanup: deleting inline policy %s of %q: %v", name, roleName, err)
		}
	}

	if _, err := client.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(roleName)}); err != nil {
		t.Errorf("cleanup: deleting role %q: %v", roleName, err)
	}
}
