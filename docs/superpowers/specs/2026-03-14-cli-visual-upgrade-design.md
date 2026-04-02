# CLI Visual Upgrade Design

## Overview

A holistic visual and UX overhaul of the formae CLI, replacing the current
`fmt.Sprintf` + `gookit/color` rendering pipeline with a modern TUI built on
the Charmbracelet library ecosystem. The upgrade introduces interactive terminal
UIs, responsive layouts, a new visual identity, and improved prompting — while
preserving the existing machine-readable output paths unchanged.

## Goals

- Responsive layouts that adapt to terminal width instead of breaking on narrow
  viewports
- Interactive TUI for commands that benefit from live updates, navigation, and
  drill-down
- Consistent visual identity across all commands with a configurable theme
  system
- Better handling of large outputs (scrolling, filtering, smart collapsing)
- Improved prompting with structured forms and multi-select
- Graceful degradation on terminals with limited capabilities

## Non-Goals

- Changing the machine-readable output paths (`--output json/yaml`)
- Building a full TUI shell (like k9s) — though the architecture should not
  prevent this in the future
- Changing the underlying API or data model
- Reworking the extract command's project structure logic (captured as future
  work)

## Libraries

| Library    | Purpose                                                    |
| ---------- | ---------------------------------------------------------- |
| bubbletea  | Core TUI framework (Elm architecture event loop)           |
| bubbles    | Pre-built components (viewport, table, spinner, text input) |
| lipgloss   | Terminal styling (colors, borders, padding, layout)        |
| huh        | Structured forms and prompts                               |
| glamour    | Markdown rendering in terminal (for help text, descriptions) |

These replace:

| Current            | Replacement                      |
| ------------------ | -------------------------------- |
| `gookit/color`     | `lipgloss`                       |
| `gtree`            | Custom tree component via lipgloss |
| `tablewriter`      | `bubbles/table`                  |
| `BasicPrompter`    | `huh`                            |
| `ClearScreen` loop | bubbletea tick-based updates     |

## Migration Strategy

**Clean break on a feature branch.** All commands are rebuilt in parallel on
the branch. No dual-maintenance of old and new rendering code. The branch is
merged when the full set of commands is ready for release.

**Build order:**

1. **Foundation layer** — theme system, lipgloss style definitions, `tui.Run()`
   helper for terminal setup/teardown, human-vs-machine output routing
2. **Mockups** — ASCII/text mockups of all commands to validate the full UX
   before writing code. Terminal prototypes with actual libraries for the visual
   polish pass.
3. **Shared components** — extracted from mockup patterns: themed table,
   progress bar, panel layout, scrollable viewport, status indicators
4. **Commands** — built one at a time using the shared components. Start with
   the status/watch view since it is the core shared component used by
   apply, destroy, and cancel.

## Visual Identity

### Color Palette (New — "formae" theme)

| Role              | Color                        | Usage                                      |
| ----------------- | ---------------------------- | ------------------------------------------ |
| Base              | Shades of gray/black         | Backgrounds, panels, borders               |
| Primary text      | Bright white                 | Active/important content                   |
| Secondary text    | Medium gray                  | Labels, metadata, secondary info           |
| Disabled/subtle   | Dark gray                    | Inactive items, hints                      |
| Primary accent    | Blue                         | Resource IDs, links, interactive elements  |
| Secondary accent  | Orange                       | Brand presence, progress bars, callouts    |
| Error             | Red                          | Failed states, error messages              |
| Warning           | Yellow/gold                  | Drift, warnings                            |
| Done              | Bright white                 | Completed items (no green)                 |
| In-progress       | Dim/medium white             | Currently executing items                  |
| Pending           | Dark gray                    | Not yet started                            |

Design principle: draw the eye to what is NOT working. Errors and warnings use
color. Success states use brightness/weight only — done items are bright white,
in-progress items are dimmer. This keeps attention on failures and active work.

### Color Palette (Classic — "classic" theme)

The existing color scheme (gold, blue, grey, red, green, light blue) preserved
via lipgloss for users who prefer the current look. The classic theme uses the
same layout and components as the new theme, just with different color
definitions.

### Theme Configuration

Theme is configurable in the PKL config under the `cli` block:

```pkl
class CliConfig {
    api: ApiConfig
    disableUsageReporting: Boolean = false
    theme: "formae" | "classic" = "formae"
}
```

The theme system is implemented as a `theme` package that defines all lipgloss
styles centrally. All components reference theme styles — no hardcoded colors
anywhere.

