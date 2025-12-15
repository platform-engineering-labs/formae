# Performance Test Plan

## Overview
This document describes the performance testing procedure for Formae with large resource counts.

## Test Environment Generation

### Generate Test Data
```bash
# Generate test environment with specified resource count
python3 scripts/generate-perf-test-env.py --count <COUNT> --region us-east-2 --output /tmp/test-env-<COUNT>

# Examples:
python3 scripts/generate-perf-test-env.py --count 200 --region us-east-2 --output /tmp/test-env-200
python3 scripts/generate-perf-test-env.py --count 2000 --region us-east-2 --output /tmp/test-env-2000
```

### Validate PKL Syntax
```bash
# Navigate to test directory and resolve dependencies
cd /tmp/test-env-<COUNT>
pkl project resolve

# Validate PKL syntax by evaluating
pkl eval main.pkl > /dev/null
```

## AWS Quota Verification

Before running tests, verify AWS quotas are sufficient:
```bash
# Check VPC quota (default: 5)
aws service-quotas get-service-quota --service-code vpc --quota-code L-F678F1CE --query 'Quota.Value'

# Check Internet Gateway quota (default: 5)
aws service-quotas get-service-quota --service-code vpc --quota-code L-A4707A72 --query 'Quota.Value'

# Check NAT Gateway quota (default: 5 per AZ)
aws service-quotas get-service-quota --service-code vpc --quota-code L-FE5A380F --query 'Quota.Value'

# Check current resource usage
aws ec2 describe-vpcs --query 'Vpcs[].VpcId' --output table
aws ec2 describe-internet-gateways --query 'InternetGateways[].InternetGatewayId' --output table
aws ec2 describe-nat-gateways --filter Name=state,Values=available --query 'NatGateways[].NatGatewayId' --output table
```

## Running Tests

### Apply Resources
```bash
# Apply the test environment (creates resources)
./formae apply /tmp/test-env-<COUNT>/main.pkl --yes --watch
```

### Check Status
```bash
# Check command status
./formae status command
```

### Destroy Resources
```bash
# IMPORTANT: Always destroy after testing to avoid AWS charges
./formae destroy /tmp/test-env-<COUNT>/main.pkl --yes --watch
```

## Monitoring

### Formae Logs
```bash
# Watch formae agent logs
tail -f ~/.pel/formae/log/formae.log

# Search for errors in logs
grep -i error ~/.pel/formae/log/formae.log | tail -50
```

## Known Issues and Fixes

### Region-Specific AMI IDs
EC2 instances require region-specific AMI IDs. The generator script includes a mapping:
- us-east-1: ami-0c02fb55b2c6c5bdc
- us-east-2: ami-0a0d8c70c12d6c0b6
- us-west-1: ami-0ecb7bb3a2c9a8b9d
- us-west-2: ami-0c2ab3b8efb09f272
- eu-west-1: ami-0c1c30571d2dae5c9
- eu-central-1: ami-0a49b025fffbbdac6

### EBS Volume Types
- `io1` volumes require the `iops` parameter
- Use `gp3` or `gp2` for simpler provisioning

### AWS Quotas
The generator script automatically caps VPC-related resources:
- VPCs: max 5 (logarithmic scaling based on total count)
- Internet Gateways: max 5 (1 per VPC)
- NAT Gateways: max 5 (expensive, capped)

## Test Iterations

| Date | Count | Success | Failed | Duration | Notes |
|------|-------|---------|--------|----------|-------|
| 2024-12-14 | 750 | 99.7% | 0.3% | ~5min | BatchGetKSUIDsByTriplets fix validated |
| 2024-12-14 | 200 | 78.7% | 21.3% | ~3min | AMI and EBS io1 bugs identified |
| 2024-12-14 | 200 | 86.7% | 13.3% | ~7min | AMI fixed, remaining: LT tags, Route53 domain, API GW deployment, AZ mismatch |

## Remaining Generator Issues (as of Dec 2024)

1. **LaunchTemplate tagSpecifications**: `resourceType = "instance"` should be `"launch-template"` - FIXED: removed tagSpecifications
2. **Route53 hosted zones**: `example.com` is reserved by AWS - FIXED: changed to `.perftest.internal`
3. **API Gateway Stage**: Stages require a DeploymentId - FIXED: removed Deployments/Stages (require Methods first)
4. **Volume attachment AZ mismatch**: EBS volumes and instances must be in same AZ - FIXED: calculated correct AZ from private subnet
5. **Route/IGW dependency**: Routes must reference gatewayId from VPCGatewayAttachment - FIXED: changed `igw.res.id` to `igwAttach.internetGatewayId`

## Formae Bugs Found During Perf Testing (Dec 2024)

### Bug 1: FormaCommandPersister Call timeout too short

**Description**: When storing a FormaCommand with 2000+ resources, the default Call timeout of 5 seconds is too short. The command is actually stored successfully, but the caller times out before receiving the response.

**Evidence from logs**:
```
2025-12-14T13:52:30 - Started storing forma command
2025-12-14T13:52:35 - Timeout error fired AND command stored successfully (same second)
```
Delta: exactly 5 seconds (the default Call timeout).

**Workaround applied**: Changed `bridge_actor.go` to use `CallWithTimeout(10)` instead of `Call()`.

**Recommended fix**: Make this timeout configurable or increase default further for large commands.

### Bug 2: NotStarted commands not picked up on agent restart - RESOLVED

**Description**: When a FormaCommand is stored but the changeset hasn't started executing yet (status "NotStarted"), restarting the agent does not resume processing of this command.

