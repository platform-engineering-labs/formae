// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

func TestPackagedHelmOCICredentialsTagsFetchOneBudget(t *testing.T) {
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, packagedKubeconfig(t))
	f.lifetime = time.Hour
	var preflight sync.Once
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/version" {
			preflight.Do(func() {
				select {
				case <-time.After(15 * time.Second):
				case <-r.Context().Done():
				}
			})
		}
		return false
	}
	tags, fetch := make(chan struct{}), make(chan struct{})
	var tagsOnce, fetchOnce sync.Once
	registry := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "user" || pass != "pass" {
			w.Header().Set("WWW-Authenticate", `Basic realm="packaged"`)
			w.WriteHeader(401)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/tags/list") {
			tagsOnce.Do(func() { close(tags) })
			select {
			case <-time.After(10 * time.Second):
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"name":"acceptance","tags":["0.1.0"]}`)
			return
		}
		if strings.Contains(r.URL.Path, "/manifests/") {
			fetchOnce.Do(func() { close(fetch) })
			<-r.Context().Done()
			return
		}
		w.WriteHeader(200)
	}))
	defer registry.Close()
	dir := t.TempDir()
	ca := filepath.Join(dir, "registry-ca.pem")
	require.NoError(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: registry.Certificate().Raw}), 0600))
	t.Setenv("SSL_CERT_FILE", ca)
	marker := filepath.Join(dir, "helper-called")
	script := "#!/bin/sh\nread server\nprintf called > '" + marker + "'\nsleep 5\nprintf '%s' '{\"Username\":\"user\",\"Secret\":\"pass\"}'\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-credential-packaged"), []byte(script), 0700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_CONFIG", dir)
	credentials := filepath.Join(dir, "registry.json")
	require.NoError(t, os.WriteFile(credentials, []byte(`{"credsStore":"packaged"}`), 0600))
	t.Setenv("HELM_REGISTRY_CONFIG", credentials)
	a := startPackagedAgent(t, stage, f, 30*time.Second)
	request := packagedChartRequest(t, f, "oci-budget-"+randomSuffix(), 300)
	var props map[string]any
	require.NoError(t, json.Unmarshal(request.Properties, &props))
	props["chart"] = "acceptance"
	props["repoURL"] = "oci://" + registry.Listener.Addr().String()
	request.Properties, _ = json.Marshal(props)
	start := time.Now()
	p := a.call(t, gen.PID{}, "Create", request).Value.(plugin.TrackedProgress)
	elapsed := time.Since(start)
	require.Equal(t, resource.OperationStatusFailure, p.OperationStatus, "%s", p.StatusMessage)
	require.FileExists(t, marker)
	select {
	case <-tags:
	default:
		t.Fatal("actual OCI Tags not reached")
	}
	select {
	case <-fetch:
	default:
		t.Fatal("actual OCI fetch not reached")
	}
	require.Greater(t, elapsed, 47*time.Second)
	require.Less(t, elapsed, 55*time.Second)
	assertPackagedReadOnly(t, f)
	t.Logf("version + credential helper + Tags + fetch shared %s callback", elapsed)
}
