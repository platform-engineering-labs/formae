// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	_ "github.com/platform-engineering-labs/formae/internal/datastore/all"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/querier"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// newSQLiteTestDatastore creates a throwaway in-memory sqlite datastore for
// tests that exercise real Metastructure query methods end to end (as
// opposed to a fake/mock Datastore), so the production wiring between
// Metastructure, the querier, and the datastore is what's actually under
// test.
func newSQLiteTestDatastore(t *testing.T) datastore.Datastore {
	t.Helper()
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}
	ds, err := datastore.DefaultRegistry.Create("sqlite", context.Background(), cfg, "test")
	require.NoError(t, err)
	return ds
}

// TestListFormaCommandStatus_ExcludesSchedulerCommands exercises the real
// ListFormaCommandStatus production path end to end: BuildStatusQuery, the
// statusQuery.Source = SourceUser assignment, and QueryFormaCommands against
// a real datastore. It fails if that Source assignment is ever removed from
// ListFormaCommandStatus — without it this query would return both the user
// and the scheduler command below instead of only the user one.
func TestListFormaCommandStatus_ExcludesSchedulerCommands(t *testing.T) {
	ds := newSQLiteTestDatastore(t)

	userApply := &forma_command.FormaCommand{
		ID:      util.NewID(),
		Command: pkgmodel.CommandApply,
		State:   forma_command.CommandStateSuccess,
		Source:  forma_command.SourceUser,
	}
	autoReconcilerApply := &forma_command.FormaCommand{
		ID:      util.NewID(),
		Command: pkgmodel.CommandApply,
		State:   forma_command.CommandStateSuccess,
		Source:  forma_command.SourceAutoReconciler,
	}
	require.NoError(t, ds.StoreFormaCommand(userApply, userApply.ID))
	require.NoError(t, ds.StoreFormaCommand(autoReconcilerApply, autoReconcilerApply.ID))

	m := &Metastructure{Datastore: ds}
	resp, err := m.ListFormaCommandStatus("command:apply", querier.Caller{ClientID: "client-1"}, 10, apimodel.CommandScopeAgent)
	require.NoError(t, err)
	require.Len(t, resp.Commands, 1, "scheduler command must not appear alongside the user command")
	assert.Equal(t, userApply.ID, resp.Commands[0].CommandID)
}

// TestCommandsForCancelQuery_UnfilteredQueryExcludesSyncButExplicitQueryReachesIt
// exercises the real commandsForCancelQuery production path (used by
// CancelCommandsByQuery) end to end against a real datastore. An unfiltered
// query must not surface a sync command as a cancel candidate (mirroring the
// exclusion QueryFormaCommands itself used to apply implicitly), but an
// operator explicitly asking for `command:sync` — e.g. to drain scheduler
// bookkeeping ahead of an agent restart — must still reach it.
func TestCommandsForCancelQuery_UnfilteredQueryExcludesSyncButExplicitQueryReachesIt(t *testing.T) {
	ds := newSQLiteTestDatastore(t)

	userApply := &forma_command.FormaCommand{
		ID:      util.NewID(),
		Command: pkgmodel.CommandApply,
		State:   forma_command.CommandStateInProgress,
		Source:  forma_command.SourceUser,
	}
	syncCommand := &forma_command.FormaCommand{
		ID:      util.NewID(),
		Command: pkgmodel.CommandSync,
		State:   forma_command.CommandStateInProgress,
		Source:  forma_command.SourceSynchronizer,
	}
	require.NoError(t, ds.StoreFormaCommand(userApply, userApply.ID))
	require.NoError(t, ds.StoreFormaCommand(syncCommand, syncCommand.ID))

	m := &Metastructure{Datastore: ds}

	unfiltered, err := m.commandsForCancelQuery("status:InProgress", querier.Caller{ClientID: "client-1"})
	require.NoError(t, err)
	gotIDs := make([]string, 0, len(unfiltered))
	for _, cmd := range unfiltered {
		gotIDs = append(gotIDs, cmd.ID)
	}
	assert.Contains(t, gotIDs, userApply.ID, "unfiltered query must still surface the user command")
	assert.NotContains(t, gotIDs, syncCommand.ID, "unfiltered query must not surface the sync command as a cancel candidate")

	explicit, err := m.commandsForCancelQuery("command:sync", querier.Caller{ClientID: "client-1"})
	require.NoError(t, err)
	require.Len(t, explicit, 1, "an explicit command:sync query must still be able to target sync commands")
	assert.Equal(t, syncCommand.ID, explicit[0].ID)
}

