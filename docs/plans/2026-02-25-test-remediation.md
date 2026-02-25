# Test Remediation Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Improve mutation scores for `forma_persister` (59.7% → ~85%) and `datastore` (35% → ~45%) by targeting surviving mutant clusters identified in the 2026-02-24 mutation report.

**Architecture:** Three types of changes: (1) production code fixes for clientID consistency, (2) Ergo unit tests for `FormaCommandPersister` actor behaviour, (3) direct datastore tests for round-trip correctness and edge cases.

**Tech Stack:** Go, Ergo actor framework (`ergo.services/ergo/testing/unit`), SQLite in-memory, testify/assert, `go test -tags=unit`.

---

## Background

### Key files
- `internal/metastructure/forma_persister/forma_command_persister.go` — actor implementation
- `internal/metastructure/forma_persister/forma_command_persister_test.go` — existing actor tests
- `internal/metastructure/datastore/sqlite.go` — SQLite persistence layer
- `internal/metastructure/datastore/datastore_test.go` — existing datastore tests
- `internal/metastructure/synchronizer.go:298` — passes `clientID = ""`
- `internal/metastructure/discovery/discovery.go:683` — passes `clientID = "formae"`

### How forma_persister tests work

Tests use the Ergo unit test framework. All tests follow this pattern:

```go
func TestFormaCommandPersister_Something(t *testing.T) {
    formaCommand := newFormaCommandWithCreateResourceUpdate() // or newSyncFormaCommand()
    formaPersister, sender, err := newFormaCommandPersisterForTest(t)
    assert.NoError(t, err)

    // Store the command
    storeResult := formaPersister.Call(sender, StoreNewFormaCommand{Command: *formaCommand})
    assert.NoError(t, storeResult.Error)

    // Send messages and assert responses
    result := formaPersister.Call(sender, SomeMessage{...})
    assert.NoError(t, result.Error)
    assert.True(t, result.Response.(bool))
}
```

`newFormaCommandPersisterForTest` creates an in-memory SQLite datastore and spawns the actor with it. The `sender` is a fake PID. `formaPersister.Call(sender, msg)` is synchronous.

### How datastore tests work

```go
func TestDatastore_Something(t *testing.T) {
    if ds, err := prepareDatastore(); err == nil {
        defer cleanupDatastore(ds)
        // ... test code ...
    } else {
        t.Fatalf("Failed to prepare datastore: %v\n", err)
    }
}
```

`prepareDatastore()` creates an in-memory SQLite datastore by default (runs as unit test). All datastore tests are in `package datastore` (internal package tests).

### Type aliases
`resource_update.ResourceUpdateState*` constants are re-exports of `types.ResourceUpdateState*` — they are the same type and interchangeable.

---

## Task 1: Fix clientID strings

**Files:**
- Modify: `internal/metastructure/synchronizer.go:298`
- Modify: `internal/metastructure/discovery/discovery.go:683`

**Motivation:** Synchronizer passes `clientID = ""` (empty, untestable). Discovery passes `clientID = "formae"` (wrong — should reflect the actor, not the product). Fixing these enables round-trip tests to kill the `if clientID.Valid` mutation in `sqlite.go:260`.

**Step 1: Fix synchronizer clientID**

In `internal/metastructure/synchronizer.go`, find the line `"",` that is the last argument in the `CreateFormaCommand` or similar call around line 298. Change the `""` to `"synchronizer"`.

Confirm the context looks like:
```go
pkgmodel.CommandSync,
allResourceUpdates,
nil, // No target updates on sync
nil, // No stack updates on sync
nil, // No policy updates on sync
"synchronizer",  // was ""
```

**Step 2: Fix discovery clientID**

In `internal/metastructure/discovery/discovery.go`, find the assignment `clientID = "formae"` around line 683. Change to `clientID = "discovery"`.

**Step 3: Run tests to verify no breakage**

```bash
go test -tags=unit ./internal/metastructure/... -count=1
```

Expected: All tests pass. No test asserts on the specific clientID value `"formae"` or `""` (if any do, update them to the new values).

**Step 4: Commit**

```bash
git add internal/metastructure/synchronizer.go internal/metastructure/discovery/discovery.go
git commit -m "fix: standardize clientID strings for synchronizer and discovery actors"
```

