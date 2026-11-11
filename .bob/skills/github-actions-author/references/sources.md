# Sources (annotated bibliography)

Research snapshot: November 2026. Prefer primary sources (GitHub Docs / Changelog / Security Lab) when citing to users; secondary sources add incident context and practical patterns. Re-check dated items (deadlines, versions) before relying on them.

## A. GitHub Docs (primary)

| ID | Title | URL | Use it for |
|---|---|---|---|
| GH-SECURE-USE | Secure use reference | https://docs.github.com/en/actions/reference/security/secure-use | Least privilege, secrets handling, SHA pinning ("only way to use an action as an immutable release"), self-hosted runner guidance |
| GH-SECURE-YOUR-WORK | Security for GitHub Actions (index) | https://docs.github.com/en/actions/how-tos/secure-your-work | Entry point: lockfiles, attestations, OIDC |
| GH-SYNTAX | Workflow syntax for GitHub Actions | https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax | `on`, filters, `uses` ref forms, matrix, defaults |
| GH-PERMISSIONS | Workflow syntax – `permissions` | https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#permissions | Available scopes, job vs workflow level |
| GH-CONCURRENCY | Workflow syntax – `concurrency` / `timeout-minutes` | https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#concurrency | Groups, cancel-in-progress, job timeouts |
| GH-SCRIPT-INJ | Script injections | https://docs.github.com/en/actions/concepts/security/script-injections | Untrusted context list, env-var mitigation, branch-name injection example |
| GH-PRT | Securely using pull_request_target | https://docs.github.com/en/actions/reference/security/securely-using-pull_request_target | Why `pull_request_target` + head checkout is insecure |
| GH-EXEC-PROT | Governing execution of GitHub Actions workflows | https://docs.github.com/en/actions/how-tos/administer/control-workflow-execution | Actor/event rules, workflow targeting, evaluate mode |
| GH-LOCKFILE | Workflow dependency lockfiles (concept) | https://docs.github.com/en/actions/concepts/security/workflow-dependency-lockfiles | What `actions.lock` covers (transitive deps) and does not (reusable workflow calls, inputs, runner image) |
| GH-LOCKFILE-HOWTO | Locking action dependencies in a repository's workflows | https://docs.github.com/en/actions/how-tos/secure-your-work/lock-workflow-dependencies | `gh actions-lock` steps, local actions, onboarding order |
| GH-OIDC | OpenID Connect (concept) | https://docs.github.com/en/actions/concepts/security/openid-connect | Token claims, benefits, custom properties as claims |
| GH-OIDC-HOWTO | Security hardening your deployments | https://docs.github.com/en/actions/how-tos/secure-your-work/security-harden-deployments | Per-provider OIDC guides (AWS, Azure, GCP, Vault, JFrog, Docker, PyPI, Octopus) and OIDC with reusable workflows |
| GH-CACHE | Dependency caching (concept) | https://docs.github.com/en/actions/concepts/workflows-and-actions/dependency-caching | Cache vs artifacts, cache scope, poisoning, read-only caches for low-trust triggers |
| GH-CACHE-REF | Dependency caching reference | https://docs.github.com/en/actions/reference/workflows-and-actions/dependency-caching | Key matching, `restore-keys`, limits |
| GH-ATTEST | Artifact attestations (concept) | https://docs.github.com/en/actions/concepts/security/artifact-attestations | SLSA L2 vs L3 via reusable workflows, Sigstore |
| GH-ATTEST-HOWTO | Using artifact attestations | https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations | Provenance, SLSA L3, Kubernetes admission, offline verify |
| GH-DEPENDABOT-ACTIONS | Keeping your actions up to date with Dependabot | https://docs.github.com/en/code-security/how-tos/secure-your-supply-chain/secure-your-dependencies/auto-update-actions | `github-actions` ecosystem config |
| GH-DEPENDABOT-OPTS | Dependabot options reference | https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference | `groups`, `cooldown`, `applies-to` |

## B. GitHub Changelog / Blog (primary, dated)