// TestCommandsForCancelQuery_UserMeSelectsOnlyCallersCommands exercises the
// real commandsForCancelQuery production path with a `user:me` query: it
// shares BuildStatusQuery with the status-listing path, so the caller's
// Subject must select only that caller's commands, never another subject's,
// even when the other subject's command is also InProgress.
func TestCommandsForCancelQuery_UserMeSelectsOnlyCallersCommands(t *testing.T) {
	ds := newSQLiteTestDatastore(t)

	mine := &forma_command.FormaCommand{
		ID:      util.NewID(),
		Command: pkgmodel.CommandApply,
		State:   forma_command.CommandStateInProgress,
		Source:  forma_command.SourceUser,
		Subject: "caller-subject",
	}
	theirs := &forma_command.FormaCommand{
		ID:      util.NewID(),
		Command: pkgmodel.CommandApply,
		State:   forma_command.CommandStateInProgress,
		Source:  forma_command.SourceUser,
		Subject: "other-subject",
	}
	require.NoError(t, ds.StoreFormaCommand(mine, mine.ID))
	require.NoError(t, ds.StoreFormaCommand(theirs, theirs.ID))

	m := &Metastructure{Datastore: ds}

	got, err := m.commandsForCancelQuery("user:me", querier.Caller{ClientID: "client-1", Subject: "caller-subject"})
	require.NoError(t, err)
	require.Len(t, got, 1, "user:me must select only the caller's own command")
	assert.Equal(t, mine.ID, got[0].ID)
}

// TestCommandsForCancelQuery_UserMeUnauthenticatedIsInvalidQuery covers a
// cancel-by-query request with no authenticated identity: `user:me` must be
// refused with the same InvalidQueryError the status-listing path returns,
// not a silent no-op and not a different error type.
func TestCommandsForCancelQuery_UserMeUnauthenticatedIsInvalidQuery(t *testing.T) {
	ds := newSQLiteTestDatastore(t)

	m := &Metastructure{Datastore: ds}

	got, err := m.commandsForCancelQuery("user:me", querier.Caller{ClientID: "client-1"})
	assert.Nil(t, got)
	require.Error(t, err)

	var invalidQueryErr apimodel.InvalidQueryError
	assert.ErrorAs(t, err, &invalidQueryErr, "an unauthenticated user:me cancel query must fail with InvalidQueryError, not a different error type")
}

// storeUserCommand persists a terminal user-initiated apply attributed to
// clientID and returns its id, so scope tests can compose a multi-client
// command history.
func storeUserCommand(t *testing.T, ds datastore.Datastore, clientID string) string {
	t.Helper()
	cmd := &forma_command.FormaCommand{
		ID:       util.NewID(),
		Command:  pkgmodel.CommandApply,
		State:    forma_command.CommandStateSuccess,
		ClientID: clientID,
		Source:   forma_command.SourceUser,
	}
	require.NoError(t, ds.StoreFormaCommand(cmd, cmd.ID))
	return cmd.ID
}

// TestListFormaCommandStatus_AgentScopeSpansClients covers `formae command
// list` with no query: it lists user commands agent-wide, so a command
// submitted by another client is visible to the caller.
func TestListFormaCommandStatus_AgentScopeSpansClients(t *testing.T) {
	ds := newSQLiteTestDatastore(t)

	mine := storeUserCommand(t, ds, "client-1")
	theirs := storeUserCommand(t, ds, "client-2")

	m := &Metastructure{Datastore: ds}
	resp, err := m.ListFormaCommandStatus("", querier.Caller{ClientID: "client-1"}, 50, apimodel.CommandScopeAgent)
	require.NoError(t, err)

	gotIDs := make([]string, 0, len(resp.Commands))
	for _, cmd := range resp.Commands {
		gotIDs = append(gotIDs, cmd.CommandID)
	}
	assert.Contains(t, gotIDs, mine)
	assert.Contains(t, gotIDs, theirs, "a command list must span every client, not just the caller")
}

