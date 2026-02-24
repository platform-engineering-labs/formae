# Mutation Testing Design

**Date:** 2026-02-21
**Branch:** feat/mutation-testing
**Goal:** Understand test quality gaps across the formae codebase using mutation testing, then decide on remediation.

## Background

Mutation testing works by introducing small automated code changes (mutants) — flipping `>` to `>=`, deleting a return statement, negating a condition — and re-running the tests to see if they catch the change. The **mutation score** is `killed mutants / total mutants`. A mutant that survives indicates either untested code or a test that executes code without asserting its behavior meaningfully.

### Score benchmarks

| Score | Quality |
|-------|---------|
| < 60% | Poor — tests are mostly smoke tests |
| 60–80% | Acceptable for most systems |
| 80–90% | Good — meaningful assertions |
| > 90% | Excellent — typical of critical/financial logic |

For a core IaC tool like formae, we target **80%+** on core logic (metastructure, changeset, datastore) and are more lenient on CLI rendering and formatting code.

## Tool: gremlins

[gremlins](https://github.com/go-gremlins/gremlins) is the most actively maintained Go mutation testing tool. Key feature: it integrates with Go's coverage profile and only mutates code that tests actually *execute*, avoiding noise from genuinely unreachable code.

Note: "dead code" in this context means code that is never executed even when tests run — not code that is executed but weakly asserted. Weakly-asserted code will still be mutated and will produce surviving mutants.

gremlins is installed as a tool dependency via the `tools/` package manager.

## Architecture of the Diagnostic

### The actor-based testing challenge

Formae uses the Ergo actor framework extensively. Multi-actor interaction scenarios are tested via workflow tests in `internal/workflow_tests/`, while individual actor behavior is tested by package-local unit tests (which themselves spin up mini actor systems via `testutil`).

gremlins works by mutating a package and re-running that package's own tests. This means:
- For packages with strong package-local unit tests: accurate mutation scores
- For packages whose coverage comes primarily from workflow tests: artificially low scores

To address this, we generate **two coverage profiles** and diff them:

1. **Unit profile** — `go test -tags=unit ./...` (all package-local tests)
2. **Workflow profile** — `go test -tags=unit ./internal/workflow_tests/...`

The diff identifies lines that are *only* covered by workflow tests, not reachable by unit tests. These are flagged separately in the report rather than contributing to a misleading low mutation score.

### Execution strategy

A `scripts/mutation-test.sh` script iterates over all Go packages and for each:

1. Checks whether the package has unit-tagged test files
2. If yes: generates a per-package coverage profile and runs gremlins
3. Captures JSON output and appends to `docs/plans/mutation-report/results.json`
4. Packages with no test files at all are collected as "untested packages"

Running per-package (rather than a single monolithic run) means:
- Results are available incrementally — you don't wait hours for a single output
- A slow or broken package doesn't block the rest
- The run can be interrupted and partially reused

A `make mutation-test` target wraps the script.

## Output

Results are written to `docs/plans/mutation-report/`:

### `summary.md`

A ranked table of packages by mutation score (lowest first), plus a separate list of packages with no tests. Example:

```
## Packages with no tests
- internal/metastructure/forma_command
- internal/metastructure/stats
- internal/metastructure/policy_update
- internal/metastructure/translator
- internal/util

## Mutation scores (lowest first)
Package                                          | Score | Killed | Total | Status
-------------------------------------------------|-------|--------|-------|-------
internal/metastructure/changeset                 | 42%   | 21     | 50    | ❌
internal/metastructure/resolver                  | 78%   | 39     | 50    | ⚠️
internal/metastructure/datastore                 | 85%   | 68     | 80    | ✅
...

## Workflow-test-only coverage
Lines reachable only via workflow tests (not unit tests):
- internal/metastructure/changeset: N lines
...
```

### `results.json`

Raw per-package gremlins JSON output for deeper inspection.

## Scope

This diagnostic is a **one-off run**, not integrated into CI. The Makefile target exists for convenience but the report is a snapshot in time.

- **In scope**: all packages under `internal/`, `pkg/`, and `plugins/` with unit build tags
- **Out of scope**: CLI rendering/formatting (lenient threshold), e2e tests, external plugins

## Known issues

- `TestDatastore_DeleteStack_ThenRecreate` is a known flaky test in the datastore package (passes in isolation, occasionally fails under full suite due to test ordering). This may affect gremlins results for the datastore package.

## Next steps after the report

Once the report is generated, packages are categorized into three buckets:

1. **Good (80%+)** — no action needed
2. **Low score, covered by unit tests** — weak assertions; write targeted new/improved tests
3. **Low score or workflow-test-only coverage** — deliberate decision needed: either add unit tests or accept that workflow tests are the appropriate level for that code

Bucket 3 may be entirely acceptable — some actor interaction logic genuinely belongs in workflow tests. The report surfaces the gap; we decide case by case.

Remediation work (if any) is a separate plan after reviewing the report.
