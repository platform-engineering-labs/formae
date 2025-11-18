# Azure Plugin Development Plan

**Branch:** `feat/plugin-sdk-testing` (switched from `feat/azure-plugin` on 2025-11-07)
**Start Date:** 2025-10-31
**Last Updated:** 2025-11-07
**Status:** 🟡 In Progress - Integrating with Blackbox Testing Framework

---

## Branch Strategy & Repository Workflow

### Repository Context

This is the **internal repository** (`formae-internal`). The workflow is:
1. Development happens on feature branches in internal repo
2. PRs are created and reviewed in internal repo
3. **Once approved, changes are pushed to the public repo** (`formae`)
4. PRs are merged **in the public repo**
5. An automated job syncs main from public back to internal
6. **Internal main should never have direct merges** (would be overwritten by sync)

### Branch History

**Initial Development (`feat/azure-plugin`):**
- Sessions 1-3 (Oct 31 - Nov 3): PKL schemas, TDD setup, CRUD operations
- Implemented Create, Read, Update, Delete, Status for Resource Groups
- All Go-level integration tests passing
- **Branch status:** Has Azure implementation but lacks blackbox testing framework

**Testing Framework (`feat/plugin-sdk-testing`):**
- PR #3: https://github.com/platform-engineering-labs/formae-internal/pull/3
- Blackbox testing framework for provider-agnostic plugin validation
- Tests via CLI (apply/destroy/inventory) not direct plugin calls
- Adds `Extractable` field to `model.Schema`
- **Branch status:** Expected to merge to public within hours (Nov 7)

### Current Strategy (Nov 7)

**Decision:** Switch to `feat/plugin-sdk-testing` branch and cherry-pick Azure commits

