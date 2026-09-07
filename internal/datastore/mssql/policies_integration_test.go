// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package mssql_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/datastore/mssql"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const policiesTestBaseDSN = "sqlserver://sa:Formae_Test_1234!@localhost:1433?encrypt=disable"

// newPoliciesTestDS creates a brand-new database (formae_policies_<unixnano>) on
// the local test container and returns a connected DatastoreMSSQL plus a
// cleanup func that drops it.
func newPoliciesTestDS(t *testing.T) (datastore.Datastore, func()) {
	t.Helper()

	master, err := sql.Open("sqlserver", policiesTestBaseDSN+"&database=master")
	if err != nil {
		t.Skipf("mssql driver open failed: %v", err)
	}
	if err := master.Ping(); err != nil {
		_ = master.Close()
		t.Skipf("local mssql not reachable on :1433: %v", err)
	}

	dbName := fmt.Sprintf("formae_policies_%d", time.Now().UnixNano())
	if _, err := master.Exec(fmt.Sprintf("CREATE DATABASE [%s]", dbName)); err != nil {
		_ = master.Close()
		t.Fatalf("create test db: %v", err)
	}

	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: "mssql",
		MSSQL: pkgmodel.MSSQLConfig{
			AuthMode: pkgmodel.MSSQLAuthSQL,
			Host:     "localhost",
			Port:     1433,
			User:     "sa",
			Password: "Formae_Test_1234!",
			Database: dbName,
		},
	}

	ds, err := mssql.NewDatastoreMSSQL(context.Background(), cfg, "test-agent")
	if err != nil {
		_, _ = master.Exec(fmt.Sprintf("ALTER DATABASE [%s] SET SINGLE_USER WITH ROLLBACK IMMEDIATE", dbName))
		_, _ = master.Exec(fmt.Sprintf("DROP DATABASE [%s]", dbName))
		_ = master.Close()
		t.Fatalf("NewDatastoreMSSQL: %v", err)
	}

	cleanup := func() {
		ds.Close()
		_, _ = master.Exec(fmt.Sprintf("ALTER DATABASE [%s] SET SINGLE_USER WITH ROLLBACK IMMEDIATE", dbName))
		_, _ = master.Exec(fmt.Sprintf("DROP DATABASE [%s]", dbName))
		_ = master.Close()
	}
	return ds, cleanup
}

