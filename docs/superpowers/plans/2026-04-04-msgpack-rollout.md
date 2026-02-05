# MessagePack Serialization Rollout Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the msgpack serialization PR by integrating the Ergo v320 upgrade, generalizing plugin ref pinning in the Makefile, and coordinating plugin repo updates.

**Architecture:** The msgpack serialization code is done on `feat/msgpack-serialization` (based on `feat/extensions-coordinator`). This plan adds the Ergo upgrade cherry-pick, replaces the ad-hoc `*_PLUGIN_REF` mechanism with a `url@ref` pattern in `EXTERNAL_PLUGIN_REPOS`, and updates the e2e workflow to match.

**Tech Stack:** Make, GitHub Actions YAML, git

**Worktree:** `/home/jeroen/dev/pel/formae-msgpack` (branch `feat/msgpack-serialization`)

---

### Task 1: Cherry-pick Ergo v320 upgrade

The Ergo v310 → v320 upgrade lives as a single commit `23f925dd` on `feat/ergo-v3.2.0-upgrade`. It needs to be integrated into our branch.

**Files:**
- Modify: `go.mod` (Ergo version bump)
- Modify: `go.sum`
- Modify: various files touched by the Ergo upgrade commit

- [ ] **Step 1: Cherry-pick the Ergo upgrade commit**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
git cherry-pick 23f925dd
```

If there are conflicts (likely in `go.mod`, `go.sum`, or `edf_registration.go`), resolve them:
- `go.mod`: keep Ergo v1.999.320
- `edf_registration.go`: keep our simplified 13-type registration
- Any other conflicts: prefer our msgpack branch changes, then re-apply the Ergo-specific changes

- [ ] **Step 2: Tidy and verify build**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
go mod tidy
cd pkg/plugin && go mod tidy && cd ../..
go build ./...
```

Expected: Clean build

- [ ] **Step 3: Run tests**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
make test-all
```

Expected: All tests pass (same as before cherry-pick)

- [ ] **Step 4: Verify Ergo version**

```bash
grep "ergo.services/ergo" /home/jeroen/dev/pel/formae-msgpack/go.mod
```

Expected: `ergo.services/ergo v1.999.320`

---

### Task 2: Generalize plugin ref pinning in the Makefile

Replace the ad-hoc `AZURE_PLUGIN_REF` / `AWS_PLUGIN_REF` variables with a `url@ref` convention in `EXTERNAL_PLUGIN_REPOS`. This scales to all plugins and makes the pinning visible where repos are defined.

**Files:**
- Modify: `Makefile:13-76`

- [ ] **Step 1: Update `EXTERNAL_PLUGIN_REPOS` documentation**

Replace lines 12-27 in the Makefile:

```makefile
# External plugin Git repositories to bundle.
# Append @branch or @tag to pin a specific ref (e.g., ...aws.git@feat/msgpack).
# Without @ref, the default branch (main) is used.
EXTERNAL_PLUGIN_REPOS ?= \
    https://github.com/platform-engineering-labs/formae-plugin-aws.git \
    https://github.com/platform-engineering-labs/formae-plugin-azure.git \
    https://github.com/platform-engineering-labs/formae-plugin-compose.git \
    https://github.com/platform-engineering-labs/formae-plugin-gcp.git \
    https://github.com/platform-engineering-labs/formae-plugin-grafana.git \
    https://github.com/platform-engineering-labs/formae-plugin-oci.git \
    https://github.com/platform-engineering-labs/formae-plugin-ovh.git

