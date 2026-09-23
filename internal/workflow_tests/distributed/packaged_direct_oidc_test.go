// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

const directSubject = "installation-fixture-exact-subject"
const directNodeImage = "kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5"

type directControl struct {
	Audience, Subject, SigningKey string
	Keys                          []string
	Expired, Denied               bool
}
type directFixture struct {
	dir, kubeconfig, controlURL string
	target                      json.RawMessage
}

func (f *directFixture) control(t *testing.T, c directControl) {
	t.Helper()
	b, e := json.Marshal(c)
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "next.json"), b, 0600))
	require.NoError(t, os.Rename(filepath.Join(f.dir, "next.json"), filepath.Join(f.dir, "control.json")))
}
func (f *directFixture) counts(t *testing.T) map[string]int {
	t.Helper()
	response, e := http.Get(f.controlURL + "/counts")
	require.NoError(t, e)
	defer response.Body.Close()
	var counts map[string]int
	require.NoError(t, json.NewDecoder(response.Body).Decode(&counts))
	return counts
}
func newDirectFixture(t *testing.T) *directFixture {
	t.Helper()
	if os.Getenv("FORMAE_TEST_DIRECT_OIDC") != "1" {
		t.Skip("set FORMAE_TEST_DIRECT_OIDC=1 to create an isolated Docker/kind OIDC fixture")
	}
	f := &directFixture{dir: t.TempDir()}
	f.kubeconfig = filepath.Join(f.dir, "kubeconfig")
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		out, e := cmd.CombinedOutput()
		require.NoError(t, e, "%s: %s", args[0], out)
		return out
	}
	write := func(name string, b []byte) { require.NoError(t, os.WriteFile(filepath.Join(f.dir, name), b, 0600)) }
	for _, name := range []string{"first", "rotated", "unknown"} {
		k, e := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, e)
		write(name+".pem", pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
	}
	tlsKey, e := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, e)
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated OIDC fixture"}, DNSNames: []string{"oidc.cloud.formae.ai"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &tlsKey.PublicKey, tlsKey)
	require.NoError(t, e)
	write("tls.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	write("tls.key", pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(tlsKey)}))
	f.control(t, directControl{Subject: directSubject, SigningKey: "first", Keys: []string{"first"}})
	root, e := filepath.Abs("../../..")
	require.NoError(t, e)
	build := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(f.dir, "issuer"), "./internal/workflow_tests/cmd/test-oidc-issuer")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, e := build.CombinedOutput()
	require.NoError(t, e, "%s", out)
	name := "formae-oidc-" + strings.ToLower(randomSuffix())
	network := name + "-network"
	issuer := name + "-issuer"
	run("docker", "network", "create", network)
	t.Cleanup(func() {
		out, e := exec.Command("docker", "network", "rm", network).CombinedOutput()
		if e != nil {
			t.Errorf("owned network cleanup: %s: %v", out, e)
		}
	})
	run("docker", "run", "--detach", "--name", issuer, "--network", network, "--network-alias", "oidc.cloud.formae.ai", "--publish", "127.0.0.1::8080", "--volume", f.dir+":/fixture", "--entrypoint", "/fixture/issuer", "debian:trixie-slim", "/fixture")
	t.Cleanup(func() {
		out, e := exec.Command("docker", "rm", "--force", issuer).CombinedOutput()
		if e != nil {
			t.Errorf("owned issuer cleanup: %s: %v", out, e)
		}
	})
	address := strings.TrimSpace(string(run("docker", "port", issuer, "8080/tcp")))
	f.controlURL = "http://" + address
	require.Eventually(t, func() bool {
		r, e := http.Get(f.controlURL + "/counts")
		if e != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == 200
	}, 10*time.Second, 100*time.Millisecond)
	write("audit-policy.yaml", []byte("apiVersion: audit.k8s.io/v1\nkind: Policy\nrules:\n- level: Metadata\n"))
	kindConfig := fmt.Sprintf(`kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraMounts:
  - hostPath: %s
    containerPath: /oidc-fixture
  kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    apiServer:
      extraArgs:
        oidc-issuer-url: https://oidc.cloud.formae.ai
        oidc-client-id: %s
        oidc-username-claim: sub
        oidc-username-prefix: "formae:"
        oidc-signing-algs: RS256
        oidc-ca-file: /oidc-fixture/tls.crt
        audit-policy-file: /oidc-fixture/audit-policy.yaml
        audit-log-path: /oidc-fixture/audit.log
      extraVolumes:
      - name: oidc-fixture
        hostPath: /oidc-fixture
        mountPath: /oidc-fixture
        readOnly: false
        pathType: Directory
`, f.dir, packagedAudience)
	write("kind.yaml", []byte(kindConfig))
	create := exec.Command("kind", "create", "cluster", "--name", name, "--image", directNodeImage, "--config", filepath.Join(f.dir, "kind.yaml"), "--kubeconfig", f.kubeconfig, "--wait", "120s")
	create.Env = append(os.Environ(), "KIND_EXPERIMENTAL_DOCKER_NETWORK="+network)
	t.Cleanup(func() {
		out, e := exec.Command("kind", "delete", "cluster", "--name", name).CombinedOutput()
		if e != nil {
			t.Errorf("owned cluster cleanup: %s: %v", out, e)
		}
	})
	out, e = create.CombinedOutput()
	require.NoError(t, e, "%s", out)
	t.Logf("isolated direct OIDC cluster: %s", out)
	var config struct {
		Clusters []struct {
			Cluster struct {
				Server string
				CA     string `json:"certificate-authority-data"`
			}
		}
	}
	require.NoError(t, json.Unmarshal(run("kubectl", "--kubeconfig", f.kubeconfig, "config", "view", "--minify", "--raw", "-o", "json"), &config))
	require.Len(t, config.Clusters, 1)
	f.target, e = json.Marshal(map[string]any{"Auth": map[string]string{"Type": "Oidc", "Endpoint": config.Clusters[0].Cluster.Server, "CertificateAuthority": config.Clusters[0].Cluster.CA, "Audience": packagedAudience}})
	require.NoError(t, e)
	// This subject can CRUD only ConfigMaps/Secrets in default, and GET only
	// kube-system for Helm physical identity. Ordinary Read/List need no UID grant.
	rbac := `apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: oidc-fixture, namespace: default}
rules:
- apiGroups: [""]
  resources: [configmaps, secrets]
  verbs: [get, list, watch, create, update, patch, delete]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: oidc-fixture, namespace: default}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: oidc-fixture}
subjects:
- {kind: User, apiGroup: rbac.authorization.k8s.io, name: "formae:` + directSubject + `"}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: oidc-fixture-helm-uid}
rules:
- apiGroups: [""]
  resources: [namespaces]
  resourceNames: [kube-system]
  verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: oidc-fixture-helm-uid}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: oidc-fixture-helm-uid}
subjects:
- {kind: User, apiGroup: rbac.authorization.k8s.io, name: "formae:` + directSubject + `"}
`
	write("rbac.yaml", []byte(rbac))
	run("kubectl", "--kubeconfig", f.kubeconfig, "apply", "-f", filepath.Join(f.dir, "rbac.yaml"))
	return f
}