func TestMSSQLPoliciesStandaloneRoundTrip(t *testing.T) {
	ds, cleanup := newPoliciesTestDS(t)
	defer cleanup()

	// Standalone policy: empty StackID.
	pol := &pkgmodel.TTLPolicy{
		Type:         "ttl",
		Label:        "ephemeral",
		TTLSeconds:   3600,
		OnDependents: "cascade",
	}

	// Not-found before creation.
	got, err := ds.GetStandalonePolicy("ephemeral")
	if err != nil {
		t.Fatalf("GetStandalonePolicy(before): %v", err)
	}
	if got != nil {
		t.Fatalf("GetStandalonePolicy(before) = %+v, want nil", got)
	}

	// Create.
	if _, err := ds.CreatePolicy(pol, "cmd-1"); err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	// Get.
	got, err = ds.GetStandalonePolicy("ephemeral")
	if err != nil {
		t.Fatalf("GetStandalonePolicy: %v", err)
	}
	ttl, ok := got.(*pkgmodel.TTLPolicy)
	if !ok {
		t.Fatalf("GetStandalonePolicy returned %T, want *TTLPolicy", got)
	}
	if ttl.Label != "ephemeral" || ttl.TTLSeconds != 3600 || ttl.OnDependents != "cascade" {
		t.Errorf("GetStandalonePolicy = %+v", ttl)
	}

	// Update bumps the version but keeps the latest readable.
	pol.TTLSeconds = 7200
	if _, err := ds.UpdatePolicy(pol, "cmd-2"); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}
	got, err = ds.GetStandalonePolicy("ephemeral")
	if err != nil {
		t.Fatalf("GetStandalonePolicy(after update): %v", err)
	}
	if got.(*pkgmodel.TTLPolicy).TTLSeconds != 7200 {
		t.Errorf("TTLSeconds after update = %d, want 7200", got.(*pkgmodel.TTLPolicy).TTLSeconds)
	}

	// ListAllStandalonePolicies sees it.
	all, err := ds.ListAllStandalonePolicies()
	if err != nil {
		t.Fatalf("ListAllStandalonePolicies: %v", err)
	}
	if len(all) != 1 || all[0].GetLabel() != "ephemeral" {
		t.Fatalf("ListAllStandalonePolicies = %+v, want [ephemeral]", all)
	}

	// Attach to a stack.
	stack := &pkgmodel.Stack{ID: "stack-ksuid-1", Label: "my-stack"}
	if _, err := ds.CreateStack(stack, "cmd-3"); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}
	if err := ds.AttachPolicyToStack(stack.ID, "ephemeral"); err != nil {
		t.Fatalf("AttachPolicyToStack: %v", err)
	}
	// Idempotent re-attach.
	if err := ds.AttachPolicyToStack(stack.ID, "ephemeral"); err != nil {
		t.Fatalf("AttachPolicyToStack(again): %v", err)
	}

	// IsPolicyAttachedToStack.
	attached, err := ds.IsPolicyAttachedToStack("my-stack", "ephemeral")
	if err != nil {
		t.Fatalf("IsPolicyAttachedToStack: %v", err)
	}
	if !attached {
		t.Errorf("IsPolicyAttachedToStack = false, want true")
	}

	// GetStacksReferencingPolicy + GetAttachedPolicyLabelsForStack.
	stacks, err := ds.GetStacksReferencingPolicy("ephemeral")
	if err != nil {
		t.Fatalf("GetStacksReferencingPolicy: %v", err)
	}
	if len(stacks) != 1 || stacks[0] != "my-stack" {
		t.Errorf("GetStacksReferencingPolicy = %+v, want [my-stack]", stacks)
	}
	labels, err := ds.GetAttachedPolicyLabelsForStack("my-stack")
	if err != nil {
		t.Fatalf("GetAttachedPolicyLabelsForStack: %v", err)
	}
	if len(labels) != 1 || labels[0] != "ephemeral" {
		t.Errorf("GetAttachedPolicyLabelsForStack = %+v, want [ephemeral]", labels)
	}

	// GetPoliciesForStack returns the standalone policy via the junction.
	stackPolicies, err := ds.GetPoliciesForStack(stack.ID)
	if err != nil {
		t.Fatalf("GetPoliciesForStack: %v", err)
	}
	if len(stackPolicies) != 1 || stackPolicies[0].GetLabel() != "ephemeral" {
		t.Fatalf("GetPoliciesForStack = %+v, want [ephemeral]", stackPolicies)
	}

	// Detach.
	if err := ds.DetachPolicyFromStack("my-stack", "ephemeral"); err != nil {
		t.Fatalf("DetachPolicyFromStack: %v", err)
	}
	attached, err = ds.IsPolicyAttachedToStack("my-stack", "ephemeral")
	if err != nil {
		t.Fatalf("IsPolicyAttachedToStack(after detach): %v", err)
	}
	if attached {
		t.Errorf("IsPolicyAttachedToStack(after detach) = true, want false")
	}

	// Delete (tombstone): standalone policy no longer visible.
	if _, err := ds.DeletePolicy("ephemeral"); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	got, err = ds.GetStandalonePolicy("ephemeral")
	if err != nil {
		t.Fatalf("GetStandalonePolicy(after delete): %v", err)
	}
	if got != nil {
		t.Errorf("GetStandalonePolicy(after delete) = %+v, want nil", got)
	}
	all, err = ds.ListAllStandalonePolicies()
	if err != nil {
		t.Fatalf("ListAllStandalonePolicies(after delete): %v", err)
	}
	if len(all) != 0 {
		t.Errorf("ListAllStandalonePolicies(after delete) = %+v, want empty", all)
	}
}

