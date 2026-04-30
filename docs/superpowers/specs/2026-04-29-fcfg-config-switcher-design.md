# fcfg — Formae Config Profile Switcher

**Date:** 2026-04-29
**Status:** Approved — pending implementation plan

## Problem

The formae agent reads its configuration from `~/.config/formae/formae.conf.pkl`. In practice we maintain several variants of this file (local dev, load test, prod, etc.) and switch between them by manually copying files and keeping ad-hoc backups. This is error-prone and undocumented.

We want a small command-line tool that manages named configuration profiles for `~/.config/formae/`, and a Claude skill so the same operations can be driven from a Claude Code session.

## Goals

- Manage a set of named formae configuration profiles on disk.
- Make switching between profiles a single, atomic operation.
- Cover the full lifecycle: list, show active, switch, snapshot, edit, delete, diff.
- Provide a Claude skill that exposes the tool to Claude Code sessions.

## Non-goals

- Knowing anything about formae's internal config schema.
- Detecting or managing the formae agent's lifecycle. The tool only moves files; restarting the agent after a switch is the user's responsibility.
- Validating profile contents (no PKL parsing, no schema checks).
- Cross-machine sync, encryption, or remote storage.

## On-disk layout

```
~/.config/formae/
  formae.conf.pkl              -> profiles/<active>.pkl   (symlink)
  profiles/
    default.pkl
    local-dev.pkl
    load-test.pkl
    prod.pkl
```

- The active config is always a symlink at `~/.config/formae/formae.conf.pkl` pointing into `profiles/`. Formae itself reads through the symlink and is unaware of the indirection.
- A profile's name is its filename without the `.pkl` extension. Allowed characters: `[a-zA-Z0-9_-]+`. No slashes, no spaces, no leading dots.
- Switching: write a new symlink to a temp name in the same directory, then `os.Rename` it over the existing symlink. POSIX guarantees this is atomic.
- "Active profile" is determined by reading the symlink target. There is no separate state file.

The config root is resolved as: `$FORMAE_CONFIG_DIR` → `$XDG_CONFIG_HOME/formae` → `~/.config/formae`. The first env var is provided as a test seam; `XDG_CONFIG_HOME` is the standard.

## CLI surface

Binary name: `fcfg`. CLI built with Cobra (consistent with the formae CLI itself).

```
fcfg init [--name <name>] [--yes]
fcfg list [--json]
fcfg current
fcfg use <name>
fcfg save <name> [--force]
fcfg edit [<name>]
fcfg delete <name>
fcfg diff <a> [<b>]
```

### Behaviors

- **`init`** — converts an existing regular `formae.conf.pkl` into `profiles/<name>.pkl` and replaces the original path with a symlink. Default name is `default`; override with `--name`. Prints the action and prompts to confirm; `--yes` skips the prompt. Idempotent: if the path is already a symlink into `profiles/`, exits 0 with "already initialized". Errors with exit code 1 if `formae.conf.pkl` does not exist (the user should write a starting config there first).

- **`list`** — prints every file in `profiles/` (without `.pkl` extension), one per line, sorted, with `*` next to the active one. With `--json`, emits a single JSON object (see below).

- **`current`** — prints the active profile name on a single line. Exits 3 if not initialized.

- **`use <name>`** — atomic symlink swap to `profiles/<name>.pkl`. Errors if `<name>` does not exist (no implicit create). On success prints `switched to <name>`.

- **`save <name>`** — copies the *resolved* current active file (i.e., `readlink` then read; not the symlink itself) to `profiles/<name>.pkl`. Refuses to overwrite without `--force`. Does **not** switch to the new profile — that's a separate `use` step. This makes `save` non-destructive: you can snapshot variants without losing your place.

- **`edit [<name>]`** — execs `$EDITOR` (fallback `vi`) on `profiles/<name>.pkl`, or on the active profile if `<name>` is omitted. No validation of the result.

- **`delete <name>`** — removes `profiles/<name>.pkl`. Refuses if `<name>` is the active profile; the safe path is to `use` a different profile first, then `delete`. There is no `--force` override for this.