# Directory for cloned plugins
PLUGINS_CACHE := .plugins
```

Remove the old `AZURE_PLUGIN_REF` and `AWS_PLUGIN_REF` variables (lines 25-27).

- [ ] **Step 2: Update `fetch-external-plugins` target**

Replace the entire `fetch-external-plugins` target (lines 52-75) with:

```makefile
## fetch-external-plugins: Clone/update external plugin repositories
## Supports @ref suffix on repo URLs (e.g., repo.git@feat/branch)
fetch-external-plugins:
	@mkdir -p $(PLUGINS_CACHE)
	@for entry in $(EXTERNAL_PLUGIN_REPOS); do \
		ref=$$(echo "$$entry" | grep -o '@.*$$' | sed 's/^@//'); \
		repo=$$(echo "$$entry" | sed 's/@[^@]*$$//'); \
		name=$$(basename $$repo .git); \
		if [ -d "$(PLUGINS_CACHE)/$$name" ]; then \
			echo "Updating $$name..."; \
			git -C "$(PLUGINS_CACHE)/$$name" fetch origin; \
			if [ -n "$$ref" ]; then \
				echo "Checking out $$name ref: $$ref"; \
				git -C "$(PLUGINS_CACHE)/$$name" checkout "origin/$$ref" --detach 2>/dev/null \
					|| git -C "$(PLUGINS_CACHE)/$$name" checkout "$$ref" --detach; \
			else \
				git -C "$(PLUGINS_CACHE)/$$name" pull --ff-only origin HEAD; \
			fi \
		else \
			echo "Cloning $$name..."; \
			if [ -n "$$ref" ]; then \
				git clone --depth 1 --branch "$$ref" $$repo "$(PLUGINS_CACHE)/$$name"; \
			else \
				git clone --depth 1 $$repo "$(PLUGINS_CACHE)/$$name"; \
			fi; \
			. ./scripts/ci/track-event.sh && formae_track_event "ci_repo_clone" "cloned_repo=$$name"; \
		fi \
	done
```

This:
- Parses `@ref` suffix from each entry in `EXTERNAL_PLUGIN_REPOS`
- Strips the `@ref` to get the clean git URL
- Clones with `-b ref` if ref is present, otherwise clones default branch
- On update, fetches and checks out the ref (supports both branch names and tags)

- [ ] **Step 3: Verify the Makefile works with no refs**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
rm -rf .plugins
make fetch-external-plugins
ls .plugins/
```

Expected: All 7 plugin repos cloned successfully from their default branch.

- [ ] **Step 4: Verify the Makefile works with a ref**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
rm -rf .plugins/formae-plugin-aws
EXTERNAL_PLUGIN_REPOS="https://github.com/platform-engineering-labs/formae-plugin-aws.git@main" make fetch-external-plugins
git -C .plugins/formae-plugin-aws log --oneline -1
```

Expected: AWS plugin cloned at the specified ref.

- [ ] **Step 5: Commit**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
git add Makefile
git commit -m "refactor(make): generalize plugin ref pinning with url@ref syntax

Replace ad-hoc AZURE_PLUGIN_REF / AWS_PLUGIN_REF variables with a
url@ref convention in EXTERNAL_PLUGIN_REPOS. Append @branch or @tag
to any plugin URL to pin it. Works for all plugins, not just Azure
and AWS."
```

---

### Task 3: Update e2e workflow to use the new mechanism

The e2e workflow currently passes `AZURE_PLUGIN_REF` and `AWS_PLUGIN_REF` to the Makefile. Update it to use `EXTERNAL_PLUGIN_REPOS` with the `@ref` syntax instead.

**Files:**
- Modify: `.github/workflows/e2e-tests.yml:1-14, 156-164`

- [ ] **Step 1: Update workflow inputs**

Replace the current inputs (lines 5-13):

```yaml
on:
  workflow_dispatch:
    inputs:
      plugin_refs:
        description: >-
          Plugin ref overrides as comma-separated name=ref pairs
          (e.g., aws=feat/msgpack,azure=feat/msgpack). Leave empty for main.
        required: false
        default: ''
  pull_request:
    branches: [ "main" ]
```

- [ ] **Step 2: Add a step to build the EXTERNAL_PLUGIN_REPOS override**

In the `e2e` job, before the test step, add a step that constructs the `EXTERNAL_PLUGIN_REPOS` value from the inputs:

