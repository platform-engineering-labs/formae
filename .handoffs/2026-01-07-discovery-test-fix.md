# Handoff: Route53 RecordSet Discovery Test Fix

## Summary
Working on PR #128 (`refactor/remove-metadata-from-plugin-api`) which removes Metadata from ResourcePlugin API and adds Route53 RecordSet SDK tests. The lifecycle tests pass, but the discovery test for multi-resource test cases (RecordSet depends on HostedZone) fails.

## Current Branch
```
refactor/remove-metadata-from-plugin-api
```

## What Was Accomplished

### 1. Fixed Parent-Child Discovery Hierarchy Issue
- **Problem**: Discovery failed with "Parent resource type not found parent=AWS::Route53::HostedZone child=AWS::Route53::RecordSet"
- **Root Cause**: `ConfigureDiscovery` was only configured for the target resource type (RecordSet), but the discovery FSM needs parent types in the hierarchy to discover child resources
- **Fix**: Modified `runDiscoveryTest` in `tests/integration/plugin-sdk/plugin_sdk_test.go` to extract ALL resource types from the forma and pass them to `ConfigureDiscovery`
- **Commit**: `11343394 fix(sdk): configure discovery for all resource types in forma`

### 2. Fixed Merge Conflict with Main Branch
- Main branch added `ru.Resource.Schema` but our PR renamed `Resource` to `DesiredState`
- Fixed by changing to `ru.DesiredState.Schema` in `resource_update.go:324`
- Used interactive rebase with autosquash to fold fix into original refactor commit

## Current Issue: Discovery Test Timeout

### Symptom
```
timeout waiting for resource Z01123372S6GQVBMC0WLF (type: AWS::Route53::RecordSet) to appear in inventory after 2m0s
```
Discovery finds 4 RecordSet resources but not the specific one we created.

### Root Cause Identified
The `CreateUnmanagedResource` function in `tests/integration/plugin-sdk/framework/harness.go` (lines 972-979) only creates the **first** cloud resource:

```go
// Find the cloud resource to create
var cloudResource *pkgmodel.Resource
for i := range forma.Resources {
    if strings.Contains(forma.Resources[i].Type, "::") {
        cloudResource = &forma.Resources[i]
        break  // <-- Takes FIRST cloud resource (HostedZone), not RecordSet!
    }
}
```

For Route53 RecordSet tests, the forma contains:
1. `AWS::Route53::HostedZone` (first cloud resource - this gets created)
2. `AWS::Route53::RecordSet` (target resource - never gets created!)

The test then waits for a RecordSet to appear in discovery, but only a HostedZone was created.

## Fix Required

Modify `CreateUnmanagedResource` in `harness.go` to:

1. **Create ALL cloud resources** in dependency order (HostedZone first, then RecordSet)
2. **Resolve references between resources** - RecordSet's `hostedZoneId` property contains a `$res` resolvable that needs the actual HostedZone ID
3. **Return the NativeID of the target (last) resource**
4. **Track all created resources** for proper cleanup in `DeleteUnmanagedResource`

### Key Files to Modify
- `tests/integration/plugin-sdk/framework/harness.go` - `CreateUnmanagedResource` function (line 964)

### Reference: NativeID Formats
- HostedZone: `Z01123372S6GQVBMC0WLF` (just the zone ID)
- RecordSet: `Z01123372S6GQVBMC0WLF|www.example.com.|A` (composite: zoneID|name|type)

### Reference: RecordSet PKL Structure
```pkl
local hz = new hostedzone.HostedZone {
  label = "test-hosted-zone"
  name = domainName
}

new recordset.RecordSet {
  hostedZoneId = hz.res.id  // <-- Resolvable reference that needs resolution
  name = "www.example.com."
  type = "A"
  ...
}
```

## Git State
```
11343394 fix(sdk): configure discovery for all resource types in forma
133d42be test(aws): add Route53 RecordSet SDK tests
62b628d1 refactor(plugin): remove Metadata from ResourcePlugin API
49e6a733 fix(resolver): preserve $ref structures in arrays during property merge (main)
```

## CI Status
- Lint, build, license checks: PASSING
- SDK Lifecycle tests: PASSING (22 tests, 18 pass)
- SDK Discovery tests: FAILING (Route53 RecordSet timeout)

## Commands to Continue
```bash
# Build and run tests locally
make build
PLUGIN_NAME=aws go test -C tests/integration/plugin-sdk -v -tags=plugin_sdk -run TestPluginSDK_Discovery/route53-recordset -timeout 30m

# Check CI status
gh pr checks 128
```

## Related Files
- `tests/integration/plugin-sdk/plugin_sdk_test.go` - Test orchestration
- `tests/integration/plugin-sdk/framework/harness.go` - Test harness with CreateUnmanagedResource
- `tests/integration/plugin-sdk/framework/test_plugin_coordinator.go` - Plugin coordinator for direct plugin calls
- `plugins/aws/testdata/route53/recordset/` - Route53 RecordSet test PKL files
