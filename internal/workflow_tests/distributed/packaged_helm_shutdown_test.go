// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPackagedHelmShutdownDuringPreparation(t *testing.T) {
	for _, mode := range []string{"agent-stop", "SIGTERM-drain"} {
		t.Run(mode, func(t *testing.T) {
			stage, _ := stagePackagedPlugins(t)
			f := newPackagedFixture(t, packagedKubeconfig(t))
			f.lifetime = time.Hour
			request := packagedChartRequest(t, f, "shutdown-"+randomSuffix(), 300)
			var props map[string]any
			require.NoError(t, json.Unmarshal(request.Properties, &props))
			archive := packagedChartArchive(t, props["chart"].(string))
			chartRelease := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(chartRelease) })
			entered, canceled := make(chan struct{}), make(chan struct{})
			var once, cancelOnce sync.Once
			chart := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				once.Do(func() { close(entered) })
				select {
				case <-r.Context().Done():
					cancelOnce.Do(func() { close(canceled) })
				case <-chartRelease:
					w.Write(archive)
					cancelOnce.Do(func() { close(canceled) })
				}
			}))
			t.Cleanup(chart.Close)
			a := startPackagedAgent(t, stage, f, 30*time.Second)
			props["chart"] = chart.URL + "/chart.tgz"
			request.Properties, _ = json.Marshal(props)
			independentPackagedCall(t, a, "Create", request)
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("preparation did not reach chart request")
			}
			if mode == "agent-stop" {
				a.stop()
			} else {
				paths, err := filepath.Glob(filepath.Join(stage, "k8s", "*", "k8s"))
				require.NoError(t, err)
				require.Len(t, paths, 1)
				require.NoError(t, ownedPackagedPID(t, paths[0]).Signal(syscall.SIGTERM))
				// Helm's HTTP getter owns a remaining-budget timeout rather
				// than a cancelable request. Let the valid archive return
				// across actual signal/drain, then test the activation fence.
				time.Sleep(200 * time.Millisecond)
				releaseOnce.Do(func() { close(chartRelease) })
			}
			select {
			case <-canceled:
			case <-time.After(5 * time.Second):
				t.Fatal("stopping agent left chart preparation alive")
			}
			assertNoPackagedMints(t, f, 3*time.Second)
			assertPackagedReadOnly(t, f)
		})
	}
}

func packagedChartArchive(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"Chart.yaml", "templates/object.yaml"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "acceptance/" + name, Mode: 0600, Size: int64(len(body))}))
		_, err = tw.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}
