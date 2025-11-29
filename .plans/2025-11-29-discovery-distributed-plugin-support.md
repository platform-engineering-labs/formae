# Plan: Discovery Actor Distributed Plugin Support

**Date**: 2025-11-29
**Branch**: `feat/distributed-plugins`
**Status**: In Progress

---

## Progress

### Pre-requisite Bug Fix (COMPLETED)
- **Fixed**: Filter was receiving only `Resource.Properties` instead of merged `Properties + ReadOnlyProperties`
- **File**: `internal/metastructure/resource_update/resource_updater.go:604-616`
- **Change**: Now uses `util.MergeJSON()` to merge properties before passing to filter
- **Tests**: Added `TestDiscovery_ResourceFiltering` and `TestDiscovery_ResourceFiltering_ByTags` in `discovery_test.go`

### Phase 1: MatchFilter Foundation - **COMPLETED**
- [x] 1.1 Define MatchFilter types (`pkg/plugin/resource.go`)
- [x] 1.2 Add GetMatchFilters() to ResourcePlugin interface
- [x] 1.3 Update AWS Plugin (`plugins/aws/aws.go`)
- [x] 1.4 Update FakeAWS Plugin (`plugins/fake-aws/fake_aws.go`)
- [x] 1.5 Update Azure Plugin (`plugins/azure/azure.go`)
- [x] 1.6 Update ResourceUpdate Struct (`internal/metastructure/resource_update/resource_update.go`)
- [x] 1.7 Update ResourceUpdate Factory (`internal/metastructure/resource_update/resource_update_factory.go`)
- [x] 1.8 Implement Filter Application in ResourceUpdater (with fallback to old function filter)
- [x] 1.9 Register MatchFilter with EDF (`edf_types.go` and `pkg/plugin/run.go`)

### Phase 2: Testing - **COMPLETED** (tests use old function filter, MatchFilter ready for Phase 3)
### Phase 3: PluginInfo and PluginCoordinator - **COMPLETED**
- [x] 2.1 Define PluginInfo interface (`pkg/plugin/resource.go`)
- [x] 2.2 Define RemotePluginInfo struct (`internal/metastructure/plugin_coordinator/remote_plugin_info.go`)
- [x] 2.3 Extend PluginAnnouncement with capabilities (`pkg/plugin/run.go`)
- [x] 2.4 Extend RegisteredPlugin with cached info (`plugin_coordinator.go`)
- [x] 2.5 Define GetPluginInfo message (`internal/metastructure/messages/plugin.go`)
- [x] 2.6 Implement PluginCoordinator handler for GetPluginInfo
- [x] 2.7 Add plugin-side GetFilters handler (`pkg/plugin/plugin_operator.go`)
- [x] Fixed EDF registration order for new types
### Phase 4: Discovery Refactoring - **COMPLETED**
- [x] 3.1 Added pluginInfoCache to DiscoveryData
- [x] 3.2 Created getPluginInfo() helper function
- [x] 3.3 Updated discover() to use PluginCoordinator
- [x] 3.4 Updated synchronizeResources() to use cached schema info
- [x] 3.5 Kept backward compatibility with function-based filters
- Note: Initial discovery delay deferred (not strictly needed for local plugins)
### Phase 5: Final EDF and Cleanup - **COMPLETED**
- [x] 5.1 Verified EDF registrations in `internal/metastructure/edf_types.go` - all required types present
- [x] 5.2 Added missing slice/map types to `pkg/plugin/run.go` for distributed plugin EDF:
  - `[]plugin.MatchFilter{}`
  - `[]plugin.ResourceDescriptor{}`
  - `[]plugin.FilterCondition{}`
  - `map[string]model.Schema{}`
- [x] 5.3 Removed duplicate filter type registrations in `pkg/plugin/run.go`
- [x] 5.4 Maintained backward compatibility with function-based filters in discovery
  - Local plugins: tries `pluginManager.ResourcePlugin()` for function filters
  - Distributed plugins: gracefully falls back to nil when plugin not available locally