// TestListFormaCommandStatus_AgentScopeReturnsAPage covers the page-size
// contract of a bare `formae command list`: with more commands than one
// stored, it returns a page of them (up to the requested count), not a
// single most-recent command.
func TestListFormaCommandStatus_AgentScopeReturnsAPage(t *testing.T) {
	ds := newSQLiteTestDatastore(t)

	for i := 0; i < 5; i++ {
		storeUserCommand(t, ds, "client-1")
	}

	m := &Metastructure{Datastore: ds}
	resp, err := m.ListFormaCommandStatus("", querier.Caller{ClientID: "client-1"}, 50, apimodel.CommandScopeAgent)
	require.NoError(t, err)
	assert.Len(t, resp.Commands, 5, "an unqueried list must return every matching command up to the requested count")

	bounded, err := m.ListFormaCommandStatus("", querier.Caller{ClientID: "client-1"}, 2, apimodel.CommandScopeAgent)
	require.NoError(t, err)
	assert.Len(t, bounded.Commands, 2, "the requested count must bound the page")
}

// TestListFormaCommandStatus_ClientScopeReturnsOnlyCallersMostRecent covers
// `formae command status` with no argument: it answers with the calling
// client's own most recent command, never another client's, even when the
// other client's command is newer.
func TestListFormaCommandStatus_ClientScopeReturnsOnlyCallersMostRecent(t *testing.T) {
	ds := newSQLiteTestDatastore(t)

	mine := storeUserCommand(t, ds, "client-1")
	theirs := storeUserCommand(t, ds, "client-2")

	m := &Metastructure{Datastore: ds}
	resp, err := m.ListFormaCommandStatus("", querier.Caller{ClientID: "client-1"}, 1, apimodel.CommandScopeClient)
	require.NoError(t, err)
	require.Len(t, resp.Commands, 1)
	assert.Equal(t, mine, resp.Commands[0].CommandID)
	assert.NotEqual(t, theirs, resp.Commands[0].CommandID)
}

// TestListFormaCommandStatus_ClientScopeWithNoCommandsIsEmptyNotAnError
// covers a client that has never run a command (including an upgraded agent
// whose stored history predates command sources): the answer is an empty
// result, not an error the API would surface as a 500.
func TestListFormaCommandStatus_ClientScopeWithNoCommandsIsEmptyNotAnError(t *testing.T) {
	ds := newSQLiteTestDatastore(t)

	m := &Metastructure{Datastore: ds}
	resp, err := m.ListFormaCommandStatus("", querier.Caller{ClientID: "client-with-no-history"}, 1, apimodel.CommandScopeClient)
	require.NoError(t, err)
	assert.Empty(t, resp.Commands)
}

