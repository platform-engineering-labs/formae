// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
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

func TestPackagedHelmConcurrent401WaitersShareCallbackRefresh(t *testing.T) {
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, packagedKubeconfig(t))
	f.lifetime = time.Hour
	f.repeatToken = true
	entered := make(chan string, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var mu sync.Mutex
	attempts := map[string]int{}
	f.gate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/configmaps") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return true
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			var obj struct{ Metadata struct{ Name string } }
			if json.Unmarshal(body, &obj) != nil {
				return true
			}
			mu.Lock()
			attempts[obj.Metadata.Name]++
			n := attempts[obj.Metadata.Name]
			mu.Unlock()
			if n == 1 {
				entered <- obj.Metadata.Name
				select {
				case <-release:
				case <-r.Context().Done():
					return true
				}
				http.Error(w, "one injected 401 per concurrent object", 401)
				return true
			}
		}
		return false
	}
	a := startPackagedAgent(t, stage, f, 30*time.Second)
	request := packagedChartRequest(t, f, "coalesce-"+randomSuffix(), 300)
	var props map[string]any
	require.NoError(t, json.Unmarshal(request.Properties, &props))
	chart := props["chart"].(string)
	require.NoError(t, os.WriteFile(filepath.Join(chart, "templates", "second.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+request.Label+"-second\n"), 0600))
	first := a.call(t, gen.PID{}, "Create", request)
	p := first.Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusInProgress, p.OperationStatus, "%s", p.StatusMessage)
	require.NoError(t, a.m.Node.SendExit(first.PID, gen.TerminateReasonNormal))
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("both parallel Helm creates did not reach 401 barriers")
		}
	}
	releaseOnce.Do(func() { close(release) })
	assertNoPackagedMints(t, f, 2*time.Second)
	mu.Lock()
	require.Equal(t, 1, attempts[request.Label])
	require.Equal(t, 1, attempts[request.Label+"-second"])
	mu.Unlock()
	before, _, _ := f.counts()
	check := a.call(t, gen.PID{}, "Status", packagedStatus(p.RequestID, f.target(), resource.OperationCreate))
	cp := check.Value.(plugin.TrackedProgress)
	require.NotEqual(t, resource.OperationStatusFailure, cp.OperationStatus, "%s", cp.StatusMessage)
	require.NoError(t, a.m.Node.SendExit(check.PID, gen.TerminateReasonNormal))
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts[request.Label] == 2 && attempts[request.Label+"-second"] == 2
	}, 3*time.Second, 10*time.Millisecond)
	after, _, _ := f.counts()
	require.Equal(t, 1, after-before, "one shared refresh must unblock both401 waiters; UID/storage reads reuse callback credentials")
	t.Log("two concurrent 401 worker waiters remained idle without minting, then one matching callback refresh resumed both with the same JWT")
}