- [x] 5.5 Tests passing: `TestDiscovery_ResourceFiltering` and `TestDiscovery_ResourceFiltering_ByTags`
- [x] 5.6 Build successful: `make build` completes without errors

---

## Problem Statement

The Discovery actor currently calls `pluginManager.ResourcePlugin(namespace)` directly to get plugin information. This fails in distributed mode because external plugins don't have a local `ResourcePlugin` - they're registered with `PluginCoordinator` as `RegisteredPlugin`.

Additionally, `ResourcePlugin.GetResourceFilters()` returns function types (`map[string]ResourceFilter`) which cannot be serialized for distributed communication.

---

## Goals

1. Discovery actor works with both local (.so) and external (distributed) plugins
2. Resource filters are declarative and serializable (no function types)
3. Filters are refreshed at the start of each discovery cycle
4. Single source of truth: PluginCoordinator handles all plugin info requests

---

## Part 1: ResourceFilter → MatchFilter Refactoring

### 1.1 Define MatchFilter Types

**File**: `pkg/plugin/resource.go`

```go
// MatchFilter is a declarative, serializable filter definition
type MatchFilter struct {
    ResourceTypes []string          // Resource types this filter applies to
    Conditions    []FilterCondition // All conditions must match (AND logic)
    Action        FilterAction      // What to do when conditions match
}

type FilterCondition struct {
    Type     ConditionType // TagMatch, PropertyMatch

    // For TagMatch - check if resource has tag with key in list and matching value
    TagKeys  []string      // Tag keys to look for (e.g., "kubernetes.io/cluster/cluster1")
    TagValue string        // Expected tag value (e.g., "owned")

    // For PropertyMatch (future) - JSONPath based
    Path     string        // JSONPath expression
    Operator string        // "eq", "contains", "regex", "exists"
    Value    string        // Expected value
}

type FilterAction string
const (
    FilterActionExclude FilterAction = "exclude"  // Exclude matching resources from discovery
    FilterActionInclude FilterAction = "include"  // Only include matching resources
)

type ConditionType string
const (
    ConditionTypeTagMatch      ConditionType = "tag_match"
    ConditionTypePropertyMatch ConditionType = "property_match"
)
```

### 1.2 Change ResourcePlugin Interface

**File**: `pkg/plugin/resource.go`

```go
// Old
GetResourceFilters() map[string]ResourceFilter

// New
GetResourceFilters() []MatchFilter
```

### 1.3 Update AWS Plugin

**File**: `plugins/aws/aws.go`

- Remove `shouldFilterEKSAutomodeResource` function
- Add `buildMatchFilters()` method that:
  1. Calls `GetAutomodeClusterNames()` to get current cluster list
  2. Builds tag keys: `kubernetes.io/cluster/{clusterName}` for each cluster
  3. Returns `[]MatchFilter` with TagMatch conditions
- `GetResourceFilters()` calls `buildMatchFilters()` to get fresh filters each time

```go
func (p *AWSPlugin) GetResourceFilters() []MatchFilter {
    clusterNames := p.GetAutomodeClusterNames()
    if len(clusterNames) == 0 {
        return nil
    }

    tagKeys := make([]string, len(clusterNames))
    for i, name := range clusterNames {
        tagKeys[i] = fmt.Sprintf("kubernetes.io/cluster/%s", name)
    }

    return []MatchFilter{{
        ResourceTypes: eksAutomodeResourceTypes,
        Conditions: []FilterCondition{{
            Type:     ConditionTypeTagMatch,
            TagKeys:  tagKeys,
            TagValue: "owned",
        }},
        Action: FilterActionExclude,
    }}
}
```

### 1.4 Update FakeAWS Plugin

**File**: `plugins/fake-aws/fake_aws.go`

- Add hardcoded filter for testing:

```go
func (p *FakeAWSPlugin) GetResourceFilters() []MatchFilter {
    return []MatchFilter{{
        ResourceTypes: []string{"FakeAWS::Test::FilteredResource"},
        Conditions: []FilterCondition{{
            Type:     ConditionTypeTagMatch,
            TagKeys:  []string{"formae:filtered"},
            TagValue: "true",
        }},
        Action: FilterActionExclude,
    }}
}
```