---

## Task 2: isResourceInFinalState unit tests

**Files:**
- Modify: `internal/metastructure/forma_persister/forma_command_persister_test.go`

**Motivation:** `isResourceInFinalState` has 4 LIVED mutations (lines 77–80). Each state is checked independently but no test exercises them individually. Mutations flip one condition at a time; only a test asserting each state separately will kill them.

**Step 1: Write the failing tests**

Add these tests to `forma_command_persister_test.go` (after the existing test functions, before the helpers):

```go
func TestIsResourceInFinalState_Success(t *testing.T) {
	assert.True(t, isResourceInFinalState(resource_update.ResourceUpdateStateSuccess))
}

func TestIsResourceInFinalState_Failed(t *testing.T) {
	assert.True(t, isResourceInFinalState(resource_update.ResourceUpdateStateFailed))
}

func TestIsResourceInFinalState_Rejected(t *testing.T) {
	assert.True(t, isResourceInFinalState(resource_update.ResourceUpdateStateRejected))
}

func TestIsResourceInFinalState_Canceled(t *testing.T) {
	assert.True(t, isResourceInFinalState(resource_update.ResourceUpdateStateCanceled))
}

func TestIsResourceInFinalState_NotStarted_ReturnsFalse(t *testing.T) {
	assert.False(t, isResourceInFinalState(resource_update.ResourceUpdateStateNotStarted))
}
```

**Step 2: Run tests to verify they compile and pass**

```bash
go test -tags=unit ./internal/metastructure/forma_persister/... -run TestIsResourceInFinalState -v
```

Expected:
```
--- PASS: TestIsResourceInFinalState_Success
--- PASS: TestIsResourceInFinalState_Failed
--- PASS: TestIsResourceInFinalState_Rejected
--- PASS: TestIsResourceInFinalState_Canceled
--- PASS: TestIsResourceInFinalState_NotStarted_ReturnsFalse
```

**Step 3: Commit**

```bash
git add internal/metastructure/forma_persister/forma_command_persister_test.go
git commit -m "test(forma_persister): add per-state tests for isResourceInFinalState"
```

---

## Task 3: Progress update with properties and version

**Files:**
- Modify: `internal/metastructure/forma_persister/forma_command_persister_test.go`

**Motivation:** Lines 327, 331, 335, 469, 472 in `forma_command_persister.go` have LIVED mutations on:
- `if progress.ResourceProperties != nil` (line 327 in `updateCommandFromProgress`)
- `if progress.ResourceReadOnlyProperties != nil` (line 331)
- `if progress.Version != ""` (line 335)
- `if msg.ResourceProperties != nil` (line 469 in `markResourceUpdateAsComplete`)
- `if msg.ResourceReadOnlyProperties != nil` (line 472)

The existing `TestFormaCommandPersister_RecordsResourceProgress` sends nil properties and empty version, so mutations that flip these conditions are equivalent (nil assignment = no change). We need tests that send non-nil values and assert they're preserved.

**Step 1: Write the failing test**

Add after the existing `TestFormaCommandPersister_RecordsResourceProgress` test:

```go
func TestFormaCommandPersister_ProgressUpdate_PreservesPropertiesAndVersion(t *testing.T) {
	formaCommand := newFormaCommandWithCreateResourceUpdate()
	formaPersister, sender, err := newFormaCommandPersisterForTest(t)
	assert.NoError(t, err)

	storeResult := formaPersister.Call(sender, StoreNewFormaCommand{Command: *formaCommand})
	assert.NoError(t, storeResult.Error)

	resourceURI := formaCommand.ResourceUpdates[0].DesiredState.URI()
	updatedProperties := json.RawMessage(`{"updated":"true","key":"new-value"}`)
	updatedReadOnlyProperties := json.RawMessage(`{"arn":"arn:aws:iam::123:role/test"}`)

	// Send progress with non-nil properties and a version
	updateProgress := messages.UpdateResourceProgress{
		CommandID:                formaCommand.ID,
		ResourceURI:              resourceURI,
		Operation:                resource_update.OperationCreate,
		ResourceStartTs:          util.TimeNow(),
		ResourceModifiedTs:       util.TimeNow().Add(10 * time.Second),
		ResourceState:            resource_update.ResourceUpdateStateInProgress,
		ResourceProperties:       updatedProperties,
		ResourceReadOnlyProperties: updatedReadOnlyProperties,
		Version:                  "v-abc123",
		Progress: plugin.TrackedProgress{
			ProgressResult: resource.ProgressResult{
				Operation:       resource.OperationCreate,
				OperationStatus: resource.OperationStatusInProgress,
			},
		},
	}
	res := formaPersister.Call(sender, updateProgress)
	assert.NoError(t, res.Error)

	// Load and assert fields were updated
	loadResult := formaPersister.Call(sender, LoadFormaCommand{CommandID: formaCommand.ID})
	assert.NoError(t, loadResult.Error)
	loaded, ok := loadResult.Response.(*forma_command.FormaCommand)
	assert.True(t, ok)

	assert.Equal(t, updatedProperties, loaded.ResourceUpdates[0].DesiredState.Properties)
	assert.Equal(t, updatedReadOnlyProperties, loaded.ResourceUpdates[0].DesiredState.ReadOnlyProperties)
	assert.Equal(t, "v-abc123", loaded.ResourceUpdates[0].Version)
}
```