**Rationale:**
1. Testing framework (PR #3) will merge to public imminently
2. Azure plugin won't be merged for weeks/months (POC stage)
3. We need the testing framework to validate Azure implementation
4. Avoid merge commits that would create messy history when syncing from public

**Implementation Steps:**
1. ✅ Document strategy in this plan
2. ✅ Switch to `feat/plugin-sdk-testing` branch
3. ✅ Cherry-pick Azure commits from `feat/azure-plugin`:
   - fccb908: PKL schemas
   - 346a5a7: Create operation
   - c7daf59: Read operation
   - e69e2d4: Delete + Status operations
   - cb7cf3a: Update operation
4. Continue Azure development on `feat/plugin-sdk-testing`
5. Use blackbox tests to validate Azure implementation

**Future Path:**
- Testing framework merges to public → syncs to internal main (hours)
- Azure continues development on `feat/plugin-sdk-testing` (weeks)
- When Azure is ready: Create new branch from updated main, cherry-pick Azure commits
- Submit Azure as separate PR to public repo
- Both features will be in public independently

**Why Not Wait for Testing Framework to Merge?**
- Could wait for merge + sync, then rebase `feat/azure-plugin` on new main
- But: We can start testing immediately by working on `feat/plugin-sdk-testing` now
- Time saved: ~1-2 hours (no waiting for merge/sync/rebase cycle)
- Risk: Minimal - testing framework is approved and stable

---

## Current Phase: Blackbox Testing Integration

### Immediate Tasks

1. **Set Extractable Flag**
   - Add `Extractable: false` to `ResourceGroupSchema` (to start)
   - This will skip extract validation in lifecycle test initially

2. **Create PKL Test File**
   - Location: `plugins/azure/testdata/resource-group.pkl`
   - Must include Stack, Target, and ResourceGroup resource
   - Must use unique names to avoid conflicts

3. **Run Lifecycle Test**
   - Command: `PLUGIN_NAME=azure go test -tags=plugin_sdk -v -run TestPluginSDK_Lifecycle ./tests/integration/plugin-sdk/`
   - Expected to test: Create → Read → Sync → Update → Destroy

4. **Implement List Operation**
   - Required for discovery test
   - Use Azure pager pattern to list all resource groups

5. **Run Discovery Test**
   - Command: `PLUGIN_NAME=azure go test -tags=plugin_sdk -v -run TestPluginSDK_Discovery ./tests/integration/plugin-sdk/`
   - Tests that unmanaged resources can be discovered

### What's Working (From feat/azure-plugin)

✅ **Create:** Synchronous resource group creation with tags
✅ **Read:** Retrieves resource properties from Azure  
✅ **Update:** Updates resource group tags
✅ **Delete:** Async deletion with status polling
✅ **Status:** Polls Azure for completion, returns error codes
✅ **Error Mapping:** Comprehensive Azure-to-OperationErrorCode translation
✅ **Go-Level Integration Tests:** All 3 tests passing

### What's NOT Implemented

❌ **List Operation** - Required for discovery test
❌ **PKL Test File** - Required for blackbox tests
❌ **Extractable Flag** - Needs to be set in Schema
❌ **Blackbox Tests** - Haven't run yet

---

## Testing Strategy

### Blackbox Testing Framework

**Location:** `tests/integration/plugin-sdk/`

**Key Files:**
- `plugin_sdk_test.go` - Main test orchestrator
- `framework/harness.go` - Test harness and CLI wrappers
- `framework/discovery.go` - Test case discovery

**Test Discovery:**
- Framework automatically finds all `.pkl` files in `plugins/{PLUGIN_NAME}/testdata/`
- Each PKL file becomes a test case
- One agent instance shared across all tests

**Lifecycle Test Flow (15 steps):**
1. Eval PKL - Parse expected properties
2. Apply (Create) - Create resource via CLI
3. Poll Status - Wait for completion
4. Verify in Inventory - Check resource exists
5. Extract (if Extractable=true) - Export back to PKL
6. Force Sync - Trigger synchronization
7. Wait for Sync - Poll completion
8. Verify Idempotency - No changes after sync
9. Create Patch File - Add Environment=test tag
10. Apply (Update) - Apply patch
11. Poll Patch Status - Wait for completion
12. Verify Tag Added - Check tag exists
13. Destroy - Delete resource
14. Poll Destroy Status - Wait for completion
15. Verify Deletion - Confirm gone from inventory

**Discovery Test Flow (10 steps):**
1. Eval PKL - Understand resource structure
2. Configure Discovery - Enable for resource type
3. Start Agent - With discovery enabled
4. Create Unmanaged - Via plugin directly
5. Trigger Discovery - Call admin endpoint
6. Wait for Completion - Poll logs
7. Query Inventory - Search for unmanaged
8. Verify Discovered - Confirm found
9. Verify Properties - Check correctness
10. Cleanup - Delete via plugin

---

## File Locations

**Plugin Code:**
- `plugins/azure/azure.go` - Main plugin
- `plugins/azure/pkg/resources/resourcegroup.go` - ResourceGroup provisioner
- `plugins/azure/pkg/client/client.go` - Azure client wrapper
- `plugins/azure/pkg/config/config.go` - Config + auth
- `plugins/azure/pkg/registry/registry.go` - Resource registry

**PKL Schemas:**
- `plugins/azure/schema/pkl/azure.pkl` - Base module with Config
- `plugins/azure/schema/pkl/resources/resourcegroup.pkl` - ResourceGroup schema
- `plugins/pkl/testdata/forma/azure_test.pkl` - Test forma file (old style)
- `plugins/azure/testdata/resource-group.pkl` - **TO CREATE** for blackbox tests

**Tests:**
- `plugins/azure/azure_integration_test.go` - Go-level integration tests (still useful)
- `tests/integration/plugin-sdk/` - Blackbox testing framework

**Documentation:**
- `.plans/azure-plugin-development.md` - This file
- `.handoffs/` - Session handoff documents (on feat/azure-plugin branch)

---

## Next Steps

1. **Set Extractable: false** in ResourceGroupSchema
2. **Create testdata/resource-group.pkl** with Stack + Target + Resource
3. **Build formae:** `make build`
4. **Run lifecycle test:** `PLUGIN_NAME=azure go test -tags=plugin_sdk -v -run TestPluginSDK_Lifecycle ./tests/integration/plugin-sdk/`
5. **Fix any issues** found by lifecycle test
6. **Implement List()** operation
7. **Run discovery test:** `PLUGIN_NAME=azure go test -tags=plugin_sdk -v -run TestPluginSDK_Discovery ./tests/integration/plugin-sdk/`
8. **Fix any issues** found by discovery test
9. **Update plan** with learnings and next resource type to implement

---

**Last Updated:** 2025-11-07
**Status:** Cherry-pick complete, ready to integrate with blackbox testing
