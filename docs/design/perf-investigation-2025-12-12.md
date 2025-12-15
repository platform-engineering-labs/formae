# Performance Investigation Summary - 2025-12-12

## Context
Performance testing with 500 resources revealed several issues after database schema normalization (migrations 00003-00005) that moved `ResourceUpdates` from JSON blob to normalized `resource_updates` table.

## Issues Identified

### 1. Orphaned resource_updates Entries (CRITICAL)

**Symptom:**
```
ERR Failed to load Forma command for complete update ... forma command not found: [commandID]
ERR process terminated abnormally - failed to load Forma command for complete update
```

**Root Cause:** `resource_updates` entries exist without corresponding `forma_commands` entries.

**Investigation:**
- Initial fix added to `DeleteFormaCommand` in both `sqlite.go` and `postgres.go` to delete from `resource_updates` before `forma_commands`
- However, orphans still appearing during sync commands
- Possible race condition: sync command deleted while resource updates still processing

**Affected Files:**
- `internal/metastructure/datastore/sqlite.go:439` - DeleteFormaCommand
- `internal/metastructure/datastore/postgres.go:443` - DeleteFormaCommand
- `internal/metastructure/forma_persister/forma_command_persister.go:165` - shouldDeleteSyncCommand

**Status:** Under investigation - need clean DB test to isolate migration vs runtime issues

---

### 2. Empty Property Path in ResolveCache (ROOT CAUSE FOUND - TEST DATA)

**Symptom:**
```
ERR Unable to resolve property  in cached properties for resource formae://[ksuid]#
```
Note the empty property name (two spaces between "property" and "in").

**Root Cause (CONFIRMED):** Test data generator creates a reference without property path.

**The Bug:**
In `scripts/generate-perf-test-env.py` line 622:
```python
targetKeyId = kmsKey{i}.res       # WRONG - no property path
```
Should be:
```python
targetKeyId = kmsKey{i}.res.arn   # CORRECT - has property path
```

**Why only KMS aliases affected:**
All other resolvables in the test generator correctly use `.res.id` or `.res.arn`:
- `vpc{i}.res.id`
- `igw{i}.res.id`
- `subnet{i}.res.id`
- `iam.lambdaRoles.toList()[...].res.arn`

Only the KMS alias `targetKeyId` used `.res` without a property.

**Fix Applied:**
Changed line 622 from `kmsKey{i}.res` to `kmsKey{i}.res.arn`

**Note:** The initial research agent incorrectly identified this as a bug in `changeset_executor.go`.
The actual issue was in the test data generator - the formae codebase correctly requires property paths.

---

### 3. Plugin Operator Missing in Action

**Symptom:**
```
ERR Plugin operator is missing in action (state='creating')
ERR PluginOperator: failed to send status result: unknown process
```

**Root Cause:** Race condition between two timeouts:
- `PluginOperationCallTimeout` = 60 seconds (hardcoded in resource_updater.go:115)
- State timeout = `StatusCheckInterval * 2` (much shorter)

**Flow:**
1. ResourceUpdater calls PluginOperator with 60s timeout
2. ResourceUpdater sets state timeout to `StatusCheckInterval * 2`
3. State timeout fires first (if StatusCheckInterval is small)
4. ResourceUpdater marks as failed and terminates
5. PluginOperator eventually responds to dead process

**Fix:** Align state timeout with PluginOperationCallTimeout (e.g., 90 seconds buffer)

**Affected Files:**
- `internal/metastructure/resource_update/resource_updater.go:115` - PluginOperationCallTimeout
- `internal/metastructure/resource_update/resource_updater.go:496-501` - State timeout setup

---

### 4. AWS Infrastructure Errors (Test Data Issues - Not Formae Bugs)

These errors are AWS configuration/dependency issues in the generated test data:

#### 4a. Lambda Role Trust Policy
**Symptom:**
```
The role defined for the function cannot be assumed by Lambda
```
**Cause:** IAM roles for Lambda functions don't have `lambda.amazonaws.com` as a trusted principal in their trust policy.