### Logo / Banner

The formae propeller icon rendered in the terminal:

- **Primary**: Sixel or Kitty graphics protocol for terminals that support it
  (actual pixel rendering)
- **Fallback**: Braille character art approximation of the propeller icon

Terminal capabilities are detected at startup to pick the appropriate renderer.
The banner is shown on non-TUI commands and as a compact header in TUI views.

### Terminal Compatibility

Target modern terminals with graceful degradation:

- True color (24-bit) as the primary target
- Automatic degradation to 256-color and 16-color via lipgloss/termenv
- Unicode box drawing and braille characters assumed available
- Sixel/Kitty graphics optional (braille fallback)
- Minimum terminal width defined per view, with a "terminal too narrow" message
  below that threshold

## Command Classification

### Full Interactive TUI

Commands that take over the terminal with a bubbletea event loop.

| Command    | Entry flow                                                      |
| ---------- | --------------------------------------------------------------- |
| `apply`    | Simulation → confirmation → drift resolution → live watch       |
| `destroy`  | Simulation → confirmation → live watch                          |
| `cancel`   | Submit cancellation → live watch of cancellation                |
| `status`   | Multi-command dashboard with drill-down, optional live watch    |
| `inventory`| Scrollable/searchable tabbed tables                             |

### Improved Formatting (print-and-exit, lipgloss-styled)

| Command        | Improvements                                            |
| -------------- | ------------------------------------------------------- |
| `status` (single, TBD) | Styled panel, may become TUI                   |
| `eval`         | Themed output: resource types, labels, properties       |
| `extract`      | File selector, PklProject prompt, themed output         |
| `status agent` | Lipgloss-styled stats tables                            |

### Improved Prompting (huh)

| Command        | Improvements                                            |
| -------------- | ------------------------------------------------------- |
| `project init` | Multi-select plugins, styled form inputs                |
| `plugin init`  | Styled scaffold prompts                                 |

### Unchanged

`config`, `agent start`, `clean`, `upgrade` — and all `--output json/yaml`
machine-readable paths across every command.

## Per-Command UX Flows

### Apply

**Phase 1 — Simulation preview (bubbletea scrollable view):**

- Runs simulation automatically (as today)
- Results in a styled panel: header with command type + mode
- Tree of updates grouped by stack, then by type: target updates, stack updates,
  resource updates, policy updates
- Each update shows operation (create/update/delete), type, label
- For resource updates: collapsible diff of property changes (patch details)
- **Smart auto-collapse**: if the full tree fits in the viewport, show
  everything expanded. If it overflows, collapse into groups with counts
  (e.g., "Resource Creates (45)") — user expands what they care about.
- Scrollable with vim-style keybindings (`j/k`, `ctrl-d/ctrl-u`)
- `/` to search by resource type or label
- `enter` to confirm and proceed, `q` to abort

**Phase 2 — Confirmation:**

- If `--yes` not specified: huh confirmation prompt
- Shows operation summary (X creates, Y updates, Z deletes)

**Phase 3 — Drift resolution (reconcile rejected, apply only):**

- When the agent rejects a reconcile due to out-of-band changes, instead of
  printing extract commands and manual delete instructions, transition to an
  interactive drift resolution view
- Each drifted resource shown as a selectable item with the change summary
- Per-resource choice: "accept drift into my code" / "overwrite with my code" /
  "skip"
- Formae runs the needed extract commands automatically for accepted changes
- After the user resolves all drifted resources, re-simulate to verify the
  updated state, then go back to Phase 2 (confirmation) with the new simulation
- If the re-simulation also gets rejected (e.g., another out-of-band change
  during resolution), show the drift resolution view again. Cap at 3 retries,
  then suggest the user use `--force` or resolve manually.

**Phase 4 — Live execution watch:**

- Hands off to the shared status/watch TUI component (same as `status --watch`)
- See the Status section below for the shared watch view design

### Destroy

Same flow as Apply but without Phase 3 (drift resolution) and with
destroy-specific features:

**Phase 1 — Simulation preview:**

Same as Apply. Additionally, if cascade deletes are detected (resources that
depend on resources being deleted), these are visually distinguished in the
tree — marked as cascade deletes with their dependency source shown.

**Phase 2 — Confirmation:**

- If cascade deletes are present: prominent warning panel before the
  confirmation prompt, listing all cascade-affected resources and what they
  depend on