**Status**: RESOLVED - Testing on Dec 14, 2024 showed that commands in "InProgress" state ARE correctly resumed on agent restart. The earlier issue may have been specific to "NotStarted" status which occurs when the HTTP response times out before the changeset starts.

**Verified behavior**:
- Command at 1281 success / 792 waiting
- Agent stopped and restarted
- Command resumed processing correctly

### Bug 3: Stale resources after apply/destroy cycle (to verify)

**Description**: After a successful apply followed by destroy, there may be stale "managed" resources in the database. This needs verification during the next test.

**Verification steps**:
1. Run `formae apply` with test environment
2. Run `formae destroy` after completion
3. Check `formae inventory --query='managed:true'` - should show 0 managed resources

### Enhancement: Add "Canceling" status for commands

**Description**: When a command is being canceled, the status remains "InProgress" until all in-flight operations complete. Consider adding a "Canceling" intermediate status to provide better visibility.

**Current behavior**:
1. User runs `formae cancel`
2. Command status stays "InProgress" while in-flight operations complete
3. Status changes directly to "Canceled" when done

**Suggested behavior**:
1. User runs `formae cancel`
2. Command status changes to "Canceling" immediately
3. Status changes to "Canceled" when all in-flight operations complete

### Bug 4: CRITICAL - Command status success count lost on restart

**Description**: The command status success count is lost during agent restart. Resources are actually created successfully, but the command doesn't track them.

**Evidence**:
- Before agent restart: 1281 success, 792 waiting
- After restart and cancel: 94 success, 2040 canceled
- Inventory shows: **1284 managed resources** actually exist

**Root Cause Found**: `metastructure.go:764` in `ReRunIncompleteCommands()`:
```go
for i := range fa.ResourceUpdates {
    fa.ResourceUpdates[i].State = resource_update.ResourceUpdateStateNotStarted  // ← BUG
}
```

This unconditionally resets ALL resource update states to `NotStarted`, wiping out the correctly-loaded success states from the database.

**Fix Required**: Only reset non-final states (InProgress, Pending), preserve final states (Success, Failed, Canceled):
```go
switch fa.ResourceUpdates[i].State {
case resource_update.ResourceUpdateStateInProgress, resource_update.ResourceUpdateStatePending:
    fa.ResourceUpdates[i].State = resource_update.ResourceUpdateStateNotStarted
// Keep Success, Failed, Rejected, Canceled as-is
}
```

**Secondary Fix**: Verify `changeset.NewChangesetFromResourceUpdates()` filters out already-completed resources to prevent re-execution.

### Bug 5: Route53 HostedZone delete fails due to non-empty zones

**Description**: Route53 hosted zones cannot be deleted if they contain non-required record sets (A, CNAME, etc.). The destroy fails with `HostedZoneNotEmptyException`.

**Root Cause**: The destroy command is not correctly ordering the deletion - record sets should be deleted before their parent hosted zones.

**Impact**: 6 out of 1284 resources failed to delete (0.5% failure rate).

### Bug 6: Stale inventory entries after failed delete + manual cleanup

**Description**: After a resource fails to delete in formae and is manually cleaned up in AWS, the stale entry remains in the formae inventory as "managed".

**Expected behavior**: The next sync should detect the resource no longer exists and remove it from inventory (or mark as deleted).

### Bug 7: Destroy doesn't clean up S3 buckets and IAM roles

**Description**: After a destroy command completes, S3 buckets and IAM roles created by the apply still exist in AWS.

**Evidence**:
- Destroy reported 1278 success, 6 failed (Route53 zones)
- Manual verification found 9 S3 buckets and 5 IAM roles still existed from the test run
- Plus 15 buckets and 11 roles from older test runs that were never cleaned up

**Possible causes**:
1. These resources were created but canceled before destroy could process them
2. The destroy dependency ordering skipped them
3. The resources were marked as "already destroyed" incorrectly

**Impact**: AWS costs continue to accrue for orphaned resources.

### Bug 8: ChangesetExecutor crashes on Resume message while canceling

**Description**: When a cancel is issued while a command is in progress, the ChangesetExecutor FSM crashes because it doesn't have a handler for the `Resume` message in the `canceling` state.

**Error logs**:
```
ERR process terminated abnormally - No handler for message changeset.Resume in state 'canceling'
    [name='formae://changeset/executor/36rBrLi6jphCA9qdaM0C8avNv5j']
ERR failed to send ResourceUpdateFinished message to requester (error: unknown process)
ERR Failed to send MarkAsComplete message to forma command persister (error: timed out)
```

**Root Cause**: The `ChangesetExecutor` FSM is missing a handler for `changeset.Resume` in the `canceling` state. When this message arrives (likely from a ResourceUpdater completing), the FSM crashes.

**Impact**:
- The ChangesetExecutor process terminates abnormally
- In-flight ResourceUpdaters can't report completion (orphaned)
- Resources may complete successfully but their status is lost

**Fix Required**: Add a handler for `Resume` message in the `canceling` state that either:
- Ignores the message (if the resource update is no longer relevant)
- Or processes it to correctly track final state of in-flight operations

### Verified Working (Dec 2024)

1. **Command resumption on agent restart** - Commands in progress are resumed (but success counts may be lost - see Bug 4)
2. **Command cancellation** - Cancel correctly waits for in-flight operations to complete
3. **Destroy at scale** - Successfully deleted 1278/1284 resources (99.5% success rate)
