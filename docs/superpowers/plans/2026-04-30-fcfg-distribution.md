# fcfg Distribution & README Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bundle `fcfg` in the `formae` orbital package and add a colocated README at `cmd/formae-config/README.md`.

**Architecture:** Two file changes, no new code. (1) Extend `Makefile`'s `pkg-bin` target to copy `./fcfg` into `./dist/pel/bin/` alongside `./formae`; the `Opkgfile` already bundles everything in `dist/pel/`, so no manifest edit is needed. (2) Create the README. Spec: `docs/superpowers/specs/2026-04-30-fcfg-distribution-design.md`.

**Tech Stack:** GNU Make, Markdown.

---

## File Structure

```
Makefile                                  # one-line addition to pkg-bin target
cmd/formae-config/README.md               # new file, ~40 lines
docs/superpowers/specs/
  2026-04-30-fcfg-distribution-design.md  # spec (already exists)
```

Conventions:
- Commits go directly on the existing `feat/fcfg` branch in the worktree at `/Users/jeroen/dev/pel/formae/.worktrees/fcfg`.
- Conventional commit messages, no Claude attribution.
- The README has no license header — markdown convention is no header.

---

## Task 1: Add fcfg to `pkg-bin`

**Files:**
- Modify: `Makefile` (the `pkg-bin` target, currently around lines 27-32 in the worktree's Makefile)

No tests for this task — `make pkg-bin` itself is the verification. We confirm both binaries land in `./dist/pel/bin/`.

- [ ] **Step 1: Inspect the current `pkg-bin` target**

Run: `grep -n -A 5 '^pkg-bin' /Users/jeroen/dev/pel/formae/.worktrees/fcfg/Makefile`

Expected output (the body should match this exactly; line numbers may differ):

```
pkg-bin: clean build
	echo '${VERSION}' > ./version.semver
	mkdir -p ./dist/pel/bin
	cp -Rp ./formae ./dist/pel/bin
```

- [ ] **Step 2: Add a line to copy `fcfg` after the existing `formae` copy**

Use the Edit tool on `/Users/jeroen/dev/pel/formae/.worktrees/fcfg/Makefile`.

Replace this exact block:

```makefile
pkg-bin: clean build
	echo '${VERSION}' > ./version.semver
	mkdir -p ./dist/pel/bin
	cp -Rp ./formae ./dist/pel/bin
```

with:

```makefile
pkg-bin: clean build
	echo '${VERSION}' > ./version.semver
	mkdir -p ./dist/pel/bin
	cp -Rp ./formae ./dist/pel/bin
	cp -Rp ./fcfg ./dist/pel/bin
```

(Ensure both `cp` lines are tab-indented like the surrounding Makefile rules — Make requires tabs, not spaces, for recipe lines.)

- [ ] **Step 3: Verify `make pkg-bin` produces both binaries**

From the worktree:

```bash
make pkg-bin
ls -1 ./dist/pel/bin/
```

Expected output of `ls`:

```
fcfg
formae
```

- [ ] **Step 4: Confirm the two binaries are functional copies**

```bash
./dist/pel/bin/fcfg --help | head -3
./dist/pel/bin/formae --version
```

Expected `fcfg --help` first line: `Manage named profiles for ~/.config/formae/`

Expected `formae --version`: a non-empty version string (no error).

- [ ] **Step 5: Clean up the build artifacts before committing**

```bash
rm -rf ./dist ./version.semver
```

(`pkg-bin` is a build target; `dist/` and `version.semver` are build outputs that should not be committed. The `clean:` target removes them, but a manual `rm -rf` is sufficient and avoids side effects.)

- [ ] **Step 6: Confirm the working tree only contains the Makefile change**

```bash
git -C /Users/jeroen/dev/pel/formae/.worktrees/fcfg status
git -C /Users/jeroen/dev/pel/formae/.worktrees/fcfg diff Makefile
```

Expected `git status`: only `Makefile` modified (the `fcfg` and possibly `formae` build artifacts at the repo root are pre-existing untracked files and can stay).

Expected `git diff` output:

```diff
@@ pkg-bin: clean build
 	echo '${VERSION}' > ./version.semver
 	mkdir -p ./dist/pel/bin
 	cp -Rp ./formae ./dist/pel/bin
+	cp -Rp ./fcfg ./dist/pel/bin
```

- [ ] **Step 7: Commit**

```bash
cd /Users/jeroen/dev/pel/formae/.worktrees/fcfg
git add Makefile
git commit -m "build(fcfg): bundle fcfg in formae opkg"
```

Commit message MUST NOT contain any `Co-Authored-By` or `Claude` attribution. Verify:

```bash
git log -1 --format='%B'
```

The output should be exactly one line: `build(fcfg): bundle fcfg in formae opkg`.

---

## Task 2: Add `cmd/formae-config/README.md`

**Files:**
- Create: `cmd/formae-config/README.md`

No tests — this is documentation. Verification is reading the file.

- [ ] **Step 1: Create the README**

Create `/Users/jeroen/dev/pel/formae/.worktrees/fcfg/cmd/formae-config/README.md` with this EXACT content:

````markdown
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
````

- [ ] **Step 2: Sanity-check the file renders**

```bash
cat /Users/jeroen/dev/pel/formae/.worktrees/fcfg/cmd/formae-config/README.md | head -5
wc -l /Users/jeroen/dev/pel/formae/.worktrees/fcfg/cmd/formae-config/README.md
```

Expected first 5 lines:

```
# fcfg

`fcfg` manages named profiles for `~/.config/formae/`. The active config is a symlink that switches atomically.

## Install
```

Expected `wc -l`: roughly 50 lines (anything in the 40-60 range is fine; an exact match is not required because table formatting and blank lines vary).

- [ ] **Step 3: Confirm the working tree only contains the new README**

```bash
git -C /Users/jeroen/dev/pel/formae/.worktrees/fcfg status
```

Expected: `cmd/formae-config/README.md` listed as a new untracked file. Pre-existing untracked artifacts (`fcfg`, `formae` binaries) at the repo root may also appear and should be left alone.

- [ ] **Step 4: Commit**

```bash
cd /Users/jeroen/dev/pel/formae/.worktrees/fcfg
git add cmd/formae-config/README.md
git commit -m "docs(fcfg): add README at cmd/formae-config/"
```

Verify the commit message has no attribution:

```bash
git log -1 --format='%B'
```

Output should be exactly one line: `docs(fcfg): add README at cmd/formae-config/`.

---

## Task 3: Final verification

- [ ] **Step 1: Confirm both commits are on the branch**

```bash
git -C /Users/jeroen/dev/pel/formae/.worktrees/fcfg log --oneline -3
```

Expected: the two new commits at the top of the log:

```
<sha2> docs(fcfg): add README at cmd/formae-config/
<sha1> build(fcfg): bundle fcfg in formae opkg
ac10558d docs: spec for fcfg distribution and README
```

- [ ] **Step 2: Confirm `make pkg-bin` still produces both binaries (sanity-rebuild)**

```bash
cd /Users/jeroen/dev/pel/formae/.worktrees/fcfg
make pkg-bin
ls -1 ./dist/pel/bin/
rm -rf ./dist ./version.semver
```

Expected `ls`:

```
fcfg
formae
```

- [ ] **Step 3: Confirm the README is browsable on disk**

```bash
ls /Users/jeroen/dev/pel/formae/.worktrees/fcfg/cmd/formae-config/README.md
```

Expected: a single line with the full path; no error.

- [ ] **Step 4: Hand off**

The branch is ready for review. The original `feat/fcfg` PR (or a new PR) can include both this distribution work and the original implementation.