- With `--on-dependents=abort` (default): cascade deletes cause the command
  to abort with a clear message suggesting `--on-dependents=cascade`
- With `--on-dependents=cascade`: cascade deletes are included in the
  confirmation summary with a warning color

**Query-based destroy:** Destroy supports both file-based (`--file`) and
query-based (`--query`) input. Both follow the same simulation → confirmation
→ watch flow, just with different input sources. The simulation header indicates
which input mode was used.

**Phase 3 — Live execution watch:** Same shared status/watch TUI component.

### Cancel

Submits the cancellation, then enters the shared status/watch TUI showing the
command transitioning through cancellation. The view shows which updates
completed, which were in-progress (and won't be canceled), and which will be
canceled.

### Status / Watch (shared component)

This is the core TUI component, used by apply, destroy, cancel (via `--watch`)
and the status command directly. Today, `runApplyForHumans` already calls
`status.WatchCommandsStatus` — this shared pattern continues.

**Multi-command view:**

- Scrollable list of commands, each showing: command ID, type, mode, state,
  duration, progress (X/Y updates done)
- `enter` to drill into a command's detail view
- `esc` to go back to the list
- Filter bar: filter by state, type, command ID (maps to `--query`)
- `d` to toggle detail/summary:
  - **Summary**: one line per command with progress bar
  - **Detail**: expand to show update-level progress per command

**Single command detail view:**

- Header: command ID, mode, state, elapsed time
- Per-stack progress bar (orange fill, X/Y updates complete)
- Update list showing targets, stacks, resources, policies with status
  indicators:
  - Bright white = done
  - Dim white + spinner = in-progress
  - Dark gray = pending
  - Red = failed
- `d` to toggle detail/summary:
  - **Summary**: one line per update (status + type + label)
  - **Detail**: expand each update to show operation, duration, error if failed

**Large command handling:**

- Scrollable viewport with vim keybindings (`j/k`, `ctrl-d/ctrl-u`)
- `/` to search by resource type or label
- `f` to filter by state (show only failed, only in-progress, etc.)
- **Auto-follow**: viewport auto-scrolls to show in-progress updates. Manual
  scroll breaks auto-follow, `F` to re-enable.
- **Smart auto-collapse**: same logic as simulation — fits in viewport =
  expanded, overflows = grouped with counts
- Status counts always visible in header regardless of scroll position
  (e.g., "142/200 done, 3 in-progress, 1 failed")

**Live updates:**

- Bubbletea tick-based refresh (replaces the hardcoded `time.Sleep(2s)` +
  `ClearScreen` loop)
- Smooth transitions as updates change state
- When all commands finish: show final state, exit (or stay with "completed"
  indicator)

**`--watch` flag semantics:** In the new TUI, the status view is always
live-updating when showing active commands (the TUI polls on a tick). The
`--watch` flag controls whether the TUI stays open after all commands finish
(`--watch` = stay open and continue polling for new commands matching the query)
or exits when done (default). For machine-readable output, `--watch` is not
supported (matching current behavior).

### Inventory

**Subcommand mapping:** The current CLI uses separate subcommands (`formae
inventory resources`, `formae inventory targets`, etc.). In the new design:

- `formae inventory` (no subcommand) opens the tabbed TUI starting on the
  Resources tab
- `formae inventory resources|targets|stacks|policies` opens the TUI
  pre-focused on the specified tab
- Machine-readable paths (`--output json/yaml`) continue to use subcommands
  and work as today — no TUI launched
- `--query` and `--max-results` flags apply to whichever tab is active.
  Switching tabs resets the query context.

**Full TUI with switchable views:**

- Four views: Resources, Targets, Stacks, Policies
- Switch between views via keybinding (`1/2/3/4` or `tab`)
- Each view is a scrollable, sortable lipgloss/bubbles table
- Search/filter bar: type to filter rows (maps to `--query`)
- Column behavior:
  - Columns resize based on terminal width
  - Lower-priority columns hide on narrow terminals
  - Sort via keybinding (`s` then pick column)
- `enter` on a row to see full detail in an expanded panel
- `q` to quit

**Truncated result sets:**

- A sensible default limit (e.g., 200 results) is applied without the user
  needing to think about it
- The status bar always shows "Showing X of Y results"
- When Y > X: status bar hint "refine your query to see more" plus a visual
  truncation marker at the bottom of the table
- When the full result fits: status bar just shows the count, no hint
- `--max-results` flag remains as an escape hatch but is not the primary UX
- Requires the API to return total count alongside truncated results (believed
  to be supported already)

### Eval

**Formatted print-and-exit output:**

- Parsed forma file displayed with themed styling
- Resource types in blue, labels in white, properties with indentation
- Responsive width: property values truncate or wrap based on terminal width
- Pipe-friendly — works with `less`, no TUI needed
- The existing `--beautify` and `--colorize` flags are replaced by the theme
  system. Colorization is automatic (disabled when piped/non-TTY). Beautified
  formatting is the default for human output.

### Extract

**Light interactivity + formatted output:**

- If no PklProject file detected: huh confirmation prompt asking if one should
  be created
- File selector for target forma file (huh path input with autocomplete)
- Extracted content shown with themed formatting
- **Future improvement** (not in initial scope): extract becomes smarter about
  distributing extracted resources/targets/policies/stacks into correct
  locations within the project structure. PklProject creation piggybacks on
  `project init` instead of duplicating logic. `project init` downloads a
  template with best-practice project structure.

### Status Agent

**Formatted print-and-exit output:**

- Replace current `tablewriter` tables with lipgloss-styled tables
- Stats layout: managed/unmanaged counts, percentages, sync status, discovery
  status
- Responsive column sizing
- Theme colors for unmanaged percentages (orange/red for high values)

### Project Init

**Interactive form (huh):**

- Stepped form flow: project name, plugin selection (multi-select with
  checkboxes), API configuration, other parameters
- Styled with formae theme
- Inline validation feedback

### Plugin Init

**Interactive form (huh):**

- Scaffold prompts styled with formae theme
- Same form patterns as project init

### Error Rendering

**Consistent across all commands:**

- Errors rendered in a styled panel with red border
- Error type as header, details below
- Structured errors (reconcile rejected, conflicting commands, referenced
  resources not found, patch rejected, empty stack rejected, target already
  exists) get dedicated formatted layouts — not walls of text
- The interactive drift resolution replaces the "copy-paste extract commands"
  pattern for reconcile rejected specifically (in apply flow)

## Shared UX Patterns

### Keybindings

All TUI views share a consistent keybinding vocabulary:

| Key            | Action                                           |
| -------------- | ------------------------------------------------ |
| `j/k`          | Scroll up/down                                   |
| `ctrl-d/ctrl-u`| Page down/up                                     |
| `/`            | Search/filter                                    |
| `d`            | Toggle detail/summary                            |
| `f`            | Filter by state                                  |
| `F`            | Re-enable auto-follow                            |
| `enter`        | Drill in / confirm                               |
| `esc`          | Go back / cancel                                 |
| `q`            | Quit                                             |
| `?`            | Show keybindings help                            |

A keybindings help footer is always visible at the bottom of TUI views showing
the most relevant bindings for the current context.

### Smart Auto-Collapse

Used in simulation preview and status/watch views. The algorithm:

1. Render the full content tree
2. Measure against available viewport height
3. If it fits → show expanded
4. If it overflows → collapse into groups with counts, user expands on demand

Groups in order: target updates, stack updates, resource updates (sub-grouped
by operation: creates, updates, deletes), policy updates.

### Auto-Follow

For live watch views:

- By default, the viewport tracks in-progress items
- Manual scroll (j/k/ctrl-d/ctrl-u) disables auto-follow
- `F` re-enables auto-follow
- Visual indicator in the footer when auto-follow is active/disabled

### Responsive Layout

All views query terminal dimensions and adapt:

- Tables drop lower-priority columns as width shrinks
- Text truncates with ellipsis rather than wrapping destructively
- Panels resize proportionally
- Below a minimum usable width: show "terminal too narrow" message
- Terminal resize events (bubbletea `WindowSizeMsg`) trigger re-layout

## Architecture

### Package Structure

```
internal/cli/tui/
    theme/          - Color palettes, lipgloss style definitions, theme loading
    components/     - Shared bubbletea components (table, progress, viewport, tree, status)
    launch.go       - tui.Run() helper for terminal setup/teardown
    keys.go         - Shared keybinding definitions

internal/cli/apply/     - Apply command (TUI flow)
internal/cli/destroy/   - Destroy command (TUI flow)
internal/cli/cancel/    - Cancel command (TUI flow)
internal/cli/status/    - Status command (TUI, hosts the shared watch component)
internal/cli/inventory/ - Inventory command (TUI)
internal/cli/eval/      - Eval command (lipgloss formatted output)
internal/cli/extract/   - Extract command (light interactivity + formatted output)
internal/cli/project/   - Project init (huh forms)
internal/cli/plugin/    - Plugin init (huh forms)
internal/cli/renderer/  - Replaced by tui/ (deleted after migration)
internal/cli/display/   - Replaced by tui/theme/ (deleted after migration)
internal/cli/printer/   - HumanReadablePrinter refactored, MachineReadablePrinter unchanged
internal/cli/prompter/  - Replaced by huh (deleted after migration)
```

### Output Routing

The existing human-vs-machine split is preserved. Each command checks the output
consumer flag early:

```
if opts.OutputConsumer == printer.ConsumerMachine {
    // existing machine-readable path — unchanged
    return runForMachines(...)
}
// new TUI/formatted path
return runForHumans(...)
```

**Non-TTY fallback:** When human output is selected but stdout is not a TTY
(e.g., piped to a file or `less`), bubbletea TUI mode is not launched. Instead,
the command falls back to a simplified print-and-exit path using lipgloss
styling with color auto-disabled (lipgloss handles this via termenv). This
ensures `formae status | less` produces readable output rather than failing or
producing garbled escape sequences.

### Nag/Tips System

The current nag system (`internal/cli/nag/`) displays random tips after
commands. In the new TUI:

- For TUI commands: nags are displayed after the TUI exits (printed to stdout
  after `tea.Program` returns), not within the TUI itself
- For print-and-exit commands: nags are displayed as today, but styled with
  lipgloss
- Nags are never shown in machine-readable output (as today)

### Terminal Capability Detection

At startup (in `tui.Run()` or theme initialization):

1. Detect color profile via termenv (true color / 256 / 16 / none)
2. Detect Sixel or Kitty graphics support for logo rendering
3. Select appropriate theme variant and logo renderer
4. Store in a context or config object passed to all components

## Testing Strategy

The project follows London-school TDD. The current CLI has very limited test
coverage — this rework is an opportunity to make significant improvements.
Testing is organized in three layers.

### Layer 1: Unit Tests (fast, many — the bulk of coverage)

Test bubbletea models as pure functions, no goroutines or I/O needed.

- **`Update()` tests**: Table-driven tests that send a `tea.Msg` to a model and
  assert on the resulting model state and returned `tea.Cmd`. This is the bread
  and butter — every message type each model handles gets covered.
- **`View()` tests**: Construct a model in a specific state, call `View()`,
  assert that the output string contains expected content. Useful for verifying
  responsive layout behavior at different terminal widths.
- **`tea.Cmd` tests**: Commands encapsulate side effects. Test them by calling
  the function and asserting on the returned `tea.Msg`. For commands that call
  the API, inject a mock client (see below).
- **Theme/styling tests**: Verify that theme definitions produce expected
  lipgloss styles and that the palette is internally consistent.
- **Shared component tests**: Each shared component (table, progress bar,
  viewport, tree, status indicator) is a bubbletea model testable via
  `Update()`/`View()` in isolation.

### Layer 2: Component Integration Tests (teatest)

Test full TUI interaction flows using `teatest` from the Charmbracelet
ecosystem (`github.com/charmbracelet/bubbletea/teatest`).

`teatest` runs a real `tea.Program` in a goroutine with piped stdin/stdout:

- `NewTestModel(t, model, WithInitialTermSize(80, 24))` — start a test program
- `Send(msg)` — inject messages, `Type(s)` — simulate keyboard input
- `WaitFor(t, output, condition)` — poll output until condition matches
- `FinalOutput(t)` / `FinalModel(t)` — inspect end state

**What to test at this layer:**

- Each command's TUI model end-to-end: key interaction flows (scroll, filter,
  detail/summary toggle, confirm/abort, drift resolution choices)
