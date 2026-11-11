# Performance and structure reference

Contents
1. Concurrency and timeouts
2. Triggers and filters
3. Caching
4. Matrix strategy
5. Jobs, outputs and artifacts
6. Reusable workflows vs composite actions
7. Runner choice
8. Maintainability conventions
9. Snippets

IDs in brackets resolve in `sources.md`.

---

## 1. Concurrency and timeouts

```yaml
# CI: cancel superseded runs on the same PR/branch
concurrency:
  group: ${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}
  cancel-in-progress: true
```

```yaml
# Deploy/release: serialize, never cancel half-way
concurrency:
  group: deploy-production
  cancel-in-progress: false
```

- Use a conditional for mixed workflows: `cancel-in-progress: ${{ github.event_name == 'pull_request' }}`.
- Set `timeout-minutes` on every job (default is 360 min, which burns minutes on hangs) `[GH-CONCURRENCY] [PIPELINEFORGE]`.

## 2. Triggers and filters

- Avoid double runs: `push: branches: [main]` + `pull_request:` instead of bare `push:` on all branches.
- `paths` / `paths-ignore` to skip docs-only changes — but **required status checks on skipped workflows stay "pending"**; for required checks, run the workflow and skip jobs inside it (e.g. with a changes-detection job) instead `[GH-SYNTAX]`.
- `workflow_dispatch` with typed `inputs` (`type: choice|boolean|environment`) for manual runs; restrict who can dispatch with execution protections `[GH-EXEC-PROT]`.
- `merge_group` trigger if the repo uses a merge queue.
- `schedule` cron runs on the default branch only and is UTC; add `workflow_dispatch` alongside for testing.

## 3. Caching

- First choice: built-in caching of setup actions, e.g. `actions/setup-node` with `cache: npm`, `actions/setup-python` `cache: pip`, `actions/setup-go` (on by default), `actions/setup-java` `cache: maven|gradle`.
- Manual `actions/cache`: key on OS + lockfile hash, with ordered `restore-keys` fallbacks; caches are immutable—a new content needs a new key; key max 512 chars `[GH-CACHE-REF]`.
- Cache *dependency downloads* (e.g. `~/.npm`), not `node_modules`, unless measured faster.
- Use artifacts (not cache) to pass build outputs between jobs; cache only things you can regenerate `[GH-CACHE]`.
- Security: see `security.md` §9 — no secrets in caches, avoid cache restore in release jobs.

## 4. Matrix strategy

```yaml
strategy:
  fail-fast: false          # see all failing combos in PRs
  max-parallel: 4           # optional, protect shared resources
  matrix:
    os: [ubuntu-24.04, windows-2025, macos-15]
    node: [20, 22, 24]
    exclude:
      - os: windows-2025
        node: 20
```

- The **application** runtime version (`node-version: 20`) is independent from the **action** runtime (node24) — don't change test matrices just to silence node20 warnings `[CONFIGCRATE-NODE]`.
- Keep matrices small on PRs; run the full matrix on `main`/nightly using `include` + conditionals.

## 5. Jobs, outputs and artifacts

- Split lint / test / build so failures surface quickly; use `needs:` to fan in.
- Pass small values via `jobs.<id>.outputs` ← `steps.<id>.outputs` (written with `echo "name=value" >> "$GITHUB_OUTPUT"`).
- Pass files via `actions/upload-artifact` / `download-artifact`, set `retention-days`, and give unique names per matrix leg.
- Use `if: always()` / `if: failure()` only for reporting/cleanup steps.
- Job summaries: `echo "### Results" >> "$GITHUB_STEP_SUMMARY"`.

## 6. Reusable workflows vs composite actions

| | Reusable workflow (`on: workflow_call`) | Composite action (`runs.using: composite`) |
|---|---|---|
| Called at | `jobs.<id>.uses` | `steps[*].uses` |
| Unit | One or more **jobs**, own runners | **Steps** inside caller's job/runner |
| Secrets | Declared `secrets:` (or `inherit` for own org) | No `secrets` context — pass as inputs |
| Environments / OIDC policy | Yes; `job_workflow_ref` claim enables central trusted deploy | Inherits caller's job |
| Status checks | Appear as `caller / callee` | Invisible inside caller job |
| Nesting limits (github.com) | Up to 10 levels, 50 unique reusable workflows per file `[NERDLEVEL-REUSE]` | — |
| Lockfile coverage | Not covered by caller's `actions.lock`; callee repo needs own lockfile | Transitive deps covered `[GH-LOCKFILE]` |

Rule of thumb: needs its own job, runner, matrix, environment or secrets contract → reusable workflow; five repeated steps → composite action `[KASCHIMER-REUSE] [DEVOPSENG-REUSE]`. Central reusable build + attestations is the path to SLSA Build L3 `[GH-ATTEST]`.

Composite action rules: every `run:` step needs explicit `shell:`; route `inputs.*` through `env:` before using in shell (injection); declare `outputs` with `value: ${{ steps.x.outputs.y }}`.

## 7. Runner choice

- GitHub-hosted labels: `ubuntu-24.04`, `ubuntu-24.04-arm`, `ubuntu-slim` (lightweight), `windows-2025`, `windows-11-arm`, `macos-15`, `macos-26` and larger-runner variants; actionlint knows the current list `[ACTIONLINT-PLAYGROUND]`.
- `ubuntu-*-arm` runners are often cheaper/faster for ARM builds and multi-arch images (avoid QEMU emulation).
- Pin images (`ubuntu-24.04`) for release builds; `-latest` labels migrate silently.
- Larger runners / self-hosted only where measured; see `security.md` §10.

## 8. Maintainability conventions

- One concern per workflow file; file name = purpose (`ci.yml`, `release.yml`, `deploy.yml`).
- `defaults: run: shell: bash` (Windows defaults to pwsh otherwise).
- Use `env:` at workflow level only for non-secret constants.
- Lint workflows in CI with actionlint (syntax, expression types, shellcheck on `run:`) and zizmor (security) `[ACTIONLINT] [ZIZMOR]`.
- CODEOWNERS on `.github/` so workflow changes need platform/security review.

## 9. Snippets

Changes detection without third-party actions (keeps required checks green):

```yaml
changes:
  runs-on: ubuntu-24.04
  timeout-minutes: 5
  permissions:
    contents: read
    pull-requests: read
  outputs:
    src: ${{ steps.filter.outputs.src }}
  steps:
    - id: filter
      env:
        GH_TOKEN: ${{ github.token }}
        PR: ${{ github.event.pull_request.number }}
      run: |
        if [ -z "$PR" ]; then echo "src=true" >> "$GITHUB_OUTPUT"; exit 0; fi
        if gh pr diff "$PR" --repo "$GITHUB_REPOSITORY" --name-only | grep -qE '^(src/|package(-lock)?\.json$)'; then
          echo "src=true" >> "$GITHUB_OUTPUT"
        else
          echo "src=false" >> "$GITHUB_OUTPUT"
        fi
```

Safe multi-line output with random delimiter:

```yaml
- run: |
    delim="EOF_$(openssl rand -hex 8)"
    { echo "notes<<$delim"; cat notes.md; echo "$delim"; } >> "$GITHUB_OUTPUT"
```