### 1.5 Update Azure Plugin

**File**: `plugins/azure/azure.go`

```go
func (p *AzurePlugin) GetResourceFilters() []MatchFilter {
    return nil // No filters yet
}
```

### 1.6 Update ResourceUpdate Struct

**File**: `internal/metastructure/resource_update/resource_update.go`

```go
type ResourceUpdate struct {
    // ... existing fields ...

    // Old: Filter plugin.ResourceFilter `json:"-"`
    // New:
    Filter *plugin.MatchFilter `json:"filter,omitempty"` // Specific filter for this resource type
}
```

### 1.7 Update ResourceUpdate Factory

**File**: `internal/metastructure/resource_update/resource_update_factory.go`

- Update `NewResourceUpdateForSyncWithFilter` to accept `*MatchFilter` instead of `ResourceFilter`
- Find the specific filter for the resource type from the `[]MatchFilter` slice

### 1.8 Implement Filter Application in ResourceUpdater

**File**: `internal/metastructure/resource_update/resource_updater.go`

Replace the current filter application (around line 607) with:

```go
func shouldFilterResource(filter *plugin.MatchFilter, properties json.RawMessage) bool {
    if filter == nil {
        return false
    }

    for _, cond := range filter.Conditions {
        switch cond.Type {
        case plugin.ConditionTypeTagMatch:
            if !matchesTagCondition(properties, cond) {
                return false // All conditions must match
            }
        case plugin.ConditionTypePropertyMatch:
            // Future: implement JSONPath matching
            return false
        }
    }

    // All conditions matched
    return filter.Action == plugin.FilterActionExclude
}

func matchesTagCondition(properties json.RawMessage, cond plugin.FilterCondition) bool {
    tags := model.ExtractTags(properties) // Use existing helper from pkg/model
    for _, tag := range tags {
        if slices.Contains(cond.TagKeys, tag.Key) && tag.Value == cond.TagValue {
            return true
        }
    }
    return false
}
```

### 1.9 Register MatchFilter with EDF

**File**: `internal/metastructure/edf_types.go`

Add to the registration list:
```go
plugin.MatchFilter{},
plugin.FilterCondition{},
```

**File**: `pkg/plugin/run.go`

Add same registrations for plugin-side EDF.

---

## Part 2: PluginInfo Interface and PluginCoordinator Enhancement

### 2.1 Define PluginInfo Interface

**File**: `pkg/plugin/resource.go` (or new file `pkg/plugin/plugin_info.go`)

```go
// PluginInfo provides read-only plugin metadata for discovery
type PluginInfo interface {
    GetNamespace() string
    SupportedResources() []ResourceDescriptor
    SchemaForResourceType(resourceType string) (model.Schema, error)
    GetResourceFilters() []MatchFilter
}
```

Both `ResourcePlugin` and a new `RemotePluginInfo` struct will implement this.

### 2.2 Define RemotePluginInfo Struct

**File**: `internal/metastructure/plugin_coordinator/plugin_info.go` (new file)

```go
// RemotePluginInfo implements PluginInfo for external/distributed plugins
type RemotePluginInfo struct {
    namespace          string
    supportedResources []plugin.ResourceDescriptor
    resourceSchemas    map[string]model.Schema
    resourceFilters    []plugin.MatchFilter
}

func (r *RemotePluginInfo) GetNamespace() string {
    return r.namespace
}

func (r *RemotePluginInfo) SupportedResources() []plugin.ResourceDescriptor {
    return r.supportedResources
}

func (r *RemotePluginInfo) SchemaForResourceType(resourceType string) (model.Schema, error) {
    schema, ok := r.resourceSchemas[resourceType]
    if !ok {
        return model.Schema{}, fmt.Errorf("schema not found for %s", resourceType)
    }
    return schema, nil
}

func (r *RemotePluginInfo) GetResourceFilters() []plugin.MatchFilter {
    return r.resourceFilters
}
```

### 2.3 Extend PluginAnnouncement

**File**: `pkg/plugin/run.go`