**Investigation Result (from test data agent):**
The test generator at `scripts/generate-perf-test-env.py` lines 438-466 creates roles with random service principals:
```python
services = ["lambda.amazonaws.com", "ec2.amazonaws.com", "ecs-tasks.amazonaws.com",
            "sagemaker.amazonaws.com", "states.amazonaws.com"]
```
Only 20% of roles get `lambda.amazonaws.com`. Lambda functions at line 849 are assigned roles without checking trust policy:
```python
role = iam.roles.toList()[{i % max(1, int(counts.lambdas * 0.1))}].res.arn
```
This means a Lambda function could get a role that only trusts `ec2.amazonaws.com`.

**Fix Options:**
1. Create dedicated Lambda execution roles with `lambda.amazonaws.com` trust policy
2. Adjust role distribution and assign Lambda functions only to Lambda-trusted roles
3. Add all service principals to each role's trust policy (less realistic but works for testing)

#### 4b. Route Table / Gateway Network Mismatch
**Symptom:**
```
route table rtb-xxx and network gateway igw-xxx belong to different networks
```
**Cause:** Routes reference internet gateways from different VPCs.

**Fix:** Ensure routes only reference gateways attached to the same VPC as the route table.

#### 4c. IAM Deletion Order (Destroy Phase)
**Symptom:**
```
Cannot delete entity, must remove roles from instance profile first
Cannot delete a policy attached to entities
```
**Cause:** Dependency graph doesn't model AWS deletion constraints:
- Instance profiles must be deleted before the roles they contain
- Policy attachments must be removed before policies can be deleted

**Fix:** Update test data generator to model these dependencies:
- InstanceProfile → Role (so role deletes after profile)
- RolePolicyAttachment → Policy (so policy deletes after detachment)

#### 4d. SecretsManager Not Found
**Symptom:**
```
ResourceNotFoundException: Secrets Manager can't find the specified secret
```
**Cause:** Secret already deleted or never created successfully.

**Status:** Minor - expected during cleanup of partially failed resources.

---

## Current Status

### Latest Run (18:06 - Cleanup)
Running destroy to clean up resources before PostgreSQL testing. Errors are all expected AWS dependency issues:

1. **`Cannot delete a policy attached to entities`** - IAM policies (4c)
2. **`Cannot delete entity, must remove roles from instance profile first`** - Instance profiles (4c)
3. **`context canceled`** - Expected from Ctrl+C interrupt

**No orphaned resource_updates errors** - The fix from previous sessions is holding.

### Previous Run (750 resources with SQLite) - FAILED
- No orphaned resource_updates errors ✅
- No empty property path errors ✅
- No Lambda trust policy errors ✅
- **SQLite "database is locked" errors** - Too much concurrent write pressure

**Key observation:** The orphan issue (`forma command not found`) occurred during sync, not during destroy. This suggests the issue is specific to the sync command cleanup path (`shouldDeleteSyncCommand`).

---

## Testing Plan

1. Destroy all resources in cloud (**IN PROGRESS**)
2. Wipe database (`~/.pel/formae/data/formae.db`)
3. Rebuild binary with `make build`
4. Start agent with fresh DB
5. Run 500 resource test again
6. Compare errors - isolates migration issues from runtime bugs

## Key Files Modified (Uncommitted)

- `internal/metastructure/datastore/sqlite.go`
- `internal/metastructure/datastore/postgres.go`
- `internal/metastructure/forma_persister/forma_command_persister.go`
- `internal/metastructure/resource_persister/resource_persister.go`
- Migration files in `migrations_sqlite/` and `migrations_postgres/`

## Commands Reference

```bash
# Check for orphaned resource_updates
sqlite3 ~/.pel/formae/data/formae.db "SELECT DISTINCT ru.command_id FROM resource_updates ru LEFT JOIN forma_commands fc ON ru.command_id = fc.command_id WHERE fc.command_id IS NULL"

# Count orphans for a specific command
sqlite3 ~/.pel/formae/data/formae.db "SELECT COUNT(*) FROM resource_updates WHERE command_id = '[commandID]'"

# Delete orphaned entries
sqlite3 ~/.pel/formae/data/formae.db "DELETE FROM resource_updates WHERE command_id IN ('[id1]', '[id2]')"

# Run tests
make test-all
go test -tags=integration ./internal/metastructure/datastore/...
```