| ID | Title | URL | Key fact |
|---|---|---|---|
| GH-SHA-POLICY | Actions policy now supports blocking and SHA pinning (15 Aug 2025) | https://github.blog/changelog/2025-08-15-github-actions-policy-now-supports-blocking-and-sha-pinning-actions/ | Enforce full-SHA pinning; `!` blocklist entries |
| GH-PRT-CHANGE | pull_request_target and environment branch protections changes (7 Nov 2025) | https://github.blog/changelog/2025-11-07-actions-pull_request_target-and-environment-branch-protections-changes/ | From 8 Dec 2025 workflow source/ref = default branch |
| GH-PRT-BLOCK | pull_request_target blocked by default December 2 (2 Nov 2026) | https://github.blog/changelog/2026-11-02-pull_request_target-blocked-by-default-december-2/ | Public repos blocked from 2 Dec 2026 unless event policy allows; brownouts 9 & 16 Nov |
| GH-EXEC-PROT-GA | Workflow execution protections GA (17 Sep 2026) | https://github.blog/changelog/2026-09-17-workflow-execution-protections-in-github-actions-generally-available/ | Workflow file targeting, insights, REST API |
| GH-EXEC-PROT-PREVIEW | Control who and what triggers workflows (18 Jun 2026) | https://github.blog/changelog/2026-06-18-control-who-and-what-triggers-github-actions-workflows/ | Actor/event rules rationale |
| GH-LOCKFILE-PREVIEW | Workflow lockfiles public preview (14 Oct 2026) | https://github.blog/changelog/2026-10-14-github-actions-workflow-lockfiles-in-public-preview/ | Dependabot regenerates lockfile entries |
| GH-LOCKFILE-GA | Workflow lockfiles generally available (28 Oct 2026) | https://github.blog/changelog/2026-10-28-github-actions-workflow-lockfiles-are-generally-available/ | GA on Free/Team/Enterprise Cloud; Require lockfile policy |
| GH-NODE20 | Deprecation of Node 20 on Actions runners / Node 20 no longer available (23 Sep 2026) | https://github.blog/changelog/2025-09-19-deprecation-of-node-20-on-github-actions-runners/ and https://github.blog/changelog/2026-09-23-node-20-is-no-longer-available-in-github-actions/ | Actions must use node24; no opt-out; macOS ≤13.4 and ARM32 self-hosted unsupported |
| GH-COMMUNITY-SECURE-DEFAULT | Towards a secure by default GitHub Actions (discussion) | https://github.com/orgs/community/discussions/179107 | GitHub's roadmap framing for pull_request_target restrictions |
| GH-BLOG-4TIPS | How to secure GitHub Actions workflows: 4 tips | https://github.blog/security/supply-chain-security/four-tips-to-keep-your-github-actions-workflows-secure/ | Untrusted input + permission tightening summary |
| GH-BLOG-DEPENDABOT | Tame Dependabot: group, slow cadence, keep security fast (Jul 2026) | https://github.blog/security/supply-chain-security/tame-dependabot-group-your-updates-slow-the-cadence-keep-security-fast/ | Grouping + monthly cadence pattern |

## C. GitHub Security Lab (primary research)

| ID | Title | URL |
|---|---|---|
| GHSL-1 | Keeping your GitHub Actions and workflows secure Part 1: Preventing pwn requests | https://securitylab.github.com/resources/github-actions-preventing-pwn-requests/ |
| GHSL-2 | Part 2: Untrusted input | https://securitylab.github.com/resources/github-actions-untrusted-input/ |
| GHSL-3 | Part 3: How to trust your building blocks | https://securitylab.github.com/resources/github-actions-building-blocks/ |
| GHSL-4 | Part 4: New vulnerability patterns and mitigation strategies (Jan 2025) | https://securitylab.github.com/resources/github-actions-new-patterns-and-mitigations/ |
| GHSL-CODEQL | How to scan GitHub Actions workflows for security issues (CodeQL for Actions) | https://github.blog/security/application-security/how-to-secure-your-github-actions-workflows-with-codeql/ |

## D. Tools and standards

| ID | Title | URL | Notes |
|---|---|---|---|
| ZIZMOR | zizmor docs | https://docs.zizmor.sh/ | Static security analysis for workflows, Dependabot, pre-commit; `--fix`, personas, SARIF |
| ZIZMOR-AUDITS | zizmor audit rules | https://docs.zizmor.sh/audits/ | template-injection, excessive-permissions, unpinned-uses, impostor-commit, cache-poisoning, bot-conditions, secrets-inherit … |
| ZIZMOR-ARTIPACKED | zizmor `artipacked` audit | https://docs.zizmor.sh/audits/#artipacked | Credential persistence via checkout |
| ZIZMOR-ACTION | zizmorcore/zizmor-action | https://github.com/zizmorcore/zizmor-action | Run in CI; Advanced Security mode vs failing mode |
| ACTIONLINT | rhysd/actionlint | https://github.com/rhysd/actionlint | Syntax, expression typing, shellcheck/pyflakes on `run:`, runner labels, cron |
| ACTIONLINT-PLAYGROUND | actionlint playground | https://rhysd.github.io/actionlint/ | Current runner label list |
| SCORECARD | OpenSSF Scorecard checks | https://github.com/ossf/scorecard/blob/main/docs/checks.md | Dangerous-Workflow, Token-Permissions, Pinned-Dependencies scoring |
| GH-ATTEST-ACTION | actions/attest-build-provenance (→ actions/attest) | https://github.com/actions/attest-build-provenance | New implementations should use `actions/attest`; plan availability |
| SLSA-GEN | slsa-framework/slsa-github-generator | https://github.com/slsa-framework/slsa-github-generator | Maintenance notice; points to artifact attestations |
| ACTIONS-LOCKFILE-FORMAT | github/actions-lockfile | https://github.com/github/actions-lockfile | Lockfile schema + Go parser for audits |
| SHA-PIN-ENFORCER | zgosalvez/github-actions-ensure-sha-pinned-actions | https://github.com/zgosalvez/github-actions-ensure-sha-pinned-actions | CI check for unpinned actions (where org policy unavailable) |

