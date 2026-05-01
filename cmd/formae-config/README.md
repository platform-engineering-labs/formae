# fcfg

`fcfg` manages named profiles for `~/.config/formae/`. The active config is a symlink that switches atomically.

## Install

Ships alongside `formae`. If you have `formae`, you also have `fcfg` on your PATH. From source:

```bash
make build
cp ./fcfg ~/.local/bin/
```

## Commands

- `fcfg init [--name <name>] [--yes]` — convert an existing `formae.conf.pkl` into a profile and replace it with a symlink. Default name `default`. `--yes` skips the confirmation prompt.
- `fcfg list [--json]` — list profiles, marking the active one with `*`. Use `--json` for parsing.
- `fcfg current` — print the active profile name.
- `fcfg use <name>` — atomically switch the active profile.
- `fcfg save <name> [--force]` — snapshot the active profile under a new name. Does not switch. `--force` overwrites.
- `fcfg edit [<name>]` — open `$EDITOR` on a profile (or the active one).
- `fcfg delete <name>` — delete a profile. Refuses if it is the active one — switch first.
- `fcfg diff <a> [<b>]` — `diff -u` between two profiles (or `<a>` vs the active profile). Exit code 1 means files differ; only codes >1 are errors.

## Example: switching between local-dev and load-test

```
fcfg init --yes        # one-time, migrates the existing config to profile "default"
fcfg save local-dev    # snapshot under a new name
fcfg edit local-dev    # tweak it
fcfg use local-dev     # switch
# ...later...
fcfg save load-test    # snapshot the active profile
fcfg edit load-test    # change endpoints/credentials/etc.
fcfg use load-test     # switch
```

`fcfg` only moves files; restart the formae agent yourself if it is running.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | User error (missing profile, invalid name, overwrite without `--force`, etc.) |
| 2 | Filesystem / permission error |
| 3 | Not initialized — run `fcfg init --yes` first |

## Claude Code integration

If you use Claude Code, this directory is paired with a skill at `.superpowers/skills/formae-config/SKILL.md` that teaches Claude how to drive `fcfg`. No setup needed; Claude Code picks it up automatically when the skill is loaded.

## License

FSL-1.1-ALv2 (same as formae).