```go
type PluginAnnouncement struct {
    Namespace            string
    NodeName             string
    MaxRequestsPerSecond int

    // NEW: Plugin capabilities
    SupportedResources   []ResourceDescriptor
    ResourceSchemas      map[string]model.Schema
    ResourceFilters      []MatchFilter
}
```

Update plugin startup to populate these fields when announcing.

### 2.4 Extend RegisteredPlugin

**File**: `internal/metastructure/plugin_coordinator/plugin_coordinator.go`

```go
type RegisteredPlugin struct {
    Namespace            string
    NodeName             gen.Atom
    MaxRequestsPerSecond int
    RegisteredAt         time.Time

    // NEW: Cached from announcement
    SupportedResources   []plugin.ResourceDescriptor
    ResourceSchemas      map[string]model.Schema
    ResourceFilters      []plugin.MatchFilter
}
```

Update `handlePluginAnnouncement` to store the new fields.

### 2.5 Define GetPluginInfo Message

**File**: `internal/metastructure/messages/plugin.go`

```go
// GetPluginInfo requests plugin info from PluginCoordinator
type GetPluginInfo struct {
    Namespace     string
    RefreshFilters bool // If true, fetch fresh filters from plugin
}

// PluginInfoResponse contains plugin capabilities
type PluginInfoResponse struct {
    Found              bool
    Namespace          string
    SupportedResources []plugin.ResourceDescriptor
    ResourceSchemas    map[string]model.Schema
    ResourceFilters    []plugin.MatchFilter
    Error              string // If not found or error occurred
}
```

### 2.6 PluginCoordinator: Handle GetPluginInfo

**File**: `internal/metastructure/plugin_coordinator/plugin_coordinator.go`

Add handler for `GetPluginInfo` message:

```go
func handleGetPluginInfo(from gen.PID, state gen.Atom, data PluginCoordinatorData,
    msg messages.GetPluginInfo, proc *gen.ServerProcess) (gen.ServerStatus, PluginCoordinatorData, error) {

    // 1. Check external plugins first
    if registered, ok := data.registeredPlugins[msg.Namespace]; ok {
        filters := registered.ResourceFilters

        // If RefreshFilters, fetch fresh filters from plugin
        if msg.RefreshFilters {
            // Send GetFilters message to plugin and wait for response
            // This ensures filters are fresh at start of each discovery cycle
            freshFilters, err := fetchFreshFilters(proc, registered.NodeName, msg.Namespace)
            if err == nil {
                filters = freshFilters
                registered.ResourceFilters = freshFilters // Update cache
            }
        }

        return gen.ServerStatusOK, data, proc.Reply(from, messages.PluginInfoResponse{
            Found:              true,
            Namespace:          msg.Namespace,
            SupportedResources: registered.SupportedResources,
            ResourceSchemas:    registered.ResourceSchemas,
            ResourceFilters:    filters,
        })
    }

    // 2. Fall back to local plugins via pluginManager
    resourcePlugin, err := data.pluginManager.ResourcePlugin(msg.Namespace)
    if err != nil {
        return gen.ServerStatusOK, data, proc.Reply(from, messages.PluginInfoResponse{
            Found: false,
            Error: fmt.Sprintf("plugin not found: %s", msg.Namespace),
        })
    }

    rp := *resourcePlugin
    return gen.ServerStatusOK, data, proc.Reply(from, messages.PluginInfoResponse{
        Found:              true,
        Namespace:          msg.Namespace,
        SupportedResources: rp.SupportedResources(),
        ResourceSchemas:    buildSchemaMap(rp),
        ResourceFilters:    rp.GetResourceFilters(),
    })
}
```

### 2.7 Plugin-Side: Handle GetFilters Message

**File**: `pkg/plugin/plugin_operator.go`

Add new message type and handler:

```go
type GetFilters struct{}

// In HandleCall:
case GetFilters:
    filters := p.plugin.GetResourceFilters()
    return gen.ServerStatusOK, p.data, proc.Reply(from, filters)
```

---

## Part 3: Discovery Actor Refactoring

### 3.1 Remove Direct pluginManager Usage

