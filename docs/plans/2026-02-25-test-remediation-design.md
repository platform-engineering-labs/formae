# Test Remediation Design: `forma_persister` and `datastore`

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:writing-plans to create the implementation plan.

**Goal:** Improve mutation scores for the two worst-scoring packages — `forma_persister` (59.7%) and `datastore` (35%) — by targeting specific surviving mutant clusters identified in the 2026-02-24 mutation report.

**Approach:** Cluster-guided behavioral tests. Write unit tests that exercise the exact code paths where mutants survived, using Ergo's test actor infrastructure for `forma_persister` and direct datastore calls for `datastore`. Includes a small number of production code fixes discovered during analysis.

---

## Section 1: `internal/metastructure/forma_persister`

### 1a. `isResourceInFinalState` — per-state coverage

**Survivors:** Lines 77–80 (4 `CONDITIONALS_NEGATION` mutations, one per state).

The function checks for four terminal states (`Completed`, `Failed`, `Cancelled`, `Skipped`). No existing test exercises each state individually, so all four conditional mutations survive.

**Tests to add:**
- `TestIsResourceInFinalState_Completed` — assert returns `true`
- `TestIsResourceInFinalState_Failed` — assert returns `true`
- `TestIsResourceInFinalState_Cancelled` — assert returns `true`
- `TestIsResourceInFinalState_Skipped` — assert returns `true`
- `TestIsResourceInFinalState_Pending` — assert returns `false` (representative non-final state)

### 1b. Sync vs non-sync branching in `MarkResourceUpdateAsComplete`

**Survivors:** Lines 327–349, 463–508 (sync command treated differently from regular commands).

Sync commands are ephemeral and not persisted to the DB. The branching that implements this is untested, so mutations to the sync/non-sync condition survive.

**Tests to add (Ergo unit tests):**
- `TestFormaCommandPersister_SyncCommand_NoDBWriteOnCompletion` — send a `MarkResourceUpdateAsComplete` message for a sync command, assert no DB write occurs and the actor does not crash

### 1c. Remove `pendingCompletions` guard — let it crash

**Survivors:** Lines 565–576, 694–701 (9 mutations on the `if cached.pendingCompletions > 0 { cached.pendingCompletions-- }` guard).

The guard was intended to prevent the counter going negative. However, a negative counter signals a programming error (more completions than expected), which should not be silently swallowed. The actor model gives us a cleaner approach: remove the guard entirely and let the actor crash, triggering the supervisor to restart it with a clean state.

`FormaCommandPersister` uses `SupervisorStrategyTransient`. On restart, `Init` starts with an empty cache; non-sync commands are lazily reloaded from the DB via `getOrLoadCommand`. Sync commands are ephemeral and are simply lost on restart, which is acceptable — the synchronizer will re-issue them on its next cycle. Recovery is clean.

**Production code change:** Remove the `if cached.pendingCompletions > 0` guard in `MarkResourceUpdateAsComplete`. Keep the decrement unconditional.

**Test to add:**
- `TestFormaCommandPersister_UnexpectedCompletion_ActorTerminates` — send more `MarkResourceUpdateAsComplete` messages than registered resource updates, assert the actor terminates (rather than silently ignoring the overflow)

**Expected outcome for `forma_persister`:** ~19–21 of 29 survivors eliminated → ~85–90% (currently 59.7%)

---

## Section 2: `internal/metastructure/datastore`

### 2a. Optional field deserialization round-trip + production code fixes

**Survivors:** Lines 268–390 (multiple `CONDITIONALS_NEGATION` mutations on `if clientID.Valid`, `if descriptionText.Valid`, `if configMode.Valid` and related branches).

**Production code fixes:**
- `internal/metastructure/synchronizer.go:298`: change `clientID = ""` → `clientID = "synchronizer"` (empty string is ambiguous and untestable)
- `internal/metastructure/discovery/discovery.go:683`: change `clientID = "formae"` → `clientID = "discovery"` (should reflect the actual actor, not the product name)
- `internal/metastructure/datastore/sqlite.go`: add validation in `StoreFormaCommand` that rejects empty `Config.Mode` with a descriptive error (config mode should always be set by callers)

**Tests to add:**
- `TestDatastore_StoreAndLoad_FormaCommand_SynchronizerClientID` — store with `clientID = "synchronizer"`, load, assert clientID round-trips correctly
- `TestDatastore_StoreAndLoad_FormaCommand_DiscoveryClientID` — store with `clientID = "discovery"`, load, assert clientID round-trips correctly
- `TestDatastore_StoreAndLoad_FormaCommand_NoDescription` — store with empty description, load, assert empty string round-trips without error
- `TestDatastore_StoreFormaCommand_EmptyMode_ReturnsError` — attempt to store with empty `Config.Mode`, assert error returned
- Strengthen existing `StoreAndLoad` tests to assert the `Config.Mode` value is preserved after load

### 2b. Target CRUD edge cases

**Survivors:** Lines 2363–2427 (conditions on "did we affect any rows?" in `UpdateTarget` and `DeleteTarget`).

**Tests to add:**
- `TestDatastore_UpdateTarget_NotFound_ReturnsError` — call `UpdateTarget` with a non-existent ID, assert a not-found error
- `TestDatastore_DeleteTarget_NotFound_ReturnsError` — call `DeleteTarget` with a non-existent ID, assert a not-found error

### 2c. Sync command exclusion

**Survivors:** Line 190 (`fa.Command != pkgmodel.CommandSync` filter).

The query that loads forma commands excludes sync commands, but no test exercises both the sync and non-sync paths through this query.

**Test to add:**
- `TestDatastore_LoadFormaCommands_ExcludesSyncCommands` — store one sync command and one regular command, assert only the regular command is returned by the query

**Expected outcome for `datastore`:** ~16–25 of 117 survivors eliminated → ~43–50% (currently 35%).

Note: 117 survivors reflects years of under-tested CRUD code in `sqlite.go`. The tests above address the most significant clusters. Getting `datastore` to 80%+ would require a much larger, systematic effort across all CRUD operations — that is a separate, larger project.

---

## Non-goals

- No changes to packages already scoring ≥80%
- No attempt to reach 80% on `datastore` in this plan
- No new workflow tests (unit tests are sufficient for all cases identified here)
