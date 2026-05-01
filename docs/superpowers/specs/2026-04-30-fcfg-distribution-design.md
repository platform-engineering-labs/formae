# fcfg Distribution & README

**Date:** 2026-04-30
**Status:** Approved — pending implementation plan

## Problem

`fcfg` was implemented under spec `2026-04-29-fcfg-config-switcher-design.md` and lives at `cmd/formae-config/`, but it is not yet bundled in the `formae` orbital package and has no user-facing README. Today the binary is built by `make build` but not copied into `dist/pel/bin/` by `pkg-bin`, so the published `.opkg` ships only `formae`.

## Goals

- Include `fcfg` in the same `formae` orbital package so users who install `formae` automatically get `fcfg` on their PATH.
- Add a colocated README at `cmd/formae-config/README.md` that explains what `fcfg` does, how to use it, and points at the Claude skill.

## Non-goals

- Splitting `fcfg` into its own `.opkg` (deferred; can revisit if independent versioning becomes useful).
- Updating the top-level `README.md` or the docs.formae.io content (release notes are the right place for an announcement, not the source-tree README).
- Adding a `fcfg`-specific CI job. The existing per-platform `formae-package` workflow will pick up the new binary automatically once `pkg-bin` copies it.

## Distribution

The orbital package manifest at `Opkgfile` declares:

```
name = "formae"
version =  semver.Version(read("env:VERSION"))
publisher = "platform.engineering"
license = "FLS 1.1-ALv2"
summary = "formae - infrastructure as code for humans in the AI era"
description = "formae - infrastructure as code for humans in the AI era"

requirements = new Listing<requirement.Requirement> {
  new {
    name = "pkl"
    method = "depends"
    operator = "ANY"
  }
}
```

The opkg bundles whatever ends up in `dist/pel/`. The `pkg-bin` target currently populates `dist/pel/bin/` with `./formae`. Extending it to also copy `./fcfg` is sufficient — no manifest change is required.

The change to `Makefile`:

```makefile
pkg-bin: clean build
	echo '${VERSION}' > ./version.semver
	mkdir -p ./dist/pel/bin
	cp -Rp ./formae ./dist/pel/bin
	cp -Rp ./fcfg ./dist/pel/bin
```

CI flow (no edits needed):

1. `.github/workflows/package.yml` runs `just publish` per platform.
2. `justfile`'s `publish` target depends on `pkg`, which depends on `build`, which calls `make pkg-bin`. Both binaries now land in `dist/pel/bin/`.
3. `ops opkg build --secure --target-path dist/pel` packages everything in `dist/pel/`.
4. `ops publish --repo pel --channel <channel> *.opkg` uploads.

Versioning: `fcfg` ships with the same version as `formae` (the env-injected `VERSION`). It is not version-stamped in its own binary and does not need to be — anyone running `fcfg` against `~/.config/formae/` is on a single install.

## README

Location: `cmd/formae-config/README.md`. Colocated with the binary's source. GitHub renders it inline when browsing the directory.

Length target: ~80 lines.

### Outline

1. **Title and one-paragraph intro.** What `fcfg` does ("manages named profiles for `~/.config/formae/`; the active config is an atomic symlink").
2. **Install.** One short paragraph:
   > Ships alongside `formae`. If you have `formae`, you also have `fcfg` on your PATH. From source: `make build && cp ./fcfg ~/.local/bin/`.
3. **Command reference.** A bulleted list of all eight commands (`init`, `current`, `list`, `use`, `save`, `edit`, `delete`, `diff`) with a one-line description each, mirroring `fcfg --help`. Notable flags inline: `--name`/`--yes` on `init`, `--json` on `list`, `--force` on `save`. The `diff` line includes the exit-code note ("exit 1 means files differ; only >1 are errors").
4. **Example workflow.** A short concrete sequence — switching between `local-dev` and `load-test`:
   ```
   fcfg init --yes                # one-time, migrates the existing config to profile "default"
   fcfg save local-dev            # snapshot under a new name
   fcfg edit local-dev            # tweak it
   fcfg use local-dev             # switch
   # ...later...
   fcfg save load-test            # snapshot the active profile
   fcfg edit load-test            # change endpoints/credentials/etc.
   fcfg use load-test             # switch
   ```
   Followed by a one-line reminder: "fcfg only moves files; restart the formae agent yourself if it is running."
5. **Exit codes.** A four-row table (0/1/2/3 with one-line meaning each).
6. **Claude integration.** One-paragraph pointer: this directory is paired with `.superpowers/skills/formae-config/SKILL.md`, which teaches Claude Code to drive `fcfg`. No setup needed when the skill repo is loaded.
7. **License.** One-line: "FSL-1.1-ALv2 (same as formae)."

### Tone

Match the existing `cmd/formae/main.go`-style internal docs: concise, declarative, no marketing copy. The top-level `README.md` carries the project pitch; this is a tool reference.

## Out of scope

- Top-level `README.md` change.
- Updates to docs.formae.io (`formae-docs` repo).
- A `fcfg-specific.opkg` package or independent versioning.
- Documentation of `Opkgfile` semantics (out of scope for this work; the existing manifest stays as-is).
- Removing or restructuring the `pelmgr` binary that lives in the user's working tree (untracked, untouched here).
