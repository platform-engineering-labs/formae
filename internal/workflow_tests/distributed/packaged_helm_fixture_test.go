// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/pkg/credential"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	plugindiscovery "github.com/platform-engineering-labs/formae/pkg/plugin/discovery"
	"github.com/stretchr/testify/require"
)

const packagedAudience = "urn:formae:kubernetes:39c24d1d-3815-4817-9242-4032be46601b"
const packagedOtherAudience = "urn:formae:kubernetes:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const packagedHelmType = "K8S::Helm::Release"

type apiObservation struct {
	At           time.Time
	Method, Path string
	Expired      bool
}
type packagedFixture struct {
	mu          sync.Mutex
	key         []byte
	mintTimes   []time.Time
	requests    []apiObservation
	lifetime    time.Duration
	repeatToken bool
	repeated    *credential.OidcIdentityTokenResult
	gate        func(http.ResponseWriter, *http.Request) bool
	mintGate    func(context.Context) bool
	api, broker *httptest.Server
}

// The proxy validates test-signed JWTs before forwarding with the task-owned
// kind client certificate. It proves plugin credential lifecycle, not real
// Kubernetes OIDC issuer/JWKS configuration (covered by the onboarding suite).
func newPackagedFixture(t *testing.T, kubeconfig string) *packagedFixture {
	t.Helper()
	out, err := exec.Command("kubectl", "--kubeconfig", kubeconfig, "config", "view", "--minify", "--raw", "-o", "json").Output()
	require.NoError(t, err)
	var cfg struct {
		Clusters []struct {
			Cluster struct {
				Server string
				CA     string `json:"certificate-authority-data"`
			}
		}
		Users []struct {
			User struct {
				Cert string `json:"client-certificate-data"`
				Key  string `json:"client-key-data"`
			}
		}
	}
	require.NoError(t, json.Unmarshal(out, &cfg))
	require.Len(t, cfg.Clusters, 1)
	require.Len(t, cfg.Users, 1)
	decode := func(s string) []byte { b, e := base64.StdEncoding.DecodeString(s); require.NoError(t, e); return b }
	cert, err := tls.X509KeyPair(decode(cfg.Users[0].User.Cert), decode(cfg.Users[0].User.Key))
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(decode(cfg.Clusters[0].Cluster.CA)))
	endpoint, err := url.Parse(cfg.Clusters[0].Cluster.Server)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(endpoint)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	proxy.Transport = tr
	f := &packagedFixture{key: []byte(util.RandomString(32)), lifetime: 16 * time.Second}
	f.broker = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req credential.OidcIdentityTokenRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || (req.Audience != packagedAudience && req.Audience != packagedOtherAudience) {
			http.Error(w, "denied", 403)
			return
		}
		f.mu.Lock()
		f.mintTimes = append(f.mintTimes, time.Now())
		gate := f.mintGate
		life := f.lifetime
		f.mu.Unlock()
		if gate != nil && !gate(r.Context()) {
			http.Error(w, "denied", 403)
			return
		}
		expiry := time.Now().Add(life)
		payload, _ := json.Marshal(map[string]any{"iss": "https://oidc.cloud.formae.ai", "sub": "packaged-test", "aud": req.Audience, "exp": float64(expiry.UnixNano()) / 1e9})
		unsigned := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
		mac := hmac.New(sha256.New, f.key)
		mac.Write([]byte(unsigned))
		token := unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		result := credential.OidcIdentityTokenResult{Token: token, ExpiresAt: expiry}
		f.mu.Lock()
		if f.repeatToken {
			if f.repeated == nil {
				copy := result
				f.repeated = &copy
			}
			result = *f.repeated
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}))
	f.api = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		parts := strings.Split(token, ".")
		valid, expired := false, false
		if len(parts) == 3 {
			mac := hmac.New(sha256.New, f.key)
			mac.Write([]byte(parts[0] + "." + parts[1]))
			sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
			payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
			var claims struct {
				Exp float64
				Aud string
			}
			if json.Unmarshal(payload, &claims) == nil {
				expired = float64(time.Now().UnixNano())/1e9 >= claims.Exp
				valid = hmac.Equal(sig, mac.Sum(nil)) && (claims.Aud == packagedAudience || claims.Aud == packagedOtherAudience) && !expired
			}
		}
		f.mu.Lock()
		f.requests = append(f.requests, apiObservation{time.Now(), r.Method, r.URL.Path, expired})
		gate := f.gate
		f.mu.Unlock()
		if !valid {
			http.Error(w, "unauthorized", 401)
			return
		}
		// Drain the request body before a barrier. Otherwise HTTP/1 cannot
		// observe peer cancellation while unread POST bytes remain, making a
		// canceled worker appear alive at the external fixture.
		if r.Body != nil {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return
			}
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		if gate != nil && gate(w, r) {
			return
		}
		r.Header.Del("Authorization")
		proxy.ServeHTTP(w, r)
	}))
	// Both local DNS aliases have authenticated TLS identities, allowing tests
	// to distinguish transport aliases from the live kube-system UID.
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	template := *seed.Certificate()
	template.DNSNames = append(template.DNSNames, "localhost", "localhost.localdomain")
	privateKey := seed.TLS.Certificates[0].PrivateKey
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, template.PublicKey, privateKey)
	require.NoError(t, err)
	seed.Close()
	f.api.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: privateKey}}, MinVersion: tls.VersionTLS12}
	f.api.StartTLS()
	t.Cleanup(func() { f.api.Close(); f.broker.Close(); tr.CloseIdleConnections() })
	return f
}
func (f *packagedFixture) target() json.RawMessage {
	ca := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.api.Certificate().Raw}))
	b, _ := json.Marshal(map[string]any{"Auth": map[string]any{"Type": "Oidc", "Endpoint": f.api.URL, "CertificateAuthority": ca, "Audience": packagedAudience}})
	return b
}
func (f *packagedFixture) counts() (int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	expired := 0
	for _, r := range f.requests {
		if r.Expired {
			expired++
		}
	}
	return len(f.mintTimes), len(f.requests), expired
}

