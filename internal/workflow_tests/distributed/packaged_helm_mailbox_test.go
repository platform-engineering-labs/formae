// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_distributed

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

// Independent operators share one credential actor mailbox. A queued request
// must not gain a fresh mint allowance after its synchronous caller has left.
func TestPackagedHelmBrokerMailboxCannotOutliveCallbacks(t *testing.T) {
	stage, _ := stagePackagedPlugins(t)
	f := newPackagedFixture(t, packagedKubeconfig(t))
	f.lifetime = time.Hour
	a := startPackagedAgent(t, stage, f, 30*time.Second)
	// Separate endpoint aliases give each owning callback an independent cache
	// while sharing the same broker mailbox. A version override avoids a failed
	// discovery probe masking the UID call under test.
	request := func(label, host string) plugin.CreateResource {
		req := packagedChartRequest(t, f, label+randomSuffix(), 300)
		var target map[string]any
		require.NoError(t, json.Unmarshal(f.target(), &target))
		target["KubernetesVersion"] = "1.36"
		auth := target["Auth"].(map[string]any)
		auth["Endpoint"] = strings.Replace(auth["Endpoint"].(string), "127.0.0.1", host, 1)
		req.TargetConfig, _ = json.Marshal(target)
		return req
	}
	beforeMints, _, _ := f.counts()
	entered := make(chan time.Time, 4)
	stopped := make(chan time.Time, 4)
	f.mu.Lock()
	f.mintGate = func(ctx context.Context) bool {
		entered <- time.Now()
		<-ctx.Done()
		stopped <- time.Now()
		return false
	}
	f.mu.Unlock()
	start := time.Now()
	one := independentPackagedCall(t, a, "Create", request("mailbox-one-", "127.0.0.1"))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first broker mint did not reach external barrier")
	}
	two := independentPackagedCall(t, a, "Create", request("mailbox-two-", "localhost"))
	three := independentPackagedCall(t, a, "Create", request("mailbox-three-", "localhost.localdomain"))
	for _, replies := range []<-chan packagedReply{one, two, three} {
		reply := receivePackaged(t, replies, 15*time.Second)
		require.Equal(t, resource.OperationStatusFailure, reply.Value.(plugin.TrackedProgress).OperationStatus)
	}
	returned := time.Now()
	var cancellationTimes []time.Time
	for len(stopped) > 0 {
		cancellationTimes = append(cancellationTimes, <-stopped)
	}
	t.Logf("all three independent callbacks returned after %s", returned.Sub(start))
	// Observe beyond the first queued request's former fresh ten-second budget.
	// The broken receiver starts a third mint here despite all callers returning.
	time.Sleep(11 * time.Second)
	closeTime := time.Now()
	var late []time.Duration
	for len(entered) > 0 {
		at := <-entered
		if at.After(returned) {
			late = append(late, at.Sub(returned))
		}
	}
	t.Logf("observation window %s; queued mint starts after callback return: %v", closeTime.Sub(returned), late)
	require.Empty(t, late, "expired mailbox requests must not start minting after their callbacks return")
	afterMints, _, _ := f.counts()
	require.Len(t, cancellationTimes, afterMints-beforeMints, "every started cooperative broker HTTP call must stop before callbacks return")
	for _, at := range cancellationTimes {
		require.True(t, at.Before(returned), "broker HTTP cancellation must precede callback return")
	}
	assertPackagedReadOnly(t, f)
}
