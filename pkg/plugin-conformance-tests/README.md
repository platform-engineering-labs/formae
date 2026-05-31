# pkg/plugin-conformance-tests — Plugin Conformance Test Suite

A provider-agnostic Go test harness that exercises a formae resource plugin
end-to-end through the real `formae` CLI and agent: create, read, update,
replace, destroy, extract, discover, and detect out-of-band changes. Plugin
authors call into it from a single `_test.go` file and get a comprehensive
CRUD-and-discovery suite for free.

Module path:

```
github.com/platform-engineering-labs/formae/pkg/plugin-conformance-tests
```

## Audience

Authors of formae **resource** plugins built on
[`pkg/plugin`](../plugin) who want a standardized way to validate their plugin
against the same lifecycle every other plugin is held to.

## Quick start

A complete conformance setup is two functions in one test file inside your
plugin repo:

```go
package main

import (
    "testing"

    conformance "github.com/platform-engineering-labs/formae/pkg/plugin-conformance-tests"
)

func TestPluginConformance(t *testing.T) {
    conformance.RunCRUDTests(t)
}

func TestPluginDiscovery(t *testing.T) {
    conformance.RunDiscoveryTests(t)
}
```

Then run them from the plugin directory:

```bash
# Both suites
go test -v ./...

# CRUD only, on a subset of resource types
FORMAE_TEST_TYPE=crud FORMAE_TEST_FILTER="s3-bucket,iam-group" go test -v ./...

# Discovery only, in parallel
FORMAE_TEST_TYPE=discovery FORMAE_TEST_PARALLEL=true go test -v ./...
```

The `formae plugin init` scaffold already wires this up; you only need to add
test data.

## How tests are discovered

The suite looks for Pkl test fixtures in a `testdata/` directory next to the
test (overridable via `FORMAE_TEST_TESTDATA_DIR`). For each base file it picks
up two optional variants by suffix:

| File | Role |
| ---- | ---- |
| `<resource>.pkl` | Required. Declares the resource to create and the expected post-create state. |
| `<resource>-update.pkl` | Optional. Same resource with at least one mutable property changed; drives the update step. |
| `<resource>-replace.pkl` | Optional. Same resource with a create-only field changed; drives the replace step (the suite expects `NativeID` to change). |

A run-unique identifier is exposed to Pkl as the `FORMAE_TEST_RUN_ID`
environment variable so resource names can be parameterised without
collisions between test runs.

## What the CRUD suite verifies

For each test case `RunCRUDTests` walks the resource through its full
lifecycle and asserts that:

1. The plugin registers with the agent.
2. `formae apply` creates the resource and reaches `Success`.
3. `formae inventory` reports the resource with the properties from the base
   Pkl file.
4. If the resource type is extractable, `formae extract` round-trips back to
   an equivalent Pkl declaration.
5. A forced sync leaves the resource untouched (idempotency).
6. If `-update.pkl` exists, patch-mode apply updates the resource without
   changing its `NativeID`.
7. If `-replace.pkl` exists, patch-mode apply replaces the resource (new
   `NativeID`) and the new properties land in inventory.
8. `formae destroy` removes the resource and inventory reflects that.
9. After re-creating the resource and deleting it out-of-band directly through
   the plugin, a forced sync tombstones it from inventory — confirming drift
   detection works for this resource type.

## What the discovery suite verifies

For each test case `RunDiscoveryTests`:

1. Creates the resource out-of-band, directly through the plugin (bypassing
   formae apply).
2. Registers the appropriate target with the agent.
3. Triggers a discovery scan.
4. Polls inventory until the resource shows up on the `$unmanaged` stack with
   the expected type.

Only resource types that declare `discoverable = true` in their Pkl schema are
exercised.

## Environment variables

| Variable | Purpose |
| -------- | ------- |
| `FORMAE_TEST_TYPE` | `crud`, `discovery`, or `all` (default). Skips the suite that doesn't match. |
| `FORMAE_TEST_FILTER` | Comma-separated case names or `/regex/` to restrict which test cases run. |
| `FORMAE_TEST_PARALLEL` | Set truthy (e.g. `true`, `1`) to run cases with `t.Parallel()`. |
| `FORMAE_TEST_TESTDATA_DIR` | Override the testdata directory. Relative paths resolve against the plugin directory. |
| `FORMAE_TEST_EXTRA_DISCOVERY_TYPES` | Extra resource types (comma-separated) to add to the discovery scan, beyond those declared in test data. |
| `FORMAE_BINARY` | Absolute path to a specific `formae` binary. Skips the channel-aware resolver. |
| `FORMAE_VERSION` | Pin a specific formae version. The resolver searches stable then dev channels for an exact match. |
| `FORMAE_TEST_RUN_ID` | Set by the harness for each run; available to Pkl fixtures for unique resource naming. |

A timeout knob for the OOB-delete inventory-removal step is also exposed for
slow backends; see the comment block above `RunCRUDTests` in
[`runner.go`](./runner.go) for the current name and default.

## Which `formae` binary gets used

The harness picks the binary every test run rather than relying on whatever is
on your `$PATH`. The resolution rules:

1. If `FORMAE_BINARY` is set, that exact binary is used.
2. Otherwise the harness reads `minFormaeVersion` from your plugin's
   `formae-plugin.pkl` and looks for the highest matching release in the
   **stable** orbital channel.
3. If `FORMAE_VERSION` is set, it pins to that exact version (still searching
   stable, then dev).
4. If the stable channel can't satisfy `minFormaeVersion`, the harness falls
   back to the **dev** channel. The dev tree is only initialised lazily, so
   stable-only plugins don't pay the cost.
5. If neither channel can satisfy the floor, the test fails with a message
   identifying the closest available version.

This means a plugin can opt in to an unreleased formae feature by bumping
`minFormaeVersion` in its manifest, without each test author having to install
a particular binary by hand. See [`setup.go`](./setup.go) for the resolver.

## How it works under the hood

`TestHarness` (see [`harness.go`](./harness.go)) does the heavy lifting:

- Resolves and downloads the right `formae` binary.
- Spins up a real `formae` agent subprocess in a temp directory.
- Spawns an Ergo `TestPluginCoordinator` so the harness can drive the plugin
  directly when the test step needs it (e.g. for the out-of-band delete and
  for discovery-only resource creation).
- Invokes `formae apply`, `formae destroy`, `formae extract`, `formae eval`,
  `formae inventory`, and `formae sync` as subprocesses for the steps that
  should go through the public CLI surface.
- Polls command status against the agent's HTTP API.
- Collects per-step results into a matrix printed by `ResultCollector` at the
  end of the run.

## See also

- [`pkg/plugin`](../plugin) — the resource plugin SDK that this suite tests.
- [`formae-plugin-template`](https://github.com/platform-engineering-labs/formae-plugin-template) —
  the template scaffolded by `formae plugin init`. Its `conformance_test.go`
  and `testdata/` are the canonical reference for wiring this suite into a new
  plugin.
- [formae documentation](https://docs.formae.io) — end-user docs and Pkl
  primer.

## License

FSL-1.1-ALv2. See [LICENSES/FSL-1.1-ALv2.md](../../LICENSES/FSL-1.1-ALv2.md).