- Multi-step flows: simulation preview → confirmation → watch transition
- Error rendering: inject error responses, verify error panels render correctly
- Non-TTY fallback: verify commands produce sensible output when stdout is not
  a TTY
- Terminal width: verify responsive layout at various widths (wide, narrow,
  minimum threshold)

**Golden file regression tests:** `teatest` provides `RequireEqualOutput(t, bts)`
which compares rendered output against snapshot files in `testdata/`. Updated
with `go test -update`. This catches unintended rendering changes. Note: v1
golden files contain raw ANSI escape sequences, so they are sensitive to styling
changes. If this becomes a maintenance burden, consider adopting `teatest` v2
(experimental, uses a virtual terminal emulator for clean rendered text in
golden files) when it stabilizes.

**API dependency injection:** TUI models receive an API client interface. At
this layer, use a mock implementation that returns canned responses:

```go
type MockAPIClient struct {
    responses []any
    errors    []error
}
```

This keeps integration tests fast and deterministic — no HTTP involved.

### Layer 3: CLI E2E Tests (httptest + FakeMetastructure + teatest)

Full round-trip tests that exercise the actual HTTP layer, serialization, error
handling, and TUI rendering together.

**Architecture:**

```
CLI command → App layer → HTTP client → httptest.NewServer(Echo + FakeMetastructure)
```

