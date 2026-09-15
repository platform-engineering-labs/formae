//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package workflow_tests_local

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

// Exercise actual admission, plugin completion, command finalization and the
// changeset's asynchronous cleanup, then attempt omission through the planner.
func TestPartialDestroyRetainsNeverCreatedDesiredIntent(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(r *resource.CreateRequest) (*resource.CreateResult, error) {
				p := &resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusSuccess, NativeID: "live", ResourceProperties: r.Properties}
				if strings.Contains(string(r.Properties), "pending") {
					p.OperationStatus = resource.OperationStatusFailure
					p.ErrorCode = resource.OperationErrorCodeAccessDenied
					p.NativeID = ""
				}
				return &resource.CreateResult{ProgressResult: p}, nil
			},
			Read: func(r *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{ResourceType: r.ResourceType, Properties: `{"BucketName":"live"}`}, nil
			},
			Delete: func(r *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{Operation: resource.OperationDelete, OperationStatus: resource.OperationStatusSuccess, NativeID: r.NativeID}}, nil
			},
		}
		m, cleanup, err := test_helpers.NewTestMetastructure(t, overrides)
		require.NoError(t, err)
		defer cleanup()
		f := &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "retirement"}}, Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}}, Resources: []pkgmodel.Resource{{Label: "live", Stack: "retirement", Type: "FakeAWS::S3::Bucket", Target: "test-target", Properties: json.RawMessage(`{"BucketName":"live"}`)}}}
		wait := func(id string, want forma_command.CommandState) {
			t.Helper()
			require.Eventually(t, func() bool { c, e := m.Datastore.GetFormaCommandByCommandID(id); return e == nil && c.State == want }, 15*time.Second, 20*time.Millisecond)
		}
		created, err := m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "retirement-test", "", "")
		require.NoError(t, err)
		wait(created.CommandID, forma_command.CommandStateSuccess)
		pending := f.Resources[0]
		pending.Label = "pending"
		pending.Properties = json.RawMessage(`{"BucketName":"pending"}`)
		next := *f
		next.Resources = append(append([]pkgmodel.Resource(nil), f.Resources...), pending)
		failed, err := m.ApplyForma(&next, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "retirement-test", "", "")
		require.NoError(t, err)
		wait(failed.CommandID, forma_command.CommandStateFailed)
		destroyed, err := m.DestroyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch}, "retirement-test", "", "")
		require.NoError(t, err)
		wait(destroyed.CommandID, forma_command.CommandStateSuccess)
		// Allow the changeset's asynchronous cleanup notification to run after
		// command completion; the original cleanup retires within this interval.
		require.Eventually(t, func() bool { r, e := m.Datastore.LoadResourcesByStack("retirement"); return e == nil && len(r) == 0 }, 5*time.Second, 20*time.Millisecond)
		require.Never(t, func() bool { s, e := m.Datastore.GetStackByLabel("retirement"); return e == nil && s == nil }, 200*time.Millisecond, 10*time.Millisecond, "asynchronous cleanup must preserve the incarnation")
		desired, err := m.ExtractDesiredStacks("stack:retirement")
		require.NoError(t, err)
		require.Len(t, desired.Resources, 1)
		require.Equal(t, "pending", desired.Resources[0].Label)
		empty := *f
		empty.Resources = nil
		_, err = m.ApplyForma(&empty, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, "retirement-test", "", "")
		// The direct workflow fixture's patch deletion remains unconfirmed
		// drift on the former live resource. Withdrawal of pending intent must
		// not bypass this separate observation guard or claim cloud deletion.
		var rejected apimodel.DriftResolutionError
		require.ErrorAs(t, err, &rejected)
		require.Equal(t, "resolution-unavailable", rejected.Code)
		require.Equal(t, destroyed.Simulation.Command.ResourceUpdates[0].ResourceID, rejected.ResourceID)
		require.NotEqual(t, desired.Resources[0].Ksuid, rejected.ResourceID)
	})
}
