# GitHub Actions security reference

Contents
1. Threat model in one paragraph
2. Trigger trust levels
3. Expression / script injection
4. `pull_request_target` and `workflow_run`
5. Token permissions
6. Pinning, lockfiles and third-party actions
7. Secrets
8. OIDC to clouds and registries
9. Cache and artifact poisoning
10. Runners
11. Build provenance (attestations, SLSA)
12. Org-level guardrails
13. Review checklist

IDs in brackets resolve in `sources.md`.

---

## 1. Threat model

A workflow is a remote-code-execution engine you invite into your repo and hand your secrets to `[SAFEGUARD-HARDEN]`. Blast radius = (what the token/identities can do) × (what code is trusted) × (what the runner can reach). Real incidents: tj-actions/changed-files tags repointed to a memory-dumping commit, ~23k repos exposed (CVE-2025-30066, Mar 2025); reviewdog/action-setup upstream compromise (CVE-2025-30154); Nx "s1ngularity" used a `pull_request_target` injection to mint an npm publish token (Aug 2025); Shai-Hulud npm worm registered victims as self-hosted runners (Sep/Nov 2025) `[STINGRAI-2026]`. CodeQL and zizmor now detect most of these patterns statically `[GHSL-CODEQL] [ZIZMOR]`.

## 2. Trigger trust levels

| Trigger | Who can fire it | Workflow file comes from | Secrets / write token | Use for |
|---|---|---|---|---|
| `push`, `release`, `schedule`, `workflow_dispatch` | Writers (dispatch: anyone with write unless restricted) | The ref | Yes | Trusted CI, release, deploy |
| `pull_request` (fork) | Anyone | PR merge commit | **No** (read-only token, no secrets) | Build/test untrusted code |
| `pull_request_target` | Anyone | Default branch (since 8 Dec 2025) | **Yes** | Labeling/commenting only, never building PR code |
| `workflow_run` | Follows triggering workflow | Default branch | **Yes** | Privileged follow-up of a `pull_request` run; treat downloaded artifacts as untrusted |
| `issue_comment`, `issues`, `discussion*` | Anyone (public repos) | Default branch | **Yes** | ChatOps; check `author_association` and inject-safe handling |

`[GH-PRT] [GH-PRT-CHANGE] [GHSL-1] [GHSL-4]`

## 3. Expression / script injection

`${{ }}` is expanded **before** the shell runs, so attacker text becomes code `[GH-SCRIPT-INJ]`. Untrusted contexts include `github.event.issue.title|body`, `pull_request.title|body|head.ref|head.label`, `comment.body`, `review.body`, `commits.*.message|author.*`, `head_commit.*`, `pages.*.page_name`, `github.head_ref` `[GHSL-2]`. Branch names like `zzz";echo${IFS}"hello";#` are valid git refs `[GH-SCRIPT-INJ]`.

```yaml
# BAD
- run: echo "Title: ${{ github.event.pull_request.title }}"
# GOOD
- env:
    PR_TITLE: ${{ github.event.pull_request.title }}
  run: echo "Title: $PR_TITLE"
```

Same rule for `actions/github-script` (`script:` is JavaScript—read `process.env.PR_TITLE`), for inputs of composite actions that end up in `run:`, and for `GITHUB_ENV`/`GITHUB_OUTPUT` writes (an attacker value containing newlines can define new env vars; use a random heredoc delimiter if you must write untrusted multi-line values) `[GHSL-4]`. Safe-ish values (still prefer `env:`): numeric IDs, SHAs, `github.repository`.

Bot-checks: `if: github.actor == 'dependabot[bot]'` is spoofable in some flows; prefer `github.event.pull_request.user.login` and avoid granting elevated permissions on that check alone `[ZIZMOR-AUDITS]`.

## 4. `pull_request_target` and `workflow_run`