- Spin up a real Echo server backed by `FakeMetastructure` (already exists in
  `internal/api/server_test.go`) via `httptest.NewServer`
- `FakeMetastructure` implements `MetastructureAPI` with queued responses —
  each test enqueues the exact sequence of responses the scenario needs
- Run the actual CLI command flow through `teatest`
- This tests the full stack: multipart form encoding, HTTP headers (Client-ID),
  response parsing, typed error handling, TUI rendering

**What to test at this layer:**

- Apply flow: submit → simulate response → confirmation → execute response →
  status polling → completion
- Destroy flow: same, including cascade delete scenarios
- Cancel flow: submit cancel → watch cancellation progress
- Status flow: multi-command dashboard, drill-down navigation
- Inventory flow: paginated results, truncation hints
- Error scenarios: reconcile rejected (drift resolution), conflicting commands,
  patch rejected, referenced resources not found — all 14+ error types
- The async command pattern: 202 Accepted → status polling → final state

**Why extend FakeMetastructure rather than generate a stub:**

The `FakeMetastructure` implements the `MetastructureAPI` Go interface. If the
interface changes, the fake won't compile — this guarantees the stub can't
drift from the real API at the type level. Generating a mock server from the
swagger spec would require a separate drift-detection mechanism.

For REST contract regression (catching changes in HTTP routes, status codes,
serialization), add a CI check that diffs `docs/swagger.json` against the
previous version and flags breaking changes. The swagger spec is already
auto-generated from annotations in `internal/api/server.go`.