func TestMSSQLPoliciesInlineAndTTLQueries(t *testing.T) {
	ds, cleanup := newPoliciesTestDS(t)
	defer cleanup()

	// Stack with an inline (stack_id set) TTL policy that has already expired.
	stack := &pkgmodel.Stack{ID: "stack-ttl-1", Label: "ttl-stack"}
	if _, err := ds.CreateStack(stack, "cmd-ttl"); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}
	inlineTTL := &pkgmodel.TTLPolicy{
		Type:         "ttl",
		Label:        "expired-ttl",
		TTLSeconds:   -10, // already in the past relative to valid_from
		OnDependents: "cascade",
		StackID:      stack.ID,
	}
	if _, err := ds.CreatePolicy(inlineTTL, "cmd-ttl"); err != nil {
		t.Fatalf("CreatePolicy(inline ttl): %v", err)
	}

	// GetPoliciesForStack returns the inline policy with its stack id preserved.
	pols, err := ds.GetPoliciesForStack(stack.ID)
	if err != nil {
		t.Fatalf("GetPoliciesForStack: %v", err)
	}
	if len(pols) != 1 || pols[0].GetStackID() != stack.ID {
		t.Fatalf("GetPoliciesForStack = %+v, want one with stackID %s", pols, stack.ID)
	}

	// GetExpiredStacks should surface the expired stack.
	expired, err := ds.GetExpiredStacks()
	if err != nil {
		t.Fatalf("GetExpiredStacks: %v", err)
	}
	found := false
	for _, e := range expired {
		if e.StackLabel == "ttl-stack" {
			found = true
			if e.OnDependents != "cascade" {
				t.Errorf("OnDependents = %q, want cascade", e.OnDependents)
			}
			if e.StackID != stack.ID {
				t.Errorf("StackID = %q, want %q", e.StackID, stack.ID)
			}
		}
	}
	if !found {
		t.Errorf("GetExpiredStacks did not include ttl-stack: %+v", expired)
	}

	// Auto-reconcile stack via inline policy.
	arStack := &pkgmodel.Stack{ID: "stack-ar-1", Label: "ar-stack"}
	if _, err := ds.CreateStack(arStack, "cmd-ar"); err != nil {
		t.Fatalf("CreateStack(ar): %v", err)
	}
	arPol := &pkgmodel.AutoReconcilePolicy{
		Type:            "auto-reconcile",
		Label:           "reconcile-5m",
		IntervalSeconds: 300,
		StackID:         arStack.ID,
	}
	if _, err := ds.CreatePolicy(arPol, "cmd-ar"); err != nil {
		t.Fatalf("CreatePolicy(auto-reconcile): %v", err)
	}
	arInfos, err := ds.GetStacksWithAutoReconcilePolicy()
	if err != nil {
		t.Fatalf("GetStacksWithAutoReconcilePolicy: %v", err)
	}
	foundAR := false
	for _, info := range arInfos {
		if info.StackLabel == "ar-stack" {
			foundAR = true
			if info.IntervalSeconds != 300 {
				t.Errorf("IntervalSeconds = %d, want 300", info.IntervalSeconds)
			}
		}
	}
	if !foundAR {
		t.Errorf("GetStacksWithAutoReconcilePolicy did not include ar-stack: %+v", arInfos)
	}

	// StackHasActiveCommands: no active commands referencing these stacks.
	active, err := ds.StackHasActiveCommands("ttl-stack")
	if err != nil {
		t.Fatalf("StackHasActiveCommands: %v", err)
	}
	if active {
		t.Errorf("StackHasActiveCommands = true, want false")
	}

	// GetResourcesAtLastReconcile: no resources stored, expect empty.
	snaps, err := ds.GetResourcesAtLastReconcile("ttl-stack")
	if err != nil {
		t.Fatalf("GetResourcesAtLastReconcile: %v", err)
	}
	if len(snaps) != 0 {
		t.Errorf("GetResourcesAtLastReconcile = %+v, want empty", snaps)
	}

	// DeletePoliciesForStack tombstones the inline policy; afterwards it's gone.
	if err := ds.DeletePoliciesForStack(stack.ID, "cmd-cleanup"); err != nil {
		t.Fatalf("DeletePoliciesForStack: %v", err)
	}
	pols, err = ds.GetPoliciesForStack(stack.ID)
	if err != nil {
		t.Fatalf("GetPoliciesForStack(after delete): %v", err)
	}
	if len(pols) != 0 {
		t.Errorf("GetPoliciesForStack(after delete) = %+v, want empty", pols)
	}
}