- Insecure pattern: `pull_request_target` + `actions/checkout` with `ref: ${{ github.event.pull_request.head.sha }}` + any step that runs repo code (`npm install`, `make`, tests, build scripts, even linters with plugins) `[GH-PRT]`.
- Label gates are vulnerable to TOCTOU: the PR can be updated after approval; if gating, pin to the exact approved SHA `[GHSL-4]`.
- Since 8 Dec 2025 the workflow and default checkout always come from the default branch; `GITHUB_REF` = default branch `[GH-PRT-CHANGE]`.
- From 2 Dec 2026 `pull_request_target` is **blocked by default in public repos** unless an Actions event policy allows it; brownouts on 9 and 16 Nov 2026 `[GH-PRT-BLOCK]`.
- Safe split: `pull_request` workflow builds and uploads results as artifact (no secrets) → `workflow_run` workflow downloads artifact, treats it as **data** (validate, never execute), and comments with a scoped token `[GHSL-1]`.
- Use `assets/templates/pr-comment-workflow-run.yml` for commenter/labeler needs.

## 5. Token permissions

- Set workflow-level `permissions: {}` or `contents: read`; elevate per job `[GH-SECURE-USE]`. Scorecard gives full marks only when the top level is read-only and writes are job-level `[SCORECARD]`.
- Common grants: `contents: write` (releases/tags/push), `pull-requests: write` (comment), `issues: write`, `packages: write` (GHCR), `id-token: write` (OIDC / attestations), `attestations: write`, `security-events: write` (SARIF upload), `checks: write`, `deployments: write`.
- Any permission not listed becomes `none` once a `permissions:` block exists.
- Org default for new repos is read since 2023, but many older repos/orgs remain write — set it explicitly in settings `[SAFEGUARD-HARDEN]`. (An IBM-internal review found >99% of scoped repos granting write by default — common in practice `[IBM-GH-REVIEW]`.)
- Prefer `GITHUB_TOKEN` over PATs; when cross-repo access is needed, use a GitHub App installation token minted per job with the narrowest repos/permissions, not a PAT.

## 6. Pinning, lockfiles and third-party actions

- Tags/branches are mutable; a full commit SHA is "currently the only way to use an action as an immutable release" `[GH-SECURE-USE]`. Format: `owner/repo@<40-hex> # vX.Y.Z` so Dependabot/Renovate can update it `[DSOTN-PIN]`.
- Pin reusable workflows (`jobs.<id>.uses`) and Docker images (`docker://image@sha256:...`) too.
- Org/enterprise policy can **enforce** SHA pinning and block specific actions with `!owner/action@ref` (Aug 2025) `[GH-SHA-POLICY]`.
- Workflow dependency lockfiles (GA 28 Oct 2026, github.com): `gh extension install github/gh-actions-lock && gh actions-lock` writes `.github/workflows/actions.lock`; runner executes locked commits even if tags move, covers transitive deps of composite actions, Dependabot updates the lockfile in the same PR, `Require lockfile` policy enforces it. It does **not** cover reusable-workflow calls (pin those by SHA) and does not make a dependency safe—review before locking `[GH-LOCKFILE] [GH-LOCKFILE-GA] [INFOQ-LOCKFILE]`.
- Impostor commits: a SHA from a **fork** is reachable via the parent repo's URL. Verify pins resolve to a commit on the action's own branch/tag (zizmor `impostor-commit`; lockfiles reject unreachable commits) `[ZIZMOR-AUDITS] [GH-LOCKFILE]`.
- Choose actions by: verified creator or org you trust, active maintenance, small dependency tree, Scorecard results, reading `action.yml` at the pinned SHA. Prefer `run:` with an official CLI over a wrapper action `[GHSL-3]`.
- Keep pins fresh with Dependabot `github-actions` ecosystem plus a cooldown (version updates only; security updates are not delayed) `[GH-DEPENDABOT-ACTIONS] [GH-DEPENDABOT-OPTS] [GH-BLOG-DEPENDABOT]`.

## 7. Secrets

- Anyone with write access can read all repo secrets via a workflow → keep prod secrets in **environments** with required reviewers `[GH-SECURE-USE]`.
- Expose at step level, not job/workflow level. Don't pass via CLI args (visible in process lists); use env or stdin.
- Redaction is best-effort: never store structured data (JSON/YAML) as one secret; register derived secrets with `::add-mask::`; if a secret hits logs, delete the log and rotate `[GH-SECURE-USE]`.
- `secrets: inherit` only to reusable workflows you own; otherwise pass named secrets `[ZIZMOR-AUDITS]`.
- Don't hardcode container registry credentials in `container:`/`services:` blocks.

## 8. OIDC