- **`diff <a> [<b>]`** — runs `diff -u profiles/<a>.pkl profiles/<b>.pkl`. If `<b>` is omitted, diffs `<a>` against the active profile. Pass-through exit semantics: `0` = identical, `1` = differ, `>1` = error.

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | User error (missing profile, invalid name, overwrite without `--force`) |
| 2 | Filesystem / permission error |
| 3 | Not initialized (no symlink at `formae.conf.pkl`) |

`diff`'s exit codes are passed through directly (so `1` from `diff` collides with our `1`; this is acceptable because `diff -u` already distinguishes via output, and scripts that need to distinguish should use `--json` on `list` and check files directly).

### JSON output

Only `list --json` produces JSON:

```json
{"active": "local-dev", "profiles": ["default", "load-test", "local-dev", "prod"]}
```

`active` is `null` if there is no symlink (uninitialized state). Other commands stay human-readable; the surface is small enough that the Claude skill doesn't need structured output for them.

## Code organization

```
cmd/formae-config/
  main.go                # Cobra root + subcommand wiring
  internal/
    paths/               # config dir resolution
      paths.go
      paths_test.go
    profiles/            # all filesystem operations
      profiles.go
      profiles_test.go
    cmd/                 # one file per subcommand
      init.go
      list.go
      current.go
      use.go
      save.go
      edit.go
      delete.go
      diff.go
```

- The `profiles` package owns all filesystem layout knowledge. Public surface: `List`, `Active`, `Use`, `Save`, `Delete`, `Init`, `ProfilePath`, `ConfigPath`, plus error sentinels (`ErrNotInitialized`, `ErrAlreadyExists`, `ErrIsActive`, `ErrNotFound`).
- Subcommand files are thin: parse args → call into `profiles` → render output / set exit code.
- The `paths` package is a tiny seam that resolves the config root from env vars. Tests override the root via `FORMAE_CONFIG_DIR`.
- Cobra is already a dependency of formae.
- Build target: extend the existing `make build` to also build `cmd/formae-config`. Output binary: `./bin/fcfg`.

## Testing

- **Unit tests on `profiles`** using `t.TempDir()` to create an isolated config root per test:
  - `init` migrates a regular file, is idempotent on a symlink, creates from empty.
  - `use` swaps atomically, errors on missing target, refuses bad names.
  - `save` copies the resolved file, refuses overwrite, `--force` overrides, does not switch.
  - `delete` refuses the active profile, removes others.
  - Edge cases: broken symlink, missing `profiles/` dir, profile name validation.
- **Integration tests** on the binary using `testscript` (with `.txtar` scenarios) covering one or two end-to-end lifecycle flows.
- No tests for `edit` (exec-ing `$EDITOR` is hard to test usefully) or `diff` (we trust `/usr/bin/diff`).
- `make lint` covers the new packages with no special configuration.

## Claude skill

A single-file skill teaching Claude how to drive `fcfg`.

**Location:** `.superpowers/skills/formae-config/SKILL.md` — colocated with the tool so it ships in the same change. (`.superpowers/` already exists in the repo.)

**Frontmatter** (`description`):

> Use when the user wants to switch, list, save, edit, delete, or compare formae configuration profiles in `~/.config/formae/`. Examples: "switch to load-test config", "show me my formae configs", "snapshot the current config as <name>".

**Body** — short reference card, ≤ ~50 lines:

- One-paragraph overview: `fcfg` is a thin file mover for named formae config profiles; it does not manage the formae agent.
- Command reference (one line per command).
- "Use `fcfg list --json` for parsing; everything else is human-readable."
- "Always pass `--yes` to `init` (no interactive prompts in agent contexts)."
- "If a command exits 3, the user hasn't run `fcfg init` yet."
- One worked example: user asks to switch to `load-test` → run `fcfg use load-test` → confirm to user.

The skill does **not** wrap multi-step workflows. Claude composes them from the reference.

## Out of scope / future

The following are deliberately deferred. They are not part of this design and should not be implemented under it:

- Detecting or restarting the formae agent.
- Importing profiles from other paths (e.g., `fcfg save --from <path>`).
- Validating PKL contents.
- Profiles consisting of multiple files (subdirectories).
- Cross-machine sync.
- A `fcfg rename` command (workaround: `save <new>` then `delete <old>`).
