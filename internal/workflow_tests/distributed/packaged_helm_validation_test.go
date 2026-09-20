// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

func TestPackagedHelmInvalidTimingBeforeNetwork(t *testing.T) {
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, packagedKubeconfig(t))
	a := startPackagedAgent(t, stage, f, 30*time.Second, func(cfg *model.Config) { cfg.Agent.Retry.StatusCheckInterval = -time.Second })
	request := packagedChartRequest(t, f, "invalid-timing-"+randomSuffix(), 300)
	p := a.call(t, gen.PID{}, "Create", request).Value.(plugin.TrackedProgress)
	require.Equal(t, resource.OperationStatusFailure, p.OperationStatus)
	require.Contains(t, p.StatusMessage, "trusted operation metadata")
	m, r, _ := f.counts()
	require.Zero(t, m)
	require.Zero(t, r)
}

func TestPackagedHelmStoredShortTimeout(t *testing.T) {
	for _, op := range []string{"Delete", "Status"} {
		t.Run(op, func(t *testing.T) {
			stage, _ := stagePackagedPlugins(t)
			f := newPackagedFixture(t, packagedKubeconfig(t))
			f.lifetime = time.Hour
			name := "stored-short-" + randomSuffix()
			labels := map[string]string{"name": name, "owner": "helm", "status": "pending-install", "version": "1", "formae.dev/timeout-seconds": "1", "formae.dev/managed": "true"}
			raw, _ := json.Marshal(map[string]any{"name": name, "namespace": "default", "version": 1, "info": map[string]string{"status": "pending-install"}, "labels": labels})
			secret := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": "sh.helm.release.v1." + name + ".v1", "namespace": "default", "labels": labels}, "data": map[string]string{"release": base64.StdEncoding.EncodeToString([]byte(base64.StdEncoding.EncodeToString(raw)))}}
			f.gate = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/secrets") {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "SecretList", "metadata": map[string]string{}, "items": []any{secret}})
					return true
				}
				return false
			}
			a := startPackagedAgent(t, stage, f, 30*time.Second)
			var request any = plugin.DeleteResource{Namespace: "K8S", ResourceType: packagedHelmType, NativeID: "default/" + name, TargetConfig: f.target()}
			if op == "Status" {
				request = packagedStatus(fmt.Sprintf("default/%s@1:install", name), f.target(), resource.OperationCreate)
			}
			p := a.call(t, gen.PID{}, op, request).Value.(plugin.TrackedProgress)
			require.Equal(t, resource.OperationStatusFailure, p.OperationStatus)
			require.Contains(t, p.StatusMessage, "timeoutSeconds")
			assertPackagedReadOnly(t, f)
			f.mu.Lock()
			defer f.mu.Unlock()
			storage := false
			for _, r := range f.requests {
				if strings.HasSuffix(r.Path, "/secrets") {
					storage = true
				}
			}
			require.True(t, storage, "stored timeout requires bounded read-only lookup")
		})
	}
}