- Job needs `permissions: id-token: write` (+ `contents: read` if it checks out) `[GH-OIDC]`.
- Restrict the trust policy `sub` claim to `repo:ORG/REPO:environment:prod` (or a specific ref) — never `repo:ORG/*` wildcards for prod. Customizable claims and repository custom properties can tighten further `[GH-OIDC]`.
- Supported guides: AWS, Azure, GCP, HashiCorp Vault, JFrog, Docker, Octopus, PyPI (trusted publishing) `[GH-OIDC-HOWTO]`.
- Reusable workflows + OIDC: the `job_workflow_ref` claim lets the cloud trust only a central, reviewed deploy workflow `[GH-OIDC-HOWTO]`.

## 9. Cache and artifact poisoning

- Caches are scoped by branch/tag, not by workflow identity; restored contents are untrusted input. Never cache secrets `[GH-CACHE]`.
- GitHub gives low-trust triggers read-only access to default-branch caches; explicitly setting a write-capable `cache-mode` re-introduces poisoning risk `[GH-CACHE]`.
- Don't restore caches in release/publish jobs if avoidable (build clean); zizmor `cache-poisoning` flags this `[ZIZMOR-AUDITS]`.
- Artifacts from `pull_request` runs consumed by `workflow_run` are attacker-controlled: extract into a temp dir, validate, never execute (`artipacked`/`artifact poisoning`) `[GHSL-4]`.

## 10. Runners

- Never use self-hosted runners for public repos; untrusted PRs could persist on the host `[GH-SECURE-USE]`.
- Prefer ephemeral/JIT self-hosted runners (one job per VM/container), isolated network, runner groups restricted to specific repos.
- Self-hosted runners must be current (node24 actions need runner ≥ 2.327.1; macOS ≤ 13.4 and ARM32 unsupported) `[GH-NODE20] [STARSLING-PIN]`.
- Consider egress control (e.g. allowlisted endpoints) on build jobs that hold publish credentials `[STINGRAI-2026]`.

## 11. Build provenance

- `actions/attest` (successor to `attest-build-provenance`) creates Sigstore-signed SLSA provenance; needs `id-token: write` and `attestations: write`. Alone ≈ SLSA Build L2; build inside a shared reusable workflow for L3. Consumers verify with `gh attestation verify` `[GH-ATTEST] [GH-ATTEST-ACTION]`.
- Private-repo attestations require GitHub Enterprise Cloud `[GH-ATTEST-ACTION]`.
- `slsa-github-generator` is in maintenance mode and points to GitHub artifact attestations `[SLSA-GEN]`.

## 12. Org-level guardrails

- Workflow execution protections (GA 17 Sep 2026): actor and event rules evaluated before a run, workflow-file targeting, evaluate mode, REST API for policy-as-code `[GH-EXEC-PROT] [GH-EXEC-PROT-GA]`.
- Allowed-actions allowlist + SHA pinning enforcement + blocklist `[GH-SHA-POLICY]`.
- Enable CodeQL `actions` language and/or zizmor SARIF upload in code scanning `[GHSL-CODEQL] [ZIZMOR-ACTION]`.
- OpenSSF Scorecard: Dangerous-Workflow (critical), Token-Permissions, Pinned-Dependencies checks `[SCORECARD]`.

## 13. Review checklist

- [ ] Top-level `permissions` read-only/empty; job-level writes minimal and justified
- [ ] Every remote `uses:` pinned to full SHA with version comment (or covered by `actions.lock`); reusable workflows SHA-pinned
- [ ] No `${{ }}` with untrusted data in `run:` / `script:` / composite `run:` steps
- [ ] No `pull_request_target`/`workflow_run`/`issue_comment` job that checks out or executes PR code
- [ ] `persist-credentials: false` on checkout unless push is required
- [ ] Secrets only at step level; prod secrets in environments; no `secrets: inherit` to third parties
- [ ] Cloud auth via OIDC with tight `sub` condition
- [ ] Deploy jobs use `environment:`; deploy concurrency does not cancel in progress
- [ ] `timeout-minutes` on all jobs
- [ ] No caches restored in publish/release jobs (or justified)
- [ ] No self-hosted runners on public repos
- [ ] All JS actions on node24-compatible versions
- [ ] Dependabot (`github-actions`) configured; zizmor/actionlint in CI
