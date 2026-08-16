# Final review fix wave — command CLI restructure

Branch `feat/command-cli-restructure`, four commits on top of the nine already
on the branch:

| SHA | Subject |
| --- | --- |
| `8f81bf4f` | fix(agent): make an unqueried command list span every client |
| `c9c2d432` | fix(cli): point the remaining status hints at the new verbs |
| `e90a580d` | test(blackbox): observe scheduler commands through the datastore |
| `332b725c` | chore(api): regenerate the swagger docs |

## Verification

| Command | Result |
| --- | --- |
| `go test -tags=unit ./internal/...` | pass, exit 0, 73 packages ok, 0 failures |
| `go build -tags=property ./tests/blackbox/...` | pass, exit 0 |
| `make lint` | pass, 0 issues |

Only the sqlite backend's datastore suite ran. The postgres, mssql and
aurora suites need `make postgres-up` / `make mssql-up` /
`make local-data-api-up` containers, which were not started in this
environment. The change to those three backends is the same one-line no-rows
change as sqlite's and they share the `dstest` suite, so the new assertions
will run against them under `make test-unit-postgres` / `-mssql` /
`-auroradataapi`.

## C1 — bare `command list` returned one client-scoped command (BLOCKER)

The two duties (`command list` with no query, `command status` with no
argument) reach the same endpoint with the same empty query, so the wire had
to carry the distinction. `/commands/status` gained a `scope` parameter
(`apimodel.CommandScope`):

- `client` — the calling client's single most recent command. This is the
  default when the parameter is absent or unrecognized, so callers written
  against the older API (and the MCP server) keep their behavior.
- `agent` — every client's user commands, newest first, bounded by the
  requested count.

`Metastructure.ListFormaCommandStatus` now takes the scope. An empty query
under `agent` scope runs through `BlugeQuerier.BuildStatusQuery("")` — the
previously unreachable unconstrained branch — with `Source = user` and the
requested `N`, which is what the finding asked for. The client-scoped route
(`GetMostRecentFormaCommandByClientID`) is untouched and still serves
`command status` with no argument.

CLI wiring (`internal/cli/status.commandScopeFor`): `Single && Query == ""`
asks for `client`; everything else, including a bare `command list`, asks for
`agent`. A non-empty query carries its own narrowing, so scope is inert
there. `formae cancel` with no query pre-fetches with `client` scope, because
that is the set the server will actually cancel — without that it would have
shown commands the cancel never touches.

One related correctness fix fell out: when a bare `command status` discovers
its command id and re-attaches, the watch query is now pinned to `id:<id>`.
Otherwise the poll would keep re-asking for "the most recent command" and
could drift onto a different command mid-watch.

`--max-results` help text no longer says "when using a query".

Tests added:

- `TestListFormaCommandStatus_AgentScopeSpansClients` — a command from another
  client appears in the list.
- `TestListFormaCommandStatus_ClientScopeReturnsOnlyCallersMostRecent` — a
  bare `command status` never returns another client's command.
- `TestListFormaCommandStatus_AgentScopeReturnsAPage` — the regression that
  would have caught this: five stored commands produce five results, and the
  requested count bounds the page.
- `TestCommandListWithNoQueryAsksForAgentScope`,
  `TestCommandStatusWithNoArgumentAsksForClientScope`,
  `TestCommandStatusWithAnIDAsksForAgentScope` — the CLI-side scope choice.
- `TestServer_ListCommandStatusScopeParameter` — the endpoint's parameter
  mapping, including the backward-compatible default.

Evidence the tests bite: before the implementation they failed to compile
against the old signature, and the page test asserted 5 against the old
single-command route.

## I2 — force-reconcile id 404'd under the user-only filter

`prepareReconcile` hardcoded `SourceAutoReconciler` for both callers. It now
takes the source: `ForceAutoReconcile` passes `SourceUser` (a user asked for
it), the scheduled beat passes `SourceAutoReconciler`. The resource updates'
own source stays `PolicyAutoReconcile` — a different taxonomy, recording the
mechanism that produced them and defining the stack's reconcile baseline; a
comment on `prepareReconcile` spells out the distinction.

`TestForceReconcileCommandIsVisibleThroughCommandStatus` builds a reconcile
baseline in sqlite, prepares both a forced and a scheduled reconcile, and
asserts the forced one resolves by id through `ListFormaCommandStatus` while
the scheduled one stays invisible. Verified it fails for the right reason:
re-hardcoding `SourceAutoReconciler` inside `prepareReconcile` makes it fail
with `"[]" should have 1 item(s), but has 0`.

