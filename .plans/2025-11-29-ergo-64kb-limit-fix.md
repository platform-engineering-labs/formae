# Plan: Fix Ergo 64KB Message Limit for AWS Plugin Registration

## Problem

The AWS plugin fails to register with the Formae agent. The plugin sends a
`PluginAnnouncement` message containing 208 resource types and their schemas,
but the message never arrives at the agent. The connection is reset immediately
after sending.

## Investigation Findings

### Symptoms
- AWS plugin (208 resources, ~253KB announcement) fails to register
- FakeAWS plugin (4 resources, small announcement) registers successfully
- Trace logs show: `link closed: too large`

### Root Cause

The Ergo Framework has a **hardcoded 64KB buffer limit** in its networking layer
that cannot be configured via `MaxMessageSize`.

Location: `ergo.services/ergo@v1.999.310/net/proto/connection.go:2533`
```go
n, e := buf.ReadDataFrom(conn, math.MaxUint16)  // 65535 bytes limit
```

This limit is checked in `lib/buffer.go:130`:
```go
if lenB > limit {
    return 0, fmt.Errorf("too large")
}
```

The `MaxMessageSize` node option only affects a later check (line 2549) that
never gets reached because the buffer overflow happens first during reading.

### Size Analysis

| Component | Uncompressed | Gzip Compressed |
|-----------|--------------|-----------------|
| SupportedResources | 28.63 KB | - |
| ResourceSchemas | 223.82 KB | - |
| MatchFilters | ~0 KB | - |
| **Combined (PluginCapabilities)** | **252.51 KB** | **18.82 KB** |

Headroom: 45 KB remaining under 64KB limit.

## Proposed Solution

Create a `PluginCapabilities` struct that wraps all capability data and compress
the entire struct with gzip before sending.

### New Types

```go
// PluginCapabilities contains all plugin capability data, compressed for transfer
type PluginCapabilities struct {
    SupportedResources []ResourceDescriptor
    ResourceSchemas    map[string]model.Schema
    MatchFilters       []MatchFilter
}

// PluginAnnouncement is sent by plugins to PluginCoordinator on startup
type PluginAnnouncement struct {
    Namespace            string
    NodeName             string
    MaxRequestsPerSecond int

    // Capabilities contains gzip-compressed JSON of PluginCapabilities
    // Use CompressCapabilities() and DecompressCapabilities() helpers
    Capabilities []byte
}
```

### Changes Required

#### 1. Add new types and helpers (`pkg/plugin/run.go`)

- Define `PluginCapabilities` struct
- Update `PluginAnnouncement` to use `Capabilities []byte`
- Add `CompressCapabilities(PluginCapabilities) ([]byte, error)`
- Add `DecompressCapabilities([]byte) (PluginCapabilities, error)`

#### 2. Plugin side: Compress capabilities (`pkg/plugin/actor.go`)

```go
caps := PluginCapabilities{
    SupportedResources: p.plugin.SupportedResources(),
    ResourceSchemas:    buildSchemaMap(p.plugin),
    MatchFilters:       p.plugin.GetMatchFilters(),
}
compressed, err := CompressCapabilities(caps)

announcement := PluginAnnouncement{
    Namespace:            p.namespace,
    NodeName:             string(p.Node().Name()),
    MaxRequestsPerSecond: p.plugin.MaxRequestsPerSecond(),
    Capabilities:         compressed,
}
```

#### 3. Agent side: Decompress capabilities (`internal/metastructure/plugin_coordinator/`)

When receiving `PluginAnnouncement`:
```go
caps, err := plugin.DecompressCapabilities(announcement.Capabilities)
// Use caps.SupportedResources, caps.ResourceSchemas, caps.MatchFilters
```

### Files to Modify

1. `pkg/plugin/run.go` - Add `PluginCapabilities`, update `PluginAnnouncement`, add compress/decompress helpers
2. `pkg/plugin/actor.go` - Compress capabilities before sending
3. `internal/metastructure/plugin_coordinator/plugin_coordinator.go` - Decompress on receive

### Testing

1. Unit test: Compress/decompress round-trip for PluginCapabilities
2. Integration test: AWS plugin registration with compressed capabilities
3. Verify existing tests still pass

## Alternatives Considered

1. **Compress only schemas**: Simpler but less elegant, three separate fields
2. **Chunking**: Split into multiple messages - complex reassembly logic
3. **Lazy loading**: Request on-demand - changes API semantics, adds latency
4. **Fork Ergo**: Fix hardcoded limit - maintenance burden

## Risk Assessment

- **Low risk**: Gzip is well-tested, 45KB headroom provides safety margin
- **Clean API**: Single compressed field vs multiple
- **Future proof**: Can accommodate significant growth in resource types
