# Mutation Testing Report
Generated: 2026-02-24 14:43:20

## Packages with no tests

- `cmd/formae`
- `internal/agent`
- `internal/cli`
- `internal/cli/agent`
- `internal/cli/clean`
- `internal/cli/cmd`
- `internal/cli/config`
- `internal/cli/dev`
- `internal/cli/display`
- `internal/cli/nag`
- `internal/cli/project`
- `internal/cli/prompter`
- `internal/cli/upgrade`
- `internal/constants`
- `internal/imconc`
- `internal/metastructure/actornames`
- `internal/metastructure/config`
- `internal/metastructure/forma_command`
- `internal/metastructure/messages`
- `internal/metastructure/plugin_process_supervisor`
- `internal/metastructure/policy_update`
- `internal/metastructure/stats`
- `internal/metastructure/testutil`
- `internal/metastructure/translator`
- `internal/metastructure/types`
- `internal/usage`
- `internal/util`
- `internal/workflow_tests/test_helpers`

## Mutation scores (worst first)

| Package | Score | Killed | Lived | Timed Out | Status |
|---------|-------|--------|-------|-----------|--------|
| `internal/metastructure/datastore` | 35.0% | 63 | 117 | 16 | ❌ |
| `internal/metastructure/forma_persister` | 59.7% | 43 | 29 | 0 | ❌ |
| `internal/logging` | 66.7% | 2 | 1 | 0 | ⚠️ |
| `internal/metastructure/resource_update` | 76.3% | 158 | 49 | 2 | ⚠️ |
| `internal/metastructure/patch` | 82.9% | 34 | 7 | 0 | ✅ |
| `internal/api` | 85.2% | 23 | 4 | 0 | ✅ |
| `internal/cli/inventory` | 85.7% | 6 | 1 | 0 | ✅ |
| `internal/metastructure/transformations` | 85.7% | 18 | 3 | 0 | ✅ |
| `internal/metastructure/resolver` | 90.3% | 28 | 3 | 0 | ✅ |
| `internal/metastructure/resource_persister` | 91.7% | 33 | 3 | 0 | ✅ |
| `internal/cli/app` | 100.0% | 2 | 0 | 0 | ✅ |
| `internal/cli/apply` | 100.0% | 9 | 0 | 0 | ✅ |
| `internal/cli/cancel` | 100.0% | 5 | 0 | 0 | ✅ |
| `internal/cli/destroy` | 100.0% | 11 | 0 | 0 | ✅ |
| `internal/cli/eval` | 100.0% | 8 | 0 | 0 | ✅ |
| `internal/cli/extract` | 100.0% | 2 | 0 | 0 | ✅ |
| `internal/cli/plugin` | 100.0% | 16 | 0 | 0 | ✅ |
| `internal/cli/status` | 100.0% | 7 | 0 | 0 | ✅ |
| `internal/metastructure/changeset` | 100.0% | 81 | 0 | 0 | ✅ |
| `internal/metastructure/stack_update` | 100.0% | 7 | 0 | 0 | ✅ |
| `internal/metastructure/target_update` | 100.0% | 16 | 0 | 0 | ✅ |

## Workflow-test-only coverage

Lines only reachable via workflow tests (not covered by package unit tests):

```
No lines found that are exclusively covered by workflow tests.
```