**Step 2: Run to verify it fails (before it should pass — check it compiles at least)**

```bash
go test -tags=unit ./internal/metastructure/forma_persister/... -run TestFormaCommandPersister_ProgressUpdate_PreservesPropertiesAndVersion -v
```

Expected: PASS (existing code already implements these assignments correctly; the test just wasn't there to kill the mutations).

**Step 3: Add test for markResourceUpdateAsComplete with properties**

```go
func TestFormaCommandPersister_Completion_PreservesProperties(t *testing.T) {
	formaCommand := newFormaCommandWithCreateResourceUpdate()
	formaPersister, sender, err := newFormaCommandPersisterForTest(t)
	assert.NoError(t, err)

	storeResult := formaPersister.Call(sender, StoreNewFormaCommand{Command: *formaCommand})
	assert.NoError(t, storeResult.Error)

	resourceURI := formaCommand.ResourceUpdates[0].DesiredState.URI()
	finalProperties := json.RawMessage(`{"final":"state","count":42}`)
	finalReadOnlyProperties := json.RawMessage(`{"arn":"arn:aws:iam::999:role/final"}`)

	markComplete := messages.MarkResourceUpdateAsComplete{
		CommandID:                  formaCommand.ID,
		ResourceURI:                resourceURI,
		Operation:                  formaCommand.ResourceUpdates[0].Operation,
		FinalState:                 resource_update.ResourceUpdateStateSuccess,
		ResourceStartTs:            util.TimeNow(),
		ResourceModifiedTs:         util.TimeNow().Add(30 * time.Second),
		ResourceProperties:         finalProperties,
		ResourceReadOnlyProperties: finalReadOnlyProperties,
		Version:                    "final-version-hash",
	}
	res := formaPersister.Call(sender, markComplete)
	assert.NoError(t, res.Error)
	assert.True(t, res.Response.(bool))

	// Command is now in final state and persisted; load it back
	loadResult := formaPersister.Call(sender, LoadFormaCommand{CommandID: formaCommand.ID})
	assert.NoError(t, loadResult.Error)
	loaded, ok := loadResult.Response.(*forma_command.FormaCommand)
	assert.True(t, ok)

	assert.Equal(t, finalProperties, loaded.ResourceUpdates[0].DesiredState.Properties)
	assert.Equal(t, finalReadOnlyProperties, loaded.ResourceUpdates[0].DesiredState.ReadOnlyProperties)
	assert.Equal(t, "final-version-hash", loaded.ResourceUpdates[0].Version)
}
```

**Step 4: Run full test suite for the package**

```bash
go test -tags=unit ./internal/metastructure/forma_persister/... -v
```

Expected: All tests pass.

**Step 5: Commit**

```bash
git add internal/metastructure/forma_persister/forma_command_persister_test.go
git commit -m "test(forma_persister): add tests for properties and version preservation in progress/completion"
```

---

## Task 4: Remove pendingCompletions guard, add error on underflow

**Files:**
- Modify: `internal/metastructure/forma_persister/forma_command_persister.go`
- Modify: `internal/metastructure/forma_persister/forma_command_persister_test.go`

**Motivation:** Lines 508–510, 565–566 have LIVED mutations on `if cached.pendingCompletions > 0`. The guard was intended to prevent the counter going negative but this silently swallows bugs. The fix: remove the guard, make the decrement unconditional, and add an explicit error when the counter underflows. This:
1. Eliminates the mutated code (the guard ceases to exist)
2. Makes underflow detectable by tests

**Important:** `pendingCompletions` is used in `shouldDeleteSyncCommand` (line 658: `allCompleted := cached.pendingCompletions == 0`) and in the cache eviction check (line 180: `cached.pendingCompletions > 0`). Underflow (going negative) would break `shouldDeleteSyncCommand`. The explicit error prevents this from happening silently.

**Step 1: Write the failing test first**

Add to `forma_command_persister_test.go`:

```go
func TestFormaCommandPersister_DuplicateCompletion_ReturnsError(t *testing.T) {
	formaCommand := newFormaCommandWithCreateResourceUpdate()
	formaPersister, sender, err := newFormaCommandPersisterForTest(t)
	assert.NoError(t, err)

	// Store command with 1 resource update
	storeResult := formaPersister.Call(sender, StoreNewFormaCommand{Command: *formaCommand})
	assert.NoError(t, storeResult.Error)

	resourceURI := formaCommand.ResourceUpdates[0].DesiredState.URI()
	markComplete := messages.MarkResourceUpdateAsComplete{
		CommandID:          formaCommand.ID,
		ResourceURI:        resourceURI,
		Operation:          formaCommand.ResourceUpdates[0].Operation,
		FinalState:         resource_update.ResourceUpdateStateSuccess,
		ResourceStartTs:    util.TimeNow(),
		ResourceModifiedTs: util.TimeNow().Add(10 * time.Second),
		Version:            "v1",
	}

	// First completion: valid, should succeed
	res1 := formaPersister.Call(sender, markComplete)
	assert.NoError(t, res1.Error)

	// Second completion for the same resource: unexpected duplicate.
	// After the first completion the command reaches final state and is evicted from cache.
	// On reload, pendingCompletions = countNonFinalResources = 0 (already final).
	// Unconditional decrement would make it -1, which must return an error.
	res2 := formaPersister.Call(sender, markComplete)
	assert.Error(t, res2.Error, "duplicate completion should return an error")
}
```

**Step 2: Run test to verify it FAILS (error not yet returned)**

```bash
go test -tags=unit ./internal/metastructure/forma_persister/... -run TestFormaCommandPersister_DuplicateCompletion_ReturnsError -v
```

Expected: FAIL — `res2.Error` is nil (current code silently ignores the duplicate).

**Step 3: Implement — remove guard in markResourceUpdateAsComplete**

In `forma_command_persister.go`, find the block around line 508:

```go
// OLD — remove this guard:
if cached.pendingCompletions > 0 {
    cached.pendingCompletions--
}
```

Replace with:

```go
cached.pendingCompletions--
if cached.pendingCompletions < 0 {
    return false, fmt.Errorf("unexpected completion for command %s resource %s: pendingCompletions went below zero, this indicates a programming error", msg.CommandID, msg.ResourceURI.KSUID())
}
```

**Step 4: Implement — remove guard in bulkUpdateResourceState**

Find the block around line 565:

```go
// OLD — remove this guard:
if cached.pendingCompletions > 0 {
    cached.pendingCompletions--
}
```

Replace with:

```go
cached.pendingCompletions--
if cached.pendingCompletions < 0 {
    return false, fmt.Errorf("unexpected state transition for command %s: pendingCompletions went below zero, this indicates a programming error", commandID)
}
```

**Step 5: Run the new test to verify it now PASSES**

```bash
go test -tags=unit ./internal/metastructure/forma_persister/... -run TestFormaCommandPersister_DuplicateCompletion_ReturnsError -v
```

Expected: PASS.

**Step 6: Run full test suite to verify no regressions**

```bash
go test -tags=unit ./internal/metastructure/forma_persister/... -v
```

Expected: All tests pass.

**Step 7: Commit**

```bash
git add internal/metastructure/forma_persister/forma_command_persister.go internal/metastructure/forma_persister/forma_command_persister_test.go
git commit -m "fix(forma_persister): replace pendingCompletions guard with explicit underflow error"
```

---

## Task 5: Datastore optional field round-trip tests

**Files:**
- Modify: `internal/metastructure/datastore/datastore_test.go`

**Motivation:** Lines 260–273 in `sqlite.go` have LIVED mutations on `if clientID.Valid`, `if descriptionText.Valid`, and `if configMode.Valid`. These survive because existing tests use empty/zero-value fields, so mutations that flip the conditions are equivalent (assigning zero value = not assigning). Tests with non-empty values and explicit assertions kill these mutations.

**Step 1: Write the failing tests**

Add to `datastore_test.go`:

```go
func TestDatastore_StoreAndLoad_FormaCommand_ClientID(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		cmd := &forma_command.FormaCommand{
			ID:       util.NewID(),
			ClientID: "synchronizer",
			Command:  pkgmodel.CommandApply,
			State:    forma_command.CommandStatePending,
			Config:   config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		}

		err := ds.StoreFormaCommand(cmd, cmd.ID)
		assert.NoError(t, err)

		loaded, err := ds.GetFormaCommandByCommandID(cmd.ID)
		assert.NoError(t, err)
		assert.Equal(t, "synchronizer", loaded.ClientID)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_StoreAndLoad_FormaCommand_Description(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		cmd := &forma_command.FormaCommand{
			ID:          util.NewID(),
			Command:     pkgmodel.CommandApply,
			State:       forma_command.CommandStatePending,
			Config:      config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			Description: pkgmodel.Description{Text: "deploy production stack"},
		}

		err := ds.StoreFormaCommand(cmd, cmd.ID)
		assert.NoError(t, err)

		loaded, err := ds.GetFormaCommandByCommandID(cmd.ID)
		assert.NoError(t, err)
		assert.Equal(t, "deploy production stack", loaded.Description.Text)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_StoreAndLoad_FormaCommand_ConfigMode(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		cmd := &forma_command.FormaCommand{
			ID:      util.NewID(),
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStatePending,
			Config:  config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
		}

		err := ds.StoreFormaCommand(cmd, cmd.ID)
		assert.NoError(t, err)

		loaded, err := ds.GetFormaCommandByCommandID(cmd.ID)
		assert.NoError(t, err)
		assert.Equal(t, pkgmodel.FormaApplyModePatch, loaded.Config.Mode)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}
```

Note: you'll need to add `"github.com/platform-engineering-labs/formae/internal/metastructure/config"` to the import block if it's not already there.

**Step 2: Run tests to verify they pass**

```bash
go test -tags=unit ./internal/metastructure/datastore/... -run "TestDatastore_StoreAndLoad_FormaCommand" -v
```

Expected: All three tests PASS (the fields are already persisted correctly; we're adding assertions to expose future mutations).

**Step 3: Commit**

```bash
git add internal/metastructure/datastore/datastore_test.go
git commit -m "test(datastore): add round-trip tests for clientID, description, and configMode fields"
```

---

## Task 6: UpdateTarget not-found test

**Files:**
- Modify: `internal/metastructure/datastore/datastore_test.go`

**Motivation:** Lines 2389–2391 in `sqlite.go` check `if !maxVersion.Valid` to return an error when `UpdateTarget` is called with a non-existent label. No test exercises this path. The existing `TestDatastore_DeleteTarget_NotFound` covers `DeleteTarget` but not `UpdateTarget`.

**Step 1: Write the failing test**

Add to `datastore_test.go` near the existing `TestDatastore_DeleteTarget_NotFound`:

```go
func TestDatastore_UpdateTarget_NotFound_ReturnsError(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		_, err := ds.UpdateTarget(&pkgmodel.Target{
			Label:     "non-existent-target",
			Namespace: "default",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not exist")
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}
```

**Step 2: Run the test**

```bash
go test -tags=unit ./internal/metastructure/datastore/... -run TestDatastore_UpdateTarget_NotFound_ReturnsError -v
```

Expected: PASS.

**Step 3: Commit**

```bash
git add internal/metastructure/datastore/datastore_test.go
git commit -m "test(datastore): add UpdateTarget not-found error test"
```

---

## Task 7: Non-sync command stores resource updates

**Files:**
- Modify: `internal/metastructure/datastore/datastore_test.go`

**Motivation:** Line 190 in `sqlite.go` has `fa.Command != pkgmodel.CommandSync` to skip storing resource_updates for sync commands. The CONDITIONALS_NEGATION mutation flips this so only sync commands store resource updates (non-sync commands would NOT). This mutation survives because no existing test loads a non-sync command back and asserts that its resource updates are present in the DB.

**Step 1: Write the test**

Add to `datastore_test.go`:

```go
func TestDatastore_StoreFormaCommand_NonSync_StoresResourceUpdates(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		resourceKsuid := util.NewID()
		cmd := &forma_command.FormaCommand{
			ID:      util.NewID(),
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStatePending,
			Config:  config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					DesiredState: pkgmodel.Resource{
						Ksuid:      resourceKsuid,
						Label:      "test-resource",
						Type:       "AWS::S3::Bucket",
						Stack:      "default",
						Properties: json.RawMessage(`{"BucketName":"test"}`),
					},
					ResourceTarget: pkgmodel.Target{
						Label:     "aws-target",
						Namespace: "AWS",
						Config:    json.RawMessage(`{}`),
					},
					Operation: resource_update.OperationCreate,
					State:     resource_update.ResourceUpdateStateNotStarted,
				},
			},
		}

		err := ds.StoreFormaCommand(cmd, cmd.ID)
		assert.NoError(t, err)

		// Load back and verify resource updates were persisted
		loaded, err := ds.GetFormaCommandByCommandID(cmd.ID)
		assert.NoError(t, err)
		assert.Len(t, loaded.ResourceUpdates, 1, "non-sync command should have resource updates stored in DB")
		assert.Equal(t, resourceKsuid, loaded.ResourceUpdates[0].DesiredState.Ksuid)
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}

func TestDatastore_StoreFormaCommand_Sync_SkipsResourceUpdates(t *testing.T) {
	if ds, err := prepareDatastore(); err == nil {
		defer cleanupDatastore(ds)

		resourceKsuid := util.NewID()
		cmd := &forma_command.FormaCommand{
			ID:      util.NewID(),
			Command: pkgmodel.CommandSync,
			State:   forma_command.CommandStatePending,
			Config:  config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					DesiredState: pkgmodel.Resource{
						Ksuid:      resourceKsuid,
						Label:      "test-resource",
						Type:       "AWS::S3::Bucket",
						Stack:      "default",
						Properties: json.RawMessage(`{"BucketName":"test"}`),
					},
					ResourceTarget: pkgmodel.Target{
						Label:     "aws-target",
						Namespace: "AWS",
						Config:    json.RawMessage(`{}`),
					},
					Operation: resource_update.OperationRead,
					State:     resource_update.ResourceUpdateStateNotStarted,
				},
			},
		}

		err := ds.StoreFormaCommand(cmd, cmd.ID)
		assert.NoError(t, err)

		// Load back and verify resource updates were NOT stored (sync optimization)
		loaded, err := ds.GetFormaCommandByCommandID(cmd.ID)
		assert.NoError(t, err)
		assert.Empty(t, loaded.ResourceUpdates, "sync command resource updates should not be stored upfront")
	} else {
		t.Fatalf("Failed to prepare datastore: %v\n", err)
	}
}
```

**Step 2: Run the tests**

```bash
go test -tags=unit ./internal/metastructure/datastore/... -run "TestDatastore_StoreFormaCommand_NonSync|TestDatastore_StoreFormaCommand_Sync" -v
```

Expected: Both PASS.

**Step 3: Run the full datastore test suite**

```bash
go test -tags=unit ./internal/metastructure/datastore/... -v
```

Expected: All tests pass.

**Step 4: Commit**

```bash
git add internal/metastructure/datastore/datastore_test.go
git commit -m "test(datastore): add tests for sync/non-sync resource update storage behaviour"
```

---

## Final verification

After all tasks, run the full unit test suite:

```bash
go test -tags=unit ./internal/... -count=1
```

Expected: All tests pass. No regressions in packages that were already at 80%+.

Optionally, re-run mutation tests on the two target packages to verify score improvement:

```bash
./scripts/mutation-test.sh internal/metastructure/forma_persister
./scripts/mutation-test.sh internal/metastructure/datastore
./scripts/generate-mutation-report.sh
```