func TestPackagedDirectOIDC(t *testing.T) {
	f := newDirectFixture(t)
	stage, _ := stagePackagedPlugins(t)
	// The HTTP controller supplies test tokens to the real supervised broker. No
	// proxy handles Kubernetes requests: the target is the kind API server itself.
	fixture := &packagedFixture{broker: &httptest.Server{URL: f.controlURL + "/mint"}}
	create := func(t *testing.T, a *packagedAgent, name string) plugin.TrackedProgress {
		t.Helper()
		props, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]string{"name": name, "namespace": "default"}, "data": map[string]string{"value": "original"}})
		return a.call(t, gen.PID{}, "Create", plugin.CreateResource{Namespace: "K8S", ResourceType: "K8S::Core::ConfigMap", Label: name, Properties: props, TargetConfig: f.target}).Value.(plugin.TrackedProgress)
	}
	for _, tc := range []struct {
		name    string
		control directControl
		success bool
		message string
	}{
		{"valid", directControl{Subject: directSubject, SigningKey: "first", Keys: []string{"first"}}, true, ""},
		{"wrong-audience", directControl{Audience: packagedOtherAudience, Subject: directSubject, SigningKey: "first", Keys: []string{"first"}}, false, ""},
		{"wrong-subject", directControl{Subject: "other-installation", SigningKey: "first", Keys: []string{"first"}}, false, "forbidden"},
		{"unknown-key", directControl{Subject: directSubject, SigningKey: "unknown", Keys: []string{"first"}}, false, ""},
		{"expired", directControl{Subject: directSubject, SigningKey: "first", Keys: []string{"first"}, Expired: true}, false, "authentication request failed"},
		{"broker-denied", directControl{Subject: directSubject, SigningKey: "first", Keys: []string{"first"}, Denied: true}, false, ""},
		{"rotated-key", directControl{Subject: directSubject, SigningKey: "rotated", Keys: []string{"first", "rotated"}}, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.control(t, tc.control)
			beforeAudit, _ := os.ReadFile(filepath.Join(f.dir, "audit.log"))
			a := startPackagedAgent(t, stage, fixture, 30*time.Second)
			result := create(t, a, "oidc-"+tc.name)
			if tc.success {
				require.Equal(t, resource.OperationStatusSuccess, result.OperationStatus, result.StatusMessage)
			} else {
				require.Equal(t, resource.OperationStatusFailure, result.OperationStatus, result.StatusMessage)
				if tc.message != "" {
					require.Contains(t, strings.ToLower(result.StatusMessage), tc.message)
				}
			}
			t.Logf("%s: status=%v message=%s", tc.name, result.OperationStatus, result.StatusMessage)
			if tc.name == "wrong-audience" || tc.name == "unknown-key" || tc.name == "wrong-subject" {
				code := 401
				username := ""
				if tc.name == "wrong-subject" {
					code = 403
					username = "formae:other-installation"
				}
				require.Eventually(t, func() bool {
					raw, e := os.ReadFile(filepath.Join(f.dir, "audit.log"))
					if e != nil || len(raw) < len(beforeAudit) {
						return false
					}
					for _, line := range bytes.Split(raw[len(beforeAudit):], []byte("\n")) {
						var event struct {
							RequestURI     string
							ResponseStatus struct{ Code int }
							User           struct{ Username string }
						}
						if json.Unmarshal(line, &event) == nil && strings.Contains(event.RequestURI, "configmaps/oidc-"+tc.name) && event.ResponseStatus.Code == code && (username == "" || event.User.Username == username) {
							return true
						}
					}
					return false
				}, 5*time.Second, 50*time.Millisecond)
			}
			if tc.name == "expired" {
				// The plugin rejects expired JWTs before transport. Separately
				// present the same issuer/broker vector to TokenReview to prove
				// the API server independently enforces expiry as well.
				response, e := http.Post(f.controlURL+"/mint", "application/json", strings.NewReader(`{"Audience":"`+packagedAudience+`"}`))
				require.NoError(t, e)
				var minted struct{ Token string }
				require.NoError(t, json.NewDecoder(response.Body).Decode(&minted))
				response.Body.Close()
				review, _ := json.Marshal(map[string]any{"apiVersion": "authentication.k8s.io/v1", "kind": "TokenReview", "spec": map[string]string{"token": minted.Token}})
				cmd := exec.Command("kubectl", "--kubeconfig", f.kubeconfig, "create", "-f", "-", "-o", "json")
				cmd.Stdin = bytes.NewReader(review)
				out, e := cmd.Output()
				require.NoError(t, e)
				var checked struct {
					Status struct {
						Authenticated bool
						Error         string
					}
				}
				require.NoError(t, json.Unmarshal(out, &checked))
				require.False(t, checked.Status.Authenticated)
				t.Log("expired JWT rejected before plugin transport and independently by API-server TokenReview")
			}
			a.stop()
		})
	}
	f.control(t, directControl{Subject: directSubject, SigningKey: "rotated", Keys: []string{"first", "rotated"}})
	t.Run("missing-broker", func(t *testing.T) {
		before := f.counts(t)["mints"]
		a := startPackagedAgent(t, stage, fixture, 30*time.Second, func(c *model.Config) { c.Agent.OidcCredentialPlugins[0].Enabled = false })
		result := create(t, a, "oidc-missing")
		require.Equal(t, resource.OperationStatusFailure, result.OperationStatus)
		require.Equal(t, before, f.counts(t)["mints"])
		a.stop()
	})
	t.Run("ordinary-crud-discovery-and-helm", func(t *testing.T) {
		a := startPackagedAgent(t, stage, fixture, 2*time.Second)
		read := a.call(t, gen.PID{}, "Read", plugin.ReadResource{Namespace: "K8S", ResourceType: "K8S::Core::ConfigMap", NativeID: "default/oidc-valid", TargetConfig: f.target, IsSync: true}).Value.(plugin.TrackedProgress)
		require.Equal(t, resource.OperationStatusSuccess, read.OperationStatus, read.StatusMessage)
		props := json.RawMessage(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"oidc-valid","namespace":"default"},"data":{"value":"updated"}}`)
		update := a.call(t, gen.PID{}, "Update", plugin.UpdateResource{Namespace: "K8S", ResourceType: "K8S::Core::ConfigMap", NativeID: "default/oidc-valid", Label: "oidc-valid", DesiredProperties: props, TargetConfig: f.target}).Value.(plugin.TrackedProgress)
		require.Equal(t, resource.OperationStatusSuccess, update.OperationStatus, update.StatusMessage)
		listingCall := a.call(t, gen.PID{}, "List", plugin.ListResources{Namespace: "K8S", ResourceType: "K8S::Core::ConfigMap", TargetConfig: f.target, ListParameters: map[string]plugin.ListParam{"namespace": {ListParam: "namespace", ListValue: "default"}}})
		listing := awaitPackagedListing(t, a, listingCall.PID, 10*time.Second)
		require.Empty(t, listing.Error)
		require.NotEmpty(t, listing.Resources)
		chart := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(chart, "templates"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(chart, "Chart.yaml"), []byte("apiVersion: v2\nname: direct-proof\nversion: 0.1.0\n"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(chart, "templates", "map.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: direct-helm-proof\n"), 0600))
		helmProps, _ := json.Marshal(map[string]any{"metadata": map[string]string{"name": "direct-helm-proof", "namespace": "default"}, "chart": chart, "timeoutSeconds": 300})
		helm := a.call(t, gen.PID{}, "Create", plugin.CreateResource{Namespace: "K8S", ResourceType: packagedHelmType, Label: "direct-helm-proof", Properties: helmProps, TargetConfig: f.target})
		hp := helm.Value.(plugin.TrackedProgress)
		require.Equal(t, resource.OperationStatusInProgress, hp.OperationStatus, hp.StatusMessage)
		awaitPackagedTerminalSuccess(t, a, helm.PID, 15*time.Second)
		del := a.call(t, gen.PID{}, "Delete", plugin.DeleteResource{Namespace: "K8S", ResourceType: "K8S::Core::ConfigMap", NativeID: "default/oidc-valid", TargetConfig: f.target}).Value.(plugin.TrackedProgress)
		require.Equal(t, resource.OperationStatusSuccess, del.OperationStatus, del.StatusMessage)
		// Remove only the extra physical identity permission. Ordinary reads
		// still work; Helm must fail before chart download or any mutation.
		_, e := runOwnedKubectl(f.kubeconfig, "delete", "clusterrolebinding", "oidc-fixture-helm-uid")
		require.NoError(t, e)
		read = a.call(t, gen.PID{}, "Read", plugin.ReadResource{Namespace: "K8S", ResourceType: "K8S::Core::ConfigMap", NativeID: "default/oidc-rotated-key", TargetConfig: f.target, IsSync: true}).Value.(plugin.TrackedProgress)
		require.Equal(t, resource.OperationStatusSuccess, read.OperationStatus, read.StatusMessage)
		noUIDProps := json.RawMessage(`{"metadata":{"name":"must-not-start","namespace":"default"},"chart":"https://unreachable.example.invalid/chart.tgz","timeoutSeconds":300}`)
		denied := a.call(t, gen.PID{}, "Create", plugin.CreateResource{Namespace: "K8S", ResourceType: packagedHelmType, Label: "must-not-start", Properties: noUIDProps, TargetConfig: f.target}).Value.(plugin.TrackedProgress)
		require.Equal(t, resource.OperationStatusFailure, denied.OperationStatus)
		require.Contains(t, strings.ToLower(denied.StatusMessage), "get namespace kube-system")
		t.Log("ordinary Read/Update/List/Delete, real Helm Create/status, and missing kube-system UID grant fail-closed passed")
		a.stop()
	})
	t.Run("hosted-policy", func(t *testing.T) {
		before := f.counts(t)["mints"]
		a := startPackagedAgent(t, stage, fixture, 30*time.Second, func(c *model.Config) {
			c.Agent.ResourcePlugins[0].PluginConfig = json.RawMessage(`{"allowedAuthMethods":["Oidc"]}`)
		})
		denied := a.call(t, gen.PID{}, "Create", plugin.CreateResource{Namespace: "K8S", ResourceType: "K8S::Core::ConfigMap", Label: "forbidden-hosted", Properties: json.RawMessage(`{"metadata":{"name":"forbidden","namespace":"default"}}`), TargetConfig: json.RawMessage(`{"Auth":{"Type":"Kubeconfig","Kubeconfig":"/must-not-read"}}`)}).Value.(plugin.TrackedProgress)
		require.Equal(t, resource.OperationStatusFailure, denied.OperationStatus)
		require.Contains(t, denied.StatusMessage, "not allowed")
		require.Equal(t, before, f.counts(t)["mints"])
		a.stop()
	})
	counts := f.counts(t)
	require.Positive(t, counts["discovery"])
	require.GreaterOrEqual(t, counts["jwks"], 2, "API server must fetch new signing kid")
	t.Logf("actual issuer observations: %v", counts)
	// API audit Metadata contains no bearer token. Assert real authn/authz boundaries.
	require.Eventually(t, func() bool {
		b, e := os.ReadFile(filepath.Join(f.dir, "audit.log"))
		return e == nil && bytes.Contains(b, []byte(`"code":403`)) && bytes.Contains(b, []byte(`"code":401`))
	}, 10*time.Second, 100*time.Millisecond)
	audit, e := os.ReadFile(filepath.Join(f.dir, "audit.log"))
	require.NoError(t, e)
	require.Contains(t, string(audit), `"username":"formae:`+directSubject+`"`)
	require.Contains(t, string(audit), `"username":"formae:other-installation"`)
}