**File**: `internal/metastructure/discovery/discovery.go`

Remove:
- Direct calls to `data.pluginManager.ResourcePlugin(namespace)`
- Storing `pluginManager` in DiscoveryData (if not needed elsewhere)

### 3.2 Add PluginInfo Cache to DiscoveryData

```go
type DiscoveryData struct {
    // ... existing fields ...

    // NEW: Cached plugin info per namespace
    pluginInfoCache map[string]*messages.PluginInfoResponse
}
```

### 3.3 Update discover() Function

Replace plugin lookup with PluginCoordinator call:

```go
func discover(...) {
    // At start of discovery cycle, clear cache to force refresh
    data.pluginInfoCache = make(map[string]*messages.PluginInfoResponse)

    for _, target := range data.targets {
        // Get plugin info from PluginCoordinator (with filter refresh)
        pluginInfo, err := getPluginInfo(proc, target.Namespace, true /* refreshFilters */)
        if err != nil {
            proc.Log().Info("Discovery: no plugin for namespace %s, skipping", target.Namespace)
            continue
        }

        // Cache for later use in synchronizeResources
        data.pluginInfoCache[target.Namespace] = pluginInfo

        discoverableResources := pluginInfo.SupportedResources
        // ... rest of discover logic unchanged ...
    }
}

func getPluginInfo(proc *gen.ServerProcess, namespace string, refreshFilters bool) (*messages.PluginInfoResponse, error) {
    result, err := proc.Call(
        gen.ProcessID{Name: actornames.PluginCoordinator, Node: proc.Node().Name()},
        messages.GetPluginInfo{Namespace: namespace, RefreshFilters: refreshFilters},
    )
    if err != nil {
        return nil, err
    }

    response := result.(messages.PluginInfoResponse)
    if !response.Found {
        return nil, fmt.Errorf(response.Error)
    }
    return &response, nil
}
```

### 3.4 Update synchronizeResources() Function

Replace direct plugin calls with cached info:

```go
func synchronizeResources(...) {
    // Get cached plugin info
    pluginInfo, ok := data.pluginInfoCache[namespace]
    if !ok {
        // Shouldn't happen, but handle gracefully
        var err error
        pluginInfo, err = getPluginInfo(proc, namespace, false)
        if err != nil {
            return err
        }
    }

    // Get schema from cached info
    schema, err := getSchemaFromPluginInfo(pluginInfo, op.ResourceType)
    if err != nil {
        return err
    }

    // Get filters from cached info
    resourceFilters := pluginInfo.ResourceFilters

    // ... rest of logic unchanged, but pass []MatchFilter instead of map ...
}
```

### 3.5 Add Initial Discovery Delay

**File**: `internal/metastructure/discovery/discovery.go`

In the discovery initialization or first cycle:

```go
const initialDiscoveryDelay = 10 * time.Second

func startDiscovery(...) {
    // Wait for plugins to announce themselves
    proc.Log().Info("Discovery: waiting %v for plugins to register", initialDiscoveryDelay)
    time.Sleep(initialDiscoveryDelay)

    // Then start discovery cycles
}
```

---

## Part 4: Testing

### 4.1 Add Filter Test to discovery_test.go

**File**: `internal/metastructure/discovery/discovery_test.go`

```go
func TestDiscovery_ResourceFiltering(t *testing.T) {
    // Setup:
    // 1. Configure FakeAWS plugin with filter (tag "formae:filtered" = "true" -> exclude)
    // 2. Create target for FakeAWS namespace
    // 3. Mock List to return 2 resources:
    //    - Resource A: has tag "formae:filtered" = "true" (should be filtered out)
    //    - Resource B: no such tag (should be included)
    // 4. Mock Read to return full properties for both

    // Execute:
    // Run discovery cycle

    // Verify:
    // - Only Resource B is stored in datastore as unmanaged
    // - Resource A is NOT stored (filtered out)
}
```

### 4.2 Update FakeAWS Plugin for Testing

**File**: `plugins/fake-aws/fake_aws.go`

Add hardcoded filter and test resource type to `SupportedResources()`.

---