## E. Secondary sources (incident context, practical patterns)

| ID | Title | URL | Why |
|---|---|---|---|
| STINGRAI-2026 | GitHub Actions Security Checklist 2026: 25 Hardening Steps | https://www.stingrai.io/blog/github-actions-security-checklist | Incident timeline (tj-actions, reviewdog, Nx, Shai-Hulud, 2026 campaigns) mapped to controls |
| SAFEGUARD-HARDEN | GitHub Actions Security Hardening: A Practical Checklist (Jul 2026) | https://safeguard.sh/resources/blog/github-actions-security-hardening | Copy-paste YAML for pinning, permissions, OIDC |
| SAFEGUARD-SUPPLY | GitHub Actions Supply Chain Hardening Checklist 2026 | https://safeguard.sh/resources/blog/github-actions-supply-chain-hardening-checklist-2026 | OIDC trust scoping, environment protection, runner isolation |
| PIPELINEFORGE | GitHub Actions Hardening Checklist for 2026 | https://pipelineforge.dev/blog/github-actions-hardening-checklist-2026 | Timeouts, concurrency, secret scoping |
| DSOTN-PIN | Pin GitHub Actions to SHAs | https://dsotn.com/articles/pin-github-actions-sha/ | Pinning rationale with primary citations |
| ROMAIN-PIN | Pinning GitHub Actions to commit SHAs (maintainer view) | https://www.romainlespinasse.dev/posts/github-actions-commit-sha-pinning/ | Sub-action pinning catch for action authors |
| STARSLING-PIN | Pin GitHub Actions to commit SHAs (starsling) | https://starsling.dev/best-practices/github-actions/pin-action-shas | node24 runner minimum, real-world pinned repos |
| CONFIGCRATE-NODE | Fix "Node.js 20 actions are deprecated" | https://configcrate.com/fix-github-actions-node-20-deprecated.html | Action runtime vs app runtime distinction |
| KASCHIMER-REUSE | Reusable workflows vs composite actions — know the difference | https://steve-kaschimer.github.io/posts/2026-03-13-github-actions-reusable-workflows-vs-composite-actions/ | Failure scenarios (secrets, status checks) |
| NERDLEVEL-REUSE | Reusable workflow vs composite action 2026 | https://nerdleveltech.com/github-actions-reusable-workflow-vs-composite-action | Nesting limits, secrets, outputs |
| DEVOPSENG-REUSE | Reusable workflows & composite actions (2026) | https://www.devopsenginer.com/blog/github-actions-reusable-workflows-guide-2026 | Decision rule of thumb |
| LFDT-DEPENDABOT | Dependabot configuration example (LF Decentralized Trust) | https://lf-decentralized-trust.github.io/governance/guidelines/dependabot-example/ | Grouping + 30-day cooldown rationale |
| ZIZMOR-TUTORIAL | Auditing your workflows with zizmor (Stéphane Robert) | https://www.stephane-robert.info/en/docs/github-actions/security/zizmor/ | Walkthrough of audits, `--fix`, CI wiring |
| INFOQ-LOCKFILE | GitHub Actions Lockfiles Reach GA (InfoQ, Nov 2026) | https://www.infoq.com/news/2026/11/github-actions-lockfiles-ga/ | Independent summary of lockfile limits |

## F. Internal (IBM)

| ID | Title | Notes |
|---|---|---|
| IBM-GH-REVIEW | HashiCorp GitHub Security Controls Review (executive summary + plan) | Internal posture data: allowed-actions, default workflow permissions, unpinned actions; plan to flip default token to read via Terraform `github_actions_organization_permissions`. Use only as illustrative internal context. |
