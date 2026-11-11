---
name: github-actions-author
description: write, review, refactor, and harden github actions workflows (.github/workflows/*.yml), composite actions (action.yml), reusable workflows, and dependabot config for actions, using researched security and performance best practices with cited references. use when the user asks to create a ci/cd pipeline on github, write or fix a github actions workflow, add a release/deploy/publish job, set up oidc to a cloud, review a workflow for security issues, migrate from another ci to github actions, or convert duplicated yaml into reusable workflows or composite actions.
---

# GitHub Actions Author

Produce workflows that are **secure by default, fast, and easy to maintain**, and explain each non-obvious choice with a reference from `references/sources.md`.

Treat every workflow as privileged code: it runs third-party code with access to the repo token, secrets and cloud identities. Major supply-chain incidents (tj-actions/changed-files CVE-2025-30066, Nx "s1ngularity", Shai-Hulud) exploited exactly the defaults this skill overrides. See `references/security.md`.

## Workflow

1. **Clarify only what changes the YAML** (skip if already given): trigger events; language/toolchain and versions; deploy target and whether it supports OIDC; github.com vs GitHub Enterprise Server (GHES lacks some features: lockfiles, execution protections); public vs private repo; whether fork PRs must be supported.
2. **Pick a starting template** from `assets/templates/` (table below) and adapt. Do not write from a blank page.
3. **Apply the non-negotiable rules** below. For anything involving secrets, fork PRs, `pull_request_target`, `workflow_run`, issue/comment triggers, caches, or self-hosted runners, read `references/security.md` first.
4. **Apply structure and speed patterns** from `references/performance-and-structure.md` (caching, concurrency, matrix, path filters, reusable vs composite).
5. **Resolve action pins.** Templates use `owner/action@<PIN-SHA>`. Run `python scripts/pin_actions.py <files>` (needs network; `GITHUB_TOKEN` optional) to rewrite every ref to `@<sha> # vX.Y.Z` (latest release for placeholders, exact tag otherwise). Without network, leave `<PIN-SHA>` and tell the user to run the script or `gh actions-lock`. Never invent or recall a SHA from memory.
6. **Validate.** Run `python scripts/check_workflow.py <files>` (offline, built in). Recommend `actionlint` and `zizmor` for full coverage; `assets/templates/workflow-lint.yml` runs both in CI.
7. **Deliver**: the YAML file(s), then a short "Why" table mapping each hardening/perf choice to a reference ID (e.g. `[GH-SECURE-USE]`), then any follow-ups the user must do in repo/org settings (see "Settings outside YAML").

## Template picker

| Need | Template |
|---|---|
| PR + push CI for a library/app | `ci.yml` |
| Build, attest provenance, publish a GitHub Release on tag | `release-attested.yml` |
| Deploy to a cloud with OIDC + environment approval | `deploy-oidc.yml` |
| Shared pipeline called from many repos | `reusable-build.yml` (callee) |
| Repeated step sequence inside jobs | `composite-action.yml` (save as `.github/actions/<name>/action.yml`) |
| Comment/label on fork PRs with a write token, without `pull_request_target` | `pr-comment-workflow-run.yml` |
| Lint/security-scan the workflows themselves | `workflow-lint.yml` |
| Keep actions updated | `dependabot.yml` (save as `.github/dependabot.yml`) |

## Non-negotiable rules

Apply all of these unless the user explicitly overrides one; if they do, add a YAML comment explaining the accepted risk.

1. **Least-privilege token.** Top-level `permissions: {}` (or `contents: read`), then grant per job only what that job uses. Never `write-all`. `[GH-SECURE-USE] [GH-PERMISSIONS] [SCORECARD]`
2. **Immutable action refs.** Pin every non-local `uses:` (actions *and* reusable workflows) to a 40-char commit SHA with a version comment: `uses: actions/checkout@<sha> # v6.0.0`. On github.com, also suggest workflow lockfiles (`gh actions-lock`), which additionally lock transitive deps of composite actions but **not** reusable workflow calls. `[GH-SECURE-USE] [GH-LOCKFILE] [GH-SHA-POLICY]`
3. **No expression injection.** Never put `${{ ... }}` with attacker-controllable data directly in `run:`, `script:` (actions/github-script) or shell inputs. Bind to `env:` and reference `"$VAR"`. Attacker-controllable includes anything ending in `title`, `body`, `message`, `name`, `email`, `head_ref`, `ref`, `label`, `page_name`, `default_branch`, plus `github.head_ref` and step outputs derived from them. When in doubt, route through `env:`. `[GH-SCRIPT-INJ] [GHSL-2]`
4. **Don't persist credentials.** `actions/checkout` with `persist-credentials: false` unless a later step must `git push`; then scope that job's permissions narrowly. `[ZIZMOR-ARTIPACKED]`
5. **Untrusted code never meets secrets.** Use `pull_request` (not `pull_request_target`) for building/testing PR code. If `pull_request_target`/`workflow_run` is unavoidable, never check out or execute PR head code in that job; use the `pull_request` → `workflow_run` split in `pr-comment-workflow-run.yml`. Public repos block `pull_request_target` by default from 2 Dec 2026 unless an event policy allows it. `[GH-PRT] [GHSL-1] [GHSL-4] [GH-PRT-BLOCK]`
6. **OIDC over long-lived cloud secrets.** `permissions: id-token: write` on the deploy job only, cloud trust scoped to repo + environment/branch. `[GH-OIDC]`
7. **Gate deployments with `environment:`** so environment protection rules (reviewers, branch restrictions) and environment-scoped secrets apply. `[GH-SECURE-USE]`
8. **Bound every job:** `timeout-minutes` on every job; `concurrency` group at workflow level (cancel-in-progress for CI, **not** for deploys/releases). `[GH-CONCURRENCY]`
9. **Secrets hygiene:** reference secrets only in the step that needs them (step-level `env:`); never `secrets: inherit` to third-party reusable workflows; never put structured JSON/YAML blobs in one secret; never cache secrets. `[GH-SECURE-USE] [GH-CACHE]`
10. **Current runtimes.** JavaScript actions must run on `node24` (Node 20 removed from runners on 23 Sep 2026). Prefer current major versions of `actions/*`; check each action's latest release instead of reusing versions from memory. `[GH-NODE20]`
11. **Explicit runner images** for reproducibility (`ubuntu-24.04` over `ubuntu-latest`) when builds are sensitive to toolchain changes; `ubuntu-latest` is acceptable for lint-only jobs.
12. **`shell: bash` defaults** via `defaults.run.shell: bash` so `set -eo pipefail` semantics apply on all OSes.

## Writing style for the YAML

- Name every workflow, job and non-trivial step (`name:`), so logs and required-status-check names are stable.
- Order keys: `name`, `on`, `permissions`, `concurrency`, `env`, `defaults`, `jobs`.
- Prefer small, single-purpose jobs connected with `needs:` and job `outputs:` over one giant job.
- Prefer built-in tool caching (`setup-node`/`setup-python`/`setup-go`/`setup-java` `cache:` input) over hand-rolled `actions/cache`.
- Prefer official CLIs in `run:` (e.g. `gh release create`) over little-known marketplace actions; every third-party action is code you trust with your token.
- Quote YAML values that look like numbers/booleans (`'3.10'`, `'on'`).
- Keep comments brief and only for non-obvious security decisions.

## Settings outside YAML (always list the relevant ones in the deliverable)

- Org/repo: default `GITHUB_TOKEN` permission = read; "Require actions to be pinned to a full-length commit SHA"; allowed-actions allowlist (and `!owner/action@ref` blocklist). `[GH-SHA-POLICY]`
- "Require lockfile" policy if using `actions.lock`. `[GH-LOCKFILE]`
- Workflow execution protections: actor/event rules, e.g. restrict `workflow_dispatch` on deploy workflows to a team; run in evaluate mode first. `[GH-EXEC-PROT]`
- Environments with required reviewers and deployment-branch rules; secrets moved into environments.
- Branch rulesets requiring the CI checks; CODEOWNERS on `.github/workflows/` and `.github/actions/`.
- Fork PR approval policy for public repos; Dependabot for `github-actions` with cooldown.

## Review mode

When asked to review/harden an existing workflow: run `scripts/check_workflow.py`, then walk `references/security.md` → "Review checklist", and return findings as a table `Severity | Location | Issue | Fix | Ref`, followed by the corrected YAML. Rank: injection & `pull_request_target`/`workflow_run` misuse > secret exposure > token permissions > unpinned refs > missing timeouts/concurrency > style.

## References

- `references/security.md` — threat model, trigger trust levels, injection, secrets, OIDC, caches, runners, attestations, review checklist.
- `references/performance-and-structure.md` — caching, concurrency, matrix, filters, artifacts, reusable workflows vs composite actions, runner choice.
- `references/sources.md` — annotated bibliography; every `[ID]` used above resolves here. Cite IDs in explanations to the user, with URLs.