func TestReplaceKSUIDs_NestedRefInArrays(t *testing.T) {
	// This test verifies that $ref objects nested inside arrays are properly
	// converted to $res objects. This is the case for resources like
	// GCP::Compute::Instance where disks[].source and networkInterfaces[].network
	// contain $ref objects.

	tests := []struct {
		name           string
		inputJSON      string
		ksuidToTriplet map[string]pkgmodel.TripletKey
		wantContains   []string
		wantNotContain []string
	}{
		{
			name: "converts $ref in top-level property",
			inputJSON: `{
				"network": {
					"$ref": "formae://abc123#/selfLink",
					"$value": "https://example.com/network"
				}
			}`,
			ksuidToTriplet: map[string]pkgmodel.TripletKey{
				"abc123": {Stack: "my-stack", Label: "my-network", Type: "GCP::Compute::Network"},
			},
			wantContains:   []string{`"$res":true`, `"$label":"my-network"`, `"$stack":"my-stack"`, `"$type":"GCP::Compute::Network"`, `"$property":"selfLink"`},
			wantNotContain: []string{`"$ref"`},
		},
		{
			name: "converts $ref nested in array of objects",
			inputJSON: `{
				"disks": [
					{
						"boot": true,
						"source": {
							"$ref": "formae://disk123#/selfLink",
							"$value": "https://example.com/disk"
						}
					}
				]
			}`,
			ksuidToTriplet: map[string]pkgmodel.TripletKey{
				"disk123": {Stack: "my-stack", Label: "my-disk", Type: "GCP::Compute::Disk"},
			},
			wantContains:   []string{`"$res":true`, `"$label":"my-disk"`, `"$stack":"my-stack"`, `"$type":"GCP::Compute::Disk"`, `"$property":"selfLink"`},
			wantNotContain: []string{`"$ref"`},
		},
		{
			name: "converts multiple $ref objects in array",
			inputJSON: `{
				"networkInterfaces": [
					{
						"name": "nic0",
						"network": {
							"$ref": "formae://net123#/selfLink",
							"$value": "https://example.com/network"
						},
						"subnetwork": {
							"$ref": "formae://subnet123#/selfLink",
							"$value": "https://example.com/subnet"
						}
					}
				]
			}`,
			ksuidToTriplet: map[string]pkgmodel.TripletKey{
				"net123":    {Stack: "my-stack", Label: "my-network", Type: "GCP::Compute::Network"},
				"subnet123": {Stack: "my-stack", Label: "my-subnet", Type: "GCP::Compute::Subnetwork"},
			},
			wantContains:   []string{`"$label":"my-network"`, `"$label":"my-subnet"`, `"$type":"GCP::Compute::Network"`, `"$type":"GCP::Compute::Subnetwork"`},
			wantNotContain: []string{`"$ref"`},
		},
		{
			name: "converts deeply nested $ref in array",
			inputJSON: `{
				"items": [
					{
						"nested": {
							"deep": {
								"ref": {
									"$ref": "formae://deep123#/prop",
									"$value": "deep-value"
								}
							}
						}
					}
				]
			}`,
			ksuidToTriplet: map[string]pkgmodel.TripletKey{
				"deep123": {Stack: "deep-stack", Label: "deep-label", Type: "Deep::Type"},
			},
			wantContains:   []string{`"$res":true`, `"$label":"deep-label"`, `"$stack":"deep-stack"`},
			wantNotContain: []string{`"$ref"`},
		},
		{
			name: "handles mix of arrays and nested objects with refs",
			inputJSON: `{
				"topLevel": {
					"$ref": "formae://top123#/topProp",
					"$value": "top-value"
				},
				"arrayField": [
					{
						"arrayNested": {
							"$ref": "formae://arr123#/arrProp",
							"$value": "arr-value"
						}
					}
				]
			}`,
			ksuidToTriplet: map[string]pkgmodel.TripletKey{
				"top123": {Stack: "stack1", Label: "label1", Type: "Type1"},
				"arr123": {Stack: "stack2", Label: "label2", Type: "Type2"},
			},
			wantContains:   []string{`"$label":"label1"`, `"$label":"label2"`, `"$type":"Type1"`, `"$type":"Type2"`},
			wantNotContain: []string{`"$ref"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := replaceKSUIDs(tt.inputJSON, tt.ksuidToTriplet)

			for _, want := range tt.wantContains {
				assert.Contains(t, result, want, "result should contain %s", want)
			}

			for _, notWant := range tt.wantNotContain {
				assert.NotContains(t, result, notWant, "result should not contain %s", notWant)
			}
		})
	}
}

func TestExtractKSUIDs_NestedRefInArrays(t *testing.T) {
	// This test verifies that KSUIDs from $ref objects nested inside arrays
	// are properly extracted. This is a prerequisite for replaceKSUIDs to work.

	tests := []struct {
		name         string
		inputJSON    string
		wantKSUIDs   []string
		wantNotFound []string
	}{
		{
			name: "extracts KSUID from top-level $ref",
			inputJSON: `{
				"network": {
					"$ref": "formae://abc123#/selfLink",
					"$value": "https://example.com/network"
				}
			}`,
			wantKSUIDs: []string{"abc123"},
		},
		{
			name: "extracts KSUID from $ref nested in array",
			inputJSON: `{
				"disks": [
					{
						"boot": true,
						"source": {
							"$ref": "formae://disk123#/selfLink",
							"$value": "https://example.com/disk"
						}
					}
				]
			}`,
			wantKSUIDs: []string{"disk123"},
		},
		{
			name: "extracts multiple KSUIDs from array items",
			inputJSON: `{
				"networkInterfaces": [
					{
						"network": {
							"$ref": "formae://net123#/selfLink",
							"$value": "https://example.com/network"
						},
						"subnetwork": {
							"$ref": "formae://subnet456#/selfLink",
							"$value": "https://example.com/subnet"
						}
					}
				]
			}`,
			wantKSUIDs: []string{"net123", "subnet456"},
		},
		{
			name: "extracts KSUID from deeply nested array",
			inputJSON: `{
				"items": [
					{
						"nested": {
							"deep": {
								"ref": {
									"$ref": "formae://deep789#/prop",
									"$value": "deep-value"
								}
							}
						}
					}
				]
			}`,
			wantKSUIDs: []string{"deep789"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ksuidSet := make(map[string]struct{})
			extractKSUIDs(tt.inputJSON, ksuidSet)

			for _, want := range tt.wantKSUIDs {
				_, found := ksuidSet[want]
				assert.True(t, found, "should have extracted KSUID %s", want)
			}

			for _, notWant := range tt.wantNotFound {
				_, found := ksuidSet[notWant]
				assert.False(t, found, "should not have extracted KSUID %s", notWant)
			}
		})
	}
}

func TestMetastructure_NetworkingEnabled(t *testing.T) {
	cfg := &pkgmodel.Config{
		Agent: pkgmodel.AgentConfig{
			Server: pkgmodel.ServerConfig{
				Nodename: "test-agent",
				Hostname: "localhost",
				Secret:   "secret",
			},
		},
	}

	m, err := NewMetastructure(context.Background(), cfg, nil, nil, "test")
	require.NoError(t, err)
	require.NotNil(t, m)

	// Verify networking is enabled
	assert.NotEqual(t, gen.NetworkModeDisabled, m.options.Network.Mode,
		"Network mode should not be disabled")
	assert.Equal(t, gen.NetworkModeEnabled, m.options.Network.Mode,
		"Network mode should be enabled")

	// Verify cookie is set from config
	assert.Equal(t, "secret", m.options.Network.Cookie,
		"Network cookie should match config secret")
}

func TestReplaceKSUIDs_RewritesEmbeddedSpan(t *testing.T) {
	ksuid := "abc123"
	refEnv := `{"$ref":"formae://` + ksuid + `#/id","$value":"v1"}`
	tmpl := "cf.kvs('" + pkgmodel.FrameEnvelope(refEnv) + "')"

	in, err := json.Marshal(map[string]any{
		"functionCode": map[string]any{"$embed": true, "$template": tmpl},
	})
	require.NoError(t, err)

	out := replaceKSUIDs(string(in), map[string]pkgmodel.TripletKey{
		ksuid: {Stack: "default", Label: "kvs", Type: "AWS::CloudFront::KeyValueStore"},
	})

	tmplOut := gjson.Get(out, "functionCode.$template").String()
	spans, err := pkgmodel.ScanEmbedSpans(tmplOut)
	require.NoError(t, err)
	require.Len(t, spans, 1, "expected exactly one span in $template output")

	assert.True(t, strings.Contains(spans[0].EnvelopeJSON, `"$res":true`),
		"span should contain $res:true, got: %s", spans[0].EnvelopeJSON)
	assert.True(t, strings.Contains(spans[0].EnvelopeJSON, `"$label":"kvs"`),
		"span should contain $label:kvs, got: %s", spans[0].EnvelopeJSON)
}

func TestReplaceKSUIDs_RewritesEmbeddedSpan_Idempotent(t *testing.T) {
	ksuid := "abc123"
	refEnv := `{"$ref":"formae://` + ksuid + `#/id","$value":"v1"}`
	tmpl := "cf.kvs('" + pkgmodel.FrameEnvelope(refEnv) + "')"

	in, err := json.Marshal(map[string]any{
		"functionCode": map[string]any{"$embed": true, "$template": tmpl},
	})
	require.NoError(t, err)

	ksuidToTriplet := map[string]pkgmodel.TripletKey{
		ksuid: {Stack: "default", Label: "kvs", Type: "AWS::CloudFront::KeyValueStore"},
	}

	out1 := replaceKSUIDs(string(in), ksuidToTriplet)
	out2 := replaceKSUIDs(out1, ksuidToTriplet)
	assert.Equal(t, out1, out2, "replaceKSUIDs should be idempotent")
}

func TestReplaceKSUIDs_RewritesMultipleEmbeddedSpans(t *testing.T) {
	// Two distinct KSUIDs appear as framed $ref spans inside a single $template.
	// Literal text separates and surrounds the spans.
	// After replaceKSUIDs both spans must be rewritten to $res+triplet form
	// and all surrounding/between literal text must be intact and in order.
	ksuid1 := "aaaa1111"
	ksuid2 := "bbbb2222"

	refEnv1 := `{"$ref":"formae://` + ksuid1 + `#/arn","$value":"v1"}`
	refEnv2 := `{"$ref":"formae://` + ksuid2 + `#/id","$value":"v2"}`

	tmpl := "prefix(" + pkgmodel.FrameEnvelope(refEnv1) + ",between," + pkgmodel.FrameEnvelope(refEnv2) + ")suffix"

	in, err := json.Marshal(map[string]any{
		"code": map[string]any{"$embed": true, "$template": tmpl},
	})
	require.NoError(t, err)

	out := replaceKSUIDs(string(in), map[string]pkgmodel.TripletKey{
		ksuid1: {Stack: "default", Label: "bucket", Type: "AWS::S3::Bucket"},
		ksuid2: {Stack: "default", Label: "queue", Type: "AWS::SQS::Queue"},
	})

	tmplOut := gjson.Get(out, "code.$template").String()
	spans, err := pkgmodel.ScanEmbedSpans(tmplOut)
	require.NoError(t, err)
	require.Len(t, spans, 2, "expected exactly two spans in $template output")

	// Both spans must have been rewritten to $res form.
	assert.True(t, strings.Contains(spans[0].EnvelopeJSON, `"$res":true`),
		"first span should contain $res:true, got: %s", spans[0].EnvelopeJSON)
	assert.True(t, strings.Contains(spans[0].EnvelopeJSON, `"$label":"bucket"`),
		"first span should contain $label:bucket, got: %s", spans[0].EnvelopeJSON)
	assert.True(t, strings.Contains(spans[1].EnvelopeJSON, `"$res":true`),
		"second span should contain $res:true, got: %s", spans[1].EnvelopeJSON)
	assert.True(t, strings.Contains(spans[1].EnvelopeJSON, `"$label":"queue"`),
		"second span should contain $label:queue, got: %s", spans[1].EnvelopeJSON)

	// Surrounding and between literal text must be intact.
	assert.True(t, strings.HasPrefix(tmplOut, "prefix("),
		"template should start with 'prefix(', got: %s", tmplOut)
	assert.True(t, strings.HasSuffix(tmplOut, ")suffix"),
		"template should end with ')suffix', got: %s", tmplOut)
	assert.True(t, strings.Contains(tmplOut, ",between,"),
		"template should contain ',between,' between spans, got: %s", tmplOut)
}

// storeReconcileBaseline persists a successful user reconcile for stackLabel
// so prepareReconcile has a last-reconcile snapshot to enforce.
func storeReconcileBaseline(t *testing.T, ds datastore.Datastore, stackLabel string) {
	t.Helper()
	baseline := &forma_command.FormaCommand{
		ID:         util.NewID(),
		Command:    pkgmodel.CommandApply,
		Config:     config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		State:      forma_command.CommandStateSuccess,
		Source:     forma_command.SourceUser,
		StartTs:    util.TimeNow().Add(-5 * time.Minute),
		ModifiedTs: util.TimeNow().Add(-5 * time.Minute),
		ResourceUpdates: []resource_update.ResourceUpdate{{
			Operation:  types.OperationCreate,
			State:      resource_update.ResourceUpdateStateSuccess,
			Source:     resource_update.FormaCommandSourceUser,
			StackLabel: stackLabel,
			DesiredState: pkgmodel.Resource{
				Ksuid:      util.NewID(),
				Label:      "bucket-1",
				Type:       "AWS::S3::Bucket",
				Stack:      stackLabel,
				Target:     "default-target",
				NativeID:   "native-bucket-1",
				Properties: json.RawMessage(`{"foo":"v1"}`),
				Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
			},
		}},
	}
	require.NoError(t, ds.StoreFormaCommand(baseline, baseline.ID))
}

// TestForceReconcileCommandIsVisibleThroughCommandStatus covers the command
// id ForceAutoReconcile hands back: it names a user-initiated command, so
// looking it up through the ordinary status-by-id path finds it. The
// scheduled reconcile beat stays scheduler-sourced and out of the
// user-facing history.
func TestForceReconcileCommandIsVisibleThroughCommandStatus(t *testing.T) {
	ds := newSQLiteTestDatastore(t)
	storeReconcileBaseline(t, ds, "stack-a")

	forced, err := prepareReconcile(ds, "stack-a", "force-reconcile", "", "", forma_command.SourceUser)
	require.NoError(t, err)
	require.NotNil(t, forced, "the stored baseline must produce something to reconcile")
	require.NoError(t, ds.StoreFormaCommand(forced.command, forced.command.ID))

	m := &Metastructure{Datastore: ds}
	resp, err := m.ListFormaCommandStatus("id:"+forced.command.ID, querier.Caller{ClientID: "cli-client"}, 1, apimodel.CommandScopeAgent)
	require.NoError(t, err)
	require.Len(t, resp.Commands, 1, "the id returned by a force-reconcile must resolve through command status")
	assert.Equal(t, forced.command.ID, resp.Commands[0].CommandID)

	scheduled, err := prepareReconcile(ds, "stack-a", "auto-reconciler", "", "", forma_command.SourceAutoReconciler)
	require.NoError(t, err)
	require.NotNil(t, scheduled)
	require.NoError(t, ds.StoreFormaCommand(scheduled.command, scheduled.command.ID))

	hidden, err := m.ListFormaCommandStatus("id:"+scheduled.command.ID, querier.Caller{ClientID: "cli-client"}, 1, apimodel.CommandScopeAgent)
	require.NoError(t, err)
	assert.Empty(t, hidden.Commands, "a scheduled reconcile stays out of the user-facing command history")
}

// A client-submitted destroy forma carries no ksuids, only (stack, label,
// type) triplets. The cascade-stack walk must resolve those triplets against
// the datastore before seeding the dependents BFS; a walk seeded only from
// forma-supplied ksuids finds nothing, and the admission conflict check never
// sees the stacks a cascade delete will touch.
func TestFindCascadeStackLabels_ResolvesKsuidlessFormaResources(t *testing.T) {
	ds := newSQLiteTestDatastore(t)

	parent := &pkgmodel.Resource{
		Stack:      "stack-0",
		Label:      "res-a",
		Type:       "Test::Generic::Resource",
		NativeID:   "native-parent",
		Properties: json.RawMessage(`{"Name":"res-a"}`),
		Managed:    true,
	}
	_, err := ds.StoreResource(parent, "create-parent")
	require.NoError(t, err)
	stored, err := ds.LoadResourceByNativeID("native-parent", "Test::Generic::Resource")
	require.NoError(t, err)
	require.NotNil(t, stored)

	child := &pkgmodel.Resource{
		Stack:    "stack-1",
		Label:    "child-xstack",
		Type:     "Test::Generic::ChildResource",
		NativeID: "native-child",
		Properties: json.RawMessage(
			`{"Name":"child-xstack","ParentId":{"$ref":"formae://` + stored.Ksuid + `#/Name"}}`),
		Managed: true,
	}
	_, err = ds.StoreResource(child, "create-child")
	require.NoError(t, err)

	m := &Metastructure{Datastore: ds}
	stacks, err := m.findCascadeStackLabels(&pkgmodel.Forma{
		Resources: []pkgmodel.Resource{
			{Stack: "stack-0", Label: "res-a", Type: "Test::Generic::Resource"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"stack-1"}, stacks,
		"the cross-stack dependent's stack must surface for admission deconfliction")
}