func stagePackagedPlugins(t *testing.T) (string, string) {
	t.Helper()
	src := os.Getenv("FORMAE_PACKAGED_K8S_SOURCE")
	if src == "" {
		t.Skip("set FORMAE_PACKAGED_K8S_SOURCE to the reviewed Kubernetes source")
	}
	require.NotEqual(t, "0.0.0", formae.Version, "set test ldflags -X github.com/platform-engineering-labs/formae.Version to the tested agent schema version")
	root, err := filepath.Abs("../../..")
	require.NoError(t, err)
	stage := t.TempDir()
	for _, key := range []string{"HELM_CACHE_HOME", "HELM_CONFIG_HOME", "HELM_DATA_HOME"} {
		t.Setenv(key, filepath.Join(stage, key))
	}
	run := func(dir string, args ...string) []byte {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, e := cmd.CombinedOutput()
		require.NoError(t, e, "%s: %s", args[0], out)
		return out
	}
	version := strings.TrimSpace(string(run(src, "pkl", "eval", "-x", "version", "formae-plugin.pkl")))
	for _, p := range []struct{ name, version, dir, pkg string }{{"k8s", version, src, "."}, {"test-oidc-broker", "0.0.1", root, "./internal/workflow_tests/cmd/test-oidc-broker"}} {
		dest := filepath.Join(stage, p.name, "v"+p.version)
		require.NoError(t, os.MkdirAll(dest, 0755))
		if binary := os.Getenv("FORMAE_PACKAGED_K8S_BINARY"); p.name == "k8s" && binary != "" {
			run(root, "cp", binary, filepath.Join(dest, p.name))
		} else {
			run(p.dir, "go", "build", "-trimpath", "-o", filepath.Join(dest, p.name), p.pkg)
		}
		manifestDir := p.dir
		if p.name != "k8s" {
			manifestDir = filepath.Join(root, "internal/workflow_tests/cmd/test-oidc-broker")
		}
		run(root, "cp", filepath.Join(manifestDir, "formae-plugin.pkl"), dest)
		run(root, "cp", "-R", filepath.Join(manifestDir, "schema"), dest)
		t.Logf("%s artifact provenance:\n%s", p.name, run(root, "go", "version", "-m", filepath.Join(dest, p.name)))
		t.Logf("artifact SHA256: %s", run(root, "sha256sum", filepath.Join(dest, p.name)))
	}
	return stage, root
}

type packagedCall struct {
	Request   any
	PID       gen.PID
	Operation string
	Reply     chan packagedReply
}
type packagedReply struct {
	PID   gen.PID
	Value any
	Err   error
}
type packagedProgress struct {
	PID   gen.PID
	Value any
}
type packagedRequester struct {
	act.Actor
	updates chan packagedProgress
}