## I4 — blackbox observability of scheduler commands

The harness now reads the agent's sqlite datastore directly for the two
observations the API can no longer serve, with a comment at the section head
explaining why (the API deliberately hides scheduler commands; the harness
has to see them; a test-only need does not belong in the query grammar or on
the HTTP surface):

- `openAgentDB`, `commandRowsFromDB`, `nonTerminalCommandsFromDB`,
  `commandFromDB`, `waitForCommandInDB`.
- `waitForAllCommandsTerminal` polls `nonTerminalCommandsFromDB`, so
  quiescence covers every source again. Its diagnostic dump loads the stuck
  commands' resource updates from the datastore too.
- `latestSyncCommand` selects the newest `command = 'sync'` row whatever its
  source, and `WaitForNextSyncCommand` waits for it via `waitForCommandInDB`.
  `commandFromDB` reconstructs the resource updates (state, operation, stack,
  desired-state resource JSON) so `StateModel.ApplySyncCommand` still absorbs
  drift the way it did before the user-only filter landed.

By-id lookups and user-command polling still go through the API.

The two `GetFormaCommandsStatus("", clientID, 100)` call sites in
`waitAndCheckCommandCompleteness` were reviewed: their intent is "every
command this client submitted", which an empty query denotes under neither
the old nor the new semantics. They now spell it out as `client:me`, with a
comment. The stale "the API returns 500 when no commands exist" handling went
with it, since I6 makes that an empty result.

## I5 — cancel footer named a deprecated verb

`renderCancelResult` now prints `To check the cancellation progress:` followed
by one `formae command status <id>` line per canceled command, so the hint is
runnable as printed. Matches `summary.go`'s re-attach hints. Tests and the
styled golden updated.

## I6 — legacy sourceless rows produced a 500

`GetMostRecentFormaCommandByClientID` returns `(nil, nil)` on no rows in all
four backends (sqlite, postgres, mssql, aurora); the interface documents it.
`ListFormaCommandStatus` renders that as an empty result, which the server
turns into a 404 and `client.go` resolves to a concrete empty list, so an
upgrading user gets a clean "no commands". `commandsForCancelQuery` handles
the nil too, so a bare `formae cancel` with no history now answers "no
commands to cancel" (404) instead of a 500.

`dstest`'s existing case flipped from "expects an error" to "expects nil, no
error", and a new
`RunGetMostRecentFormaCommandByClientIDIgnoresSourcelessRows` covers a client
whose only row was written without a source.

## M1 — dead querier code

`BlugeQuerier.QueryStatus` and the unreferenced `querier.Querier` interface
(whose declared signature did not even match) are deleted;
`internal/metastructure/querier/querier.go` is gone. `BuildStatusQuery` is the
sole entry point and absorbed the doc comment.

## M2 — TUI header fallback

`Model.headerCommand`'s fallback value is now `"command status"`, which
survives the alias deletion. The pinned test was renamed to
`TestHeaderCommand_EmptyOptionFallsBackToARealVerb` and updated, and the two
statuswatch goldens that render the fallback were regenerated with
`go test -update`.

## M4 — swagger annotation

`/commands/status` documents the `scope` parameter (with its enum and its
"ignored when query is set" caveat), the 200 ceiling on `max_results`, and a
404 response. `docs/` was regenerated with `swag init` (a separate commit):
note that the checked-in output had drifted from `main` independently of this
branch, so that commit also picks up API model fields added earlier.

## M5 — sort-highlight assertion

`TestListEntryPointSortsByAgeDescending` also asserts
`m.multi.sortHi == colAge`.

## Accepted, unchanged

Interactive `formae cancel --query 'command:sync'` still finds nothing, per
the owner's ruling. The cancel pre-fetch was not weakened, and
`commandsForCancelQuery`'s deliberate non-restriction by source (so an
operator can still target `command:sync` explicitly) is untouched.

## Open

- Non-sqlite datastore suites unrun locally (containers not started); see
  Verification.
- MCP's `list_commands` with an empty query still gets the client-scoped
  single command, because the wire default is unchanged for compatibility. If
  that surface should list agent-wide, the MCP server (separate repo) passes
  `scope=agent`.