```yaml
      - name: Build plugin repo list
        id: plugins
        run: |
          # Default repos (no ref pinning)
          REPOS=(
            "https://github.com/platform-engineering-labs/formae-plugin-aws.git"
            "https://github.com/platform-engineering-labs/formae-plugin-azure.git"
            "https://github.com/platform-engineering-labs/formae-plugin-compose.git"
            "https://github.com/platform-engineering-labs/formae-plugin-gcp.git"
            "https://github.com/platform-engineering-labs/formae-plugin-grafana.git"
            "https://github.com/platform-engineering-labs/formae-plugin-oci.git"
            "https://github.com/platform-engineering-labs/formae-plugin-ovh.git"
          )

          # Apply ref overrides from input (e.g., "aws=feat/msgpack,azure=feat/msgpack")
          REFS="${{ github.event.inputs.plugin_refs }}"
          if [ -n "$REFS" ]; then
            IFS=',' read -ra PAIRS <<< "$REFS"
            for pair in "${PAIRS[@]}"; do
              name="${pair%%=*}"
              ref="${pair##*=}"
              for i in "${!REPOS[@]}"; do
                if [[ "${REPOS[$i]}" == *"formae-plugin-${name}.git"* ]]; then
                  REPOS[$i]="${REPOS[$i]}@${ref}"
                  echo "Pinning ${name} to ref: ${ref}"
                fi
              done
            done
          fi

          echo "EXTERNAL_PLUGIN_REPOS=${REPOS[*]}" >> "$GITHUB_ENV"
```

- [ ] **Step 3: Update the test step to pass the override**

Replace lines 160-164:

```yaml
      - name: Running ${{ matrix.test }}
        env:
          AWS_PROFILE: e2e-test
          AZURE_SUBSCRIPTION_ID: ${{ secrets.AZURE_SUBSCRIPTION_ID }}
        run: >-
          make test-e2e
          E2E_RUN_FLAGS="-run ${{ matrix.test }}"
          EXTERNAL_PLUGIN_REPOS="${EXTERNAL_PLUGIN_REPOS}"
```

- [ ] **Step 4: Commit**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
git add .github/workflows/e2e-tests.yml
git commit -m "refactor(ci): use url@ref for e2e plugin pinning

Replace per-plugin ref inputs with a single plugin_refs input that
accepts comma-separated name=ref pairs. Constructs EXTERNAL_PLUGIN_REPOS
with @ref suffixes, matching the new Makefile convention."
```

---

### Task 4: Final verification

- [ ] **Step 1: Run full test suite**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
make test-all
```

Expected: All tests pass.

- [ ] **Step 2: Run linter**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
make lint
```

Expected: 0 issues.

- [ ] **Step 3: Verify commit log**

```bash
cd /home/jeroen/dev/pel/formae-msgpack
git log --oneline feat/extensions-coordinator..HEAD
```

Expected: Commits for msgpack helpers, MarshalEDF, compression removal, dep promotion, sub-module tidy, Ergo upgrade, Makefile refactor, e2e workflow update.

---

### Task 5: Plugin repo coordination (manual, per plugin)

This task is executed in each plugin repo, not in the formae worktree. It's documented here for completeness.

**For each plugin repo** (aws, azure, compose, gcp, grafana, oci, ovh):

- [ ] **Step 1: Create a feature branch**

```bash
git checkout -b feat/msgpack-serialization
```

- [ ] **Step 2: Update `pkg/plugin` dependency**

Update `go.mod` to point at the formae branch (or the new release once available):

```bash
go get github.com/platform-engineering-labs/formae/pkg/plugin@feat/msgpack-serialization
go mod tidy
```

- [ ] **Step 3: Build and test**

```bash
make build
make test
```

- [ ] **Step 4: Trigger nightly with formae branch**

Via GitHub Actions UI or CLI, trigger the nightly workflow with `formae_branch=feat/msgpack-serialization`:

```bash
gh workflow run nightly.yml -f formae_branch=feat/msgpack-serialization
```

- [ ] **Step 5: Verify nightly passes**

Once green: the plugin is validated against the formae msgpack branch.

---

### Task 6: Merge sequence (manual coordination)

- [ ] **Step 1: Verify all plugin nightlies are green** against `feat/msgpack-serialization`
- [ ] **Step 2: Merge formae PR** (`feat/msgpack-serialization` → main)
- [ ] **Step 3: Cut a formae release**
- [ ] **Step 4: Update each plugin's `pkg/plugin` dependency** to point at the new release tag
- [ ] **Step 5: Merge each plugin's feature branch** to main
- [ ] **Step 6: Reset each plugin's nightly** `formae_branch` default back to `main` (already the default, no change needed unless it was hardcoded)
- [ ] **Step 7: Verify plugin CI and nightly are both green**
