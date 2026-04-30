---
name: formae-config
description: Use when the user wants to switch, list, save, edit, delete, or compare formae configuration profiles in ~/.config/formae/. Examples — "switch formae to load-test", "show me my formae configs", "snapshot the current formae config as <name>", "what formae config am I on?".
---

# formae-config

`fcfg` is a small CLI that manages named profiles for `~/.config/formae/`. The active config is a symlink at `~/.config/formae/formae.conf.pkl` pointing into `~/.config/formae/profiles/`. `fcfg` only moves files; it does **not** restart the formae agent. After `fcfg use <name>` the user must restart the agent themselves if it is running.

## Command reference

- `fcfg init [--name <name>] [--yes]` — convert an existing `formae.conf.pkl` into a profile and replace it with a symlink. Always pass `--yes` from agent contexts.
- `fcfg list [--json]` — list profiles. Use `--json` when you need to parse the output.
- `fcfg current` — print the active profile name on a single line.
- `fcfg use <name>` — atomically switch the active profile.
- `fcfg save <name> [--force]` — snapshot the active profile under a new name. Does not switch. `--force` overwrites an existing profile.
- `fcfg edit [<name>]` — open `$EDITOR` on a profile (or the active one). Skip from agent contexts; edit the file directly instead.
- `fcfg delete <name>` — delete a profile. Refuses if it is the active one — switch first.
- `fcfg diff <a> [<b>]` — `diff -u` between two profiles, or `<a>` vs the active profile.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | User error (missing profile, invalid name, overwrite without `--force`, etc.) |
| 2 | Filesystem / permission error |
| 3 | Not initialized — run `fcfg init --yes` first |

## JSON output

Only `fcfg list --json` produces JSON:

```json
{"active": "local-dev", "profiles": ["default", "load-test", "local-dev", "prod"]}
```

`active` is `null` if the user has not run `fcfg init` yet.

## Worked example

User: "switch my formae to load-test"

1. Run `fcfg use load-test`.
2. If exit code 3: tell the user they need to run `fcfg init --yes` first.
3. If exit code 1: report the error from stderr (likely "profile not found" — suggest `fcfg list` to see what's available).
4. On success: confirm to the user, and remind them to restart the formae agent if it is running.
