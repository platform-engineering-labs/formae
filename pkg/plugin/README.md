# pkg/plugin — Resource Plugin SDK

This is the Go SDK for writing **formae** resource plugins. A resource plugin
teaches the formae agent how to create, read, update, delete, and discover one
or more types of cloud resources (S3 buckets, GCP projects, Azure VMs, DNS
records, …).

Module path:

```
github.com/platform-engineering-labs/formae/pkg/plugin
```

## Audience

You should read this if you want to add formae support for a cloud service or
API that doesn't have a plugin yet. If you only want to *use* an existing
plugin, see the [formae documentation](https://docs.formae.io) instead.

> **Auth plugins are a different SDK.** Authentication plugins for the formae
> CLI/agent live in [`pkg/auth`](../auth) and use a net/rpc-based interface.
> This package only covers **resource** plugins.

## Quick start

The recommended way to start a new plugin is the bundled scaffolding command,
which clones the
[`formae-plugin-template`](https://github.com/platform-engineering-labs/formae-plugin-template)
repository and customises it for you:

```bash
formae plugin init
```

The generated project already wires up this SDK, the manifest, a Pkl schema
package, and a `conformance_test.go` that uses
[`pkg/plugin-conformance-tests`](../plugin-conformance-tests) to validate the
plugin end-to-end.

## What's in here

| Path | Purpose |
| ---- | ------- |
| `plugin.go`, `type.go`, `version.go` | Base `Plugin` interface, plugin `Type` ("resource"), `SDKVersion` and `MinFormaeVersion` constants. |
| `resource.go` | The `ResourcePlugin` interface that plugin authors implement, plus the optional `ObservablePlugin` and `Configurable` interfaces. |
| `resource/` | Request and result types for the CRUD methods (`CreateRequest`, `ReadResult`, `ProgressResult`, `OperationStatus`, error codes, …). |
| `sdk/` | The external-plugin entry point. `sdk.RunWithManifest(p, sdk.RunConfig{})` is the one call a plugin's `main` makes. |
| `manifest.go` | `Manifest` type and parser for `formae-plugin.pkl` (name, namespace, version, license, `minFormaeVersion`). |
| `descriptors/` | Extracts resource type descriptors and JSON schemas from the plugin's Pkl package at startup. |
| `schema/lib/` | Pkl standard-library extensions plugin schemas can import (random, bcrypt, tfvars helpers, …). |
| `discovery/` | Helper for the agent to locate installed plugin binaries on disk. |
| `logger.go`, `metrics.go` | `Logger` and `MetricRegistry` interfaces injected into the request context for structured logging and OpenTelemetry metrics. |
| `actor.go`, `application.go`, `plugin_operator.go`, `run.go`, `wrapper.go` | Ergo-actor wiring — the transport that ferries CRUD calls between the agent and the plugin process. Plugin authors don't need to touch these. |

## The `ResourcePlugin` interface

This is everything you implement. The SDK wraps it with manifest-derived
identity and schema methods automatically.

```go
type ResourcePlugin interface {
    // Configuration
    RateLimit() pkgmodel.RateLimitConfig
    DiscoveryFilters() []pkgmodel.MatchFilter
    LabelConfig() pkgmodel.LabelConfig

    // CRUD
    Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error)
    Read(ctx context.Context, req *resource.ReadRequest)     (*resource.ReadResult, error)
    Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error)
    Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error)

    // Async progress polling
    Status(ctx context.Context, req *resource.StatusRequest) (*resource.StatusResult, error)

    // Discovery
    List(ctx context.Context, req *resource.ListRequest) (*resource.ListResult, error)
}
```

See [`resource.go`](./resource.go) for the source of truth and
[`resource/resource.go`](./resource/resource.go) for the request and result
types.

### Optional interfaces

Implement these on the same struct to opt in to extra SDK features:

- **`ObservablePlugin`** — receive a `Logger` and `MetricRegistry` once at
  startup. The SDK also injects these into every `context.Context` it passes to
  your CRUD methods, so the typical pattern is to extract them with
  `plugin.LoggerFromContext(ctx)` / `plugin.MetricsFromContext(ctx)` inside each
  operation.
- **`Configurable`** — receive plugin-specific configuration as
  `json.RawMessage`, sourced from the user's `formae.conf.pkl`. Called once
  during startup, before the plugin announces itself to the agent.

## Async operations and progress

CRUD methods return a `*resource.ProgressResult` (embedded in each `*Result`
type). Long-running cloud operations should return
`OperationStatus.InProgress` together with the cloud-side `NativeID` and any
provider-specific tracking info. The agent will then call `Status` on a polling
schedule until the operation reaches `Success` or `Failure`.

The SDK retries automatically for error codes classed as recoverable
(`Throttling`, `NetworkFailure`, `ServiceInternalError`, `ServiceTimeout`,
`NotStabilized`, `InternalFailure`, `NotFound`, `ResourceConflict`). Other
error codes terminate the operation.

## Manifest and schema

Every plugin ships two extra files alongside its binary:

- **`formae-plugin.pkl`** — the manifest. Declares the plugin name, namespace
  (e.g. `AWS`, `CLOUDFLARE`), version, license, and the minimum formae version
  the plugin requires. See [`manifest.go`](./manifest.go) for the full schema.
- **`schema/pkl/PklProject`** — a Pkl package describing the resource types
  the plugin supports, their fields, validation rules, create-only fields,
  whether each type is `discoverable` / `extractable`, and parent-resource
  mappings. The SDK extracts JSON Schemas from this package at startup via
  [`descriptors/`](./descriptors).

The scaffolding command sets both of these up; you mostly edit the Pkl files
to describe new resource types as you add them.

## Entry point

A complete plugin `main` is typically a single call:

```go
package main

import (
    "github.com/platform-engineering-labs/formae/pkg/plugin/sdk"
    "github.com/your-org/formae-plugin-foo/pkg/foo"
)

func main() {
    sdk.RunWithManifest(&foo.Plugin{}, sdk.RunConfig{})
}
```

`RunWithManifest` reads the manifest from the plugin's install directory,
extracts schemas from the Pkl package, wraps your `ResourcePlugin` into the
internal `FullResourcePlugin`, starts an Ergo node on a free local port, and
announces the plugin to the agent. See [`sdk/run.go`](./sdk/run.go).

For testing or inspection — when you want the wrapped plugin without starting
a node — call `sdk.SetupPlugin` (or `sdk.SetupPluginFromDir` to point at a
specific directory) instead.

## Conventions and constraints

A few things commonly trip up new plugin authors:

- **Plugins must be stateless across operations.** The agent may restart you
  at any time, hot-reload you, or run operations concurrently. Don't rely on
  in-memory state surviving between calls; persist anything you need in the
  cloud or in the resource properties you return.
- **`NativeID` is mandatory in every `ProgressResult`.** It's how the agent
  re-finds your resource on subsequent operations.
- **Resource properties travel as JSON.** Properties on requests and results
  are `json.RawMessage` (or strings); make sure they round-trip cleanly through
  your Pkl schema.
- **Pick the right error code.** The agent uses
  `resource.OperationErrorCode` to decide whether to retry. Returning a generic
  `InternalFailure` for a permanently-broken request will cause unnecessary
  retries; using `Throttling` for a hard auth failure will mask the real
  problem.
- **No pointer sharing across the actor boundary.** Everything between agent
  and plugin is serialized (MessagePack + zstd over Ergo). If you stash a
  pointer in a request, expect to get back a fresh copy on the other side.

## Stability

The SDK is still evolving as more cloud providers come online and tier-1 use
cases shake out. We try to keep `ResourcePlugin` and its request/result types
stable, and any breaking change will be called out in the formae release
notes. Pin `minFormaeVersion` in your `formae-plugin.pkl` to the lowest formae
version your plugin actually requires, so end users get a clear error when
they try to use it with an older agent.

## See also

- [`pkg/plugin-conformance-tests`](../plugin-conformance-tests) — provider-agnostic
  CRUD and discovery test suite. Wire it into your plugin's `_test.go` to
  validate the entire lifecycle through the real agent.
- [`pkg/auth`](../auth) — the separate SDK for **auth** plugins.
- [`formae-plugin-template`](https://github.com/platform-engineering-labs/formae-plugin-template) —
  the template `formae plugin init` clones from.
- [formae documentation](https://docs.formae.io) — end-user docs, installation,
  Pkl primer, integrations.

## License

FSL-1.1-ALv2. See [LICENSES/FSL-1.1-ALv2.md](../../LICENSES/FSL-1.1-ALv2.md).