func (a *packagedRequester) HandleMessage(from gen.PID, msg any) error {
	if call, ok := msg.(packagedCall); ok {
		pid := call.PID
		if pid == (gen.PID{}) {
			v, err := a.CallWithTimeout(gen.ProcessID{Name: actornames.PluginCoordinator, Node: a.Node().Name()}, messages.SpawnPluginOperator{Namespace: "K8S", ResourceURI: "packaged-helm", Operation: call.Operation, OperationID: util.RandomString(20), RequestedBy: a.PID()}, 15)
			if err != nil {
				call.Reply <- packagedReply{Err: err}
				return nil
			}
			result := v.(messages.SpawnPluginOperatorResult)
			if result.Error != "" {
				call.Reply <- packagedReply{Err: fmt.Errorf("%s", result.Error)}
				return nil
			}
			pid = result.PID
		}
		if _, ok := call.Request.(plugin.ListResources); ok {
			err := a.Send(pid, call.Request)
			call.Reply <- packagedReply{PID: pid, Err: err}
			return nil
		}
		value, err := a.CallWithTimeout(pid, call.Request, 65)
		call.Reply <- packagedReply{PID: pid, Value: value, Err: err}
		return nil
	}
	a.updates <- packagedProgress{from, msg}
	return nil
}

type packagedAgent struct {
	m         *metastructure.Metastructure
	requester gen.PID
	updates   chan packagedProgress
	stopOnce  *sync.Once
}

func startPackagedAgent(t *testing.T, stage string, f *packagedFixture, poll time.Duration, configure ...func(*model.Config)) *packagedAgent {
	t.Helper()
	cfg := newNetworkedTestConfig(t)
	cfg.PluginDir = stage
	cfg.Agent.Retry.StatusCheckInterval = poll
	cfg.Agent.ResourcePlugins = []model.ResourcePluginUserConfig{{Type: "k8s", Enabled: true, Retry: &cfg.Agent.Retry, PluginConfig: json.RawMessage(`{"allowedAuthMethods":["Oidc","Kubeconfig"]}`)}}
	raw, _ := json.Marshal(map[string]string{"controlUrl": f.broker.URL})
	cfg.Agent.OidcCredentialPlugins = []model.OidcCredentialPluginUserConfig{{Type: "test-oidc-broker", Enabled: true, PluginConfig: raw}}
	for _, apply := range configure {
		apply(cfg)
	}
	resources := []plugin.ResourcePluginInfo{}
	for _, info := range plugindiscovery.DiscoverPlugins(stage, plugindiscovery.Resource) {
		resources = append(resources, info.ToResourcePluginInfo())
	}
	brokers := []plugin.OidcCredentialPluginInfo{}
	for _, info := range plugindiscovery.DiscoverPlugins(stage, plugindiscovery.OidcCredential) {
		brokers = append(brokers, info.ToOidcCredentialPluginInfo())
	}
	require.Len(t, resources, 1)
	require.NotEmpty(t, brokers)
	db, err := dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "packaged-test")
	require.NoError(t, err)
	logs := setupTestLogger()
	m, err := metastructure.NewMetastructureWithDataStoreAndContext(context.Background(), cfg, resources, brokers, db, "packaged-test")
	require.NoError(t, err)
	require.NoError(t, m.Start())
	stopOnce := &sync.Once{}
	t.Cleanup(func() { stopOnce.Do(func() { m.Stop(true) }); cleanupTestDatabase(t, cfg) })
	if cfg.Agent.OidcCredentialPlugins[0].Enabled {
		require.True(t, logs.WaitForLog("Oidc credential broker registered", 20*time.Second), "supervised broker must register")
	}
	require.Eventually(t, func() bool { ps, e := m.RegisteredPlugins(); return e == nil && len(ps) == 1 }, 20*time.Second, 100*time.Millisecond)
	a := &packagedAgent{m: m, updates: make(chan packagedProgress, 100), stopOnce: stopOnce}
	a.requester, err = m.Node.Spawn(func() gen.ProcessBehavior { return &packagedRequester{updates: a.updates} }, gen.ProcessOptions{})
	require.NoError(t, err)
	return a
}
func (a *packagedAgent) call(t *testing.T, pid gen.PID, op string, request any) packagedReply {
	t.Helper()
	reply := make(chan packagedReply, 1)
	require.NoError(t, a.m.Node.Send(a.requester, packagedCall{Request: request, PID: pid, Operation: op, Reply: reply}))
	select {
	case result := <-reply:
		require.NoError(t, result.Err)
		return result
	case <-time.After(70 * time.Second):
		t.Fatal("packaged callback did not return")
		return packagedReply{}
	}
}

func (a *packagedAgent) stop() { a.stopOnce.Do(func() { a.m.Stop(true) }) }

func runOwnedKubectl(kubeconfig string, args ...string) ([]byte, error) {
	return exec.Command("kubectl", append([]string{"--kubeconfig", kubeconfig}, args...)...).Output()
}