## Part 5: EDF Registration

### 5.1 Agent-Side Registration

**File**: `internal/metastructure/edf_types.go`

Add:
```go
plugin.MatchFilter{},
plugin.FilterCondition{},
plugin.ResourceDescriptor{},
messages.GetPluginInfo{},
messages.PluginInfoResponse{},
```

### 5.2 Plugin-Side Registration

**File**: `pkg/plugin/run.go`

Add same types to plugin EDF registration.

---

## Implementation Order

### Phase 1: MatchFilter Foundation (Steps 1.1 - 1.9)
1. Define MatchFilter types in `pkg/plugin/resource.go`
2. Update ResourcePlugin interface
3. Update AWS, FakeAWS, Azure plugins
4. Update ResourceUpdate struct and factory
5. Implement filter application in ResourceUpdater
6. Register with EDF

**Checkpoint**: All existing tests pass, filters work with new declarative format

### Phase 2: Testing (Step 4.1 - 4.2)
1. Add filter test to discovery_test.go
2. Verify FakeAWS filter works

**Checkpoint**: New filter test passes

### Phase 3: PluginInfo and PluginCoordinator (Steps 2.1 - 2.7)
1. Define PluginInfo interface
2. Define RemotePluginInfo struct
3. Extend PluginAnnouncement
4. Extend RegisteredPlugin
5. Define GetPluginInfo message
6. Implement PluginCoordinator handler
7. Add plugin-side GetFilters handler

**Checkpoint**: PluginCoordinator can serve plugin info for both local and external plugins

### Phase 4: Discovery Refactoring (Steps 3.1 - 3.5)
1. Add pluginInfoCache to DiscoveryData
2. Update discover() to use PluginCoordinator
3. Update synchronizeResources() to use cached info
4. Remove direct pluginManager usage
5. Add initial discovery delay

**Checkpoint**: Discovery works with both local and distributed plugins

### Phase 5: Final EDF and Cleanup (Step 5.1 - 5.2)
1. Verify all EDF registrations
2. Run full test suite
3. Clean up any remaining direct plugin access

---

## Files to Modify

### Core Changes
- `pkg/plugin/resource.go` - MatchFilter types, PluginInfo interface
- `internal/metastructure/resource_update/resource_update.go` - Filter field type
- `internal/metastructure/resource_update/resource_update_factory.go` - Factory methods
- `internal/metastructure/resource_update/resource_updater.go` - Filter application
- `internal/metastructure/discovery/discovery.go` - Use PluginCoordinator

### Plugin Changes
- `plugins/aws/aws.go` - Declarative filters
- `plugins/fake-aws/fake_aws.go` - Test filters
- `plugins/azure/azure.go` - Empty filters

### Coordinator Changes
- `internal/metastructure/plugin_coordinator/plugin_coordinator.go` - GetPluginInfo handler
- `internal/metastructure/plugin_coordinator/plugin_info.go` - RemotePluginInfo (new file)
- `pkg/plugin/run.go` - Announcement with capabilities
- `pkg/plugin/plugin_operator.go` - GetFilters handler

### Message Changes
- `internal/metastructure/messages/plugin.go` - GetPluginInfo, PluginInfoResponse

### EDF Changes
- `internal/metastructure/edf_types.go` - New type registrations
- `pkg/plugin/run.go` - Plugin-side registrations

### Test Changes
- `internal/metastructure/discovery/discovery_test.go` - Filter test

---

## Open Questions (Resolved)

1. ✅ Tag extraction: Use `model.ExtractTags()` from pkg/model
2. ✅ Filter refresh: PluginCoordinator fetches fresh filters on each discovery cycle
3. ✅ ResourceUpdate.Filter: Store single `*MatchFilter` for the resource type
4. ✅ Announcement timing: Skip namespace if no plugin, log info, add 10s initial delay

---

## Success Criteria

1. `make test-all` passes
2. Distributed plugin test (`TestMetastructure_ApplyForma_DistributedPlugin_HappyPath`) passes
3. New filter test passes
4. Discovery works with AWS external plugin
5. No function types in plugin communication (all serializable)