### Test Organization

```
internal/cli/tui/
    theme/theme_test.go           - Theme/palette unit tests
    components/table_test.go      - Shared component unit tests
    components/progress_test.go
    components/viewport_test.go
    components/tree_test.go
    components/testdata/          - Golden files for components

internal/cli/apply/
    apply_test.go                 - Apply TUI unit + integration tests
    apply_e2e_test.go             - Apply full round-trip e2e tests
    testdata/                     - Golden files

internal/cli/status/
    status_test.go                - Status TUI unit + integration tests
    status_e2e_test.go            - Status full round-trip e2e tests
    testdata/

... (same pattern for each command)

internal/cli/testutil/
    fake_api.go                   - Mock API client for layer 2 tests
    e2e_helpers.go                - Helpers for layer 3 (httptest setup,
                                    FakeMetastructure configuration, teatest
                                    wrappers)
```

Machine-readable output tests remain largely unchanged — they don't use
bubbletea and can continue to assert on JSON/YAML output directly.

## Implementation Notes

**Theme config requires updates in two places:** The `theme` field must be added
to both the PKL schema (`internal/schema/pkl/assets/formae/Config.pkl` —
`CliConfig` class) and the Go config struct (`pkg/model/config.go` —
`CliConfig` struct).

**Smart auto-collapse optimization:** For very large update lists (hundreds of
resources), the auto-collapse algorithm should estimate line count from data
size first and only render the expanded tree if the estimate fits the viewport.
This avoids an expensive double-render (expand, measure, collapse, re-render)
on large commands.

**Sixel/Kitty detection:** Terminal graphics protocol detection requires
querying the terminal with escape sequences and waiting for a response, which
can have timing issues on some terminals. The detection should have a short
timeout with braille fallback as the safe default.

## Mockup Plan

Before writing implementation code, all commands will be mocked up:

1. **ASCII/text mockups** first — nail down information architecture, layout
   structure, what goes where
2. **Terminal prototypes** second — small Go programs using the actual
   Charmbracelet libraries for visual polish and color testing
3. **Iterate** until the full command set has a coherent, validated design

Mock up in this order (from most complex to simplest):
1. Status/watch view (core shared component)
2. Simulation preview (apply)
3. Drift resolution (apply)
4. Inventory tables
5. Project init form
6. Formatted outputs (eval, extract, status agent)
7. Error panels
8. Logo/banner
