#!/usr/bin/env python3
"""Rewrite `uses:` references in workflow/action files to full commit SHAs.

    uses: actions/checkout@v6            ->  uses: actions/checkout@<sha> # v6.0.1
    uses: actions/checkout@<PIN-SHA>     ->  latest release, pinned        # vX.Y.Z

Usage:
    GITHUB_TOKEN=... python pin_actions.py .github/workflows/*.yml [--dry-run]

Resolution uses the GitHub REST API (token optional but avoids rate limits; `gh auth token`
works). Major tags (v6) are resolved to the most specific release tag pointing at the same
commit so the trailing comment is precise. Never invents a SHA: unresolved refs are left
untouched and reported. Local (./) and docker:// references are skipped.
Alternative on github.com: `gh actions-lock` (workflow dependency lockfiles).
"""
import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.request

USES_RE = re.compile(
    r"^(?P<pre>\s*-?\s*uses:\s*)(?P<q>['\"]?)(?P<repo>[\w.-]+/[\w.-]+)(?P<sub>(/[\w./-]+)?)@(?P<ref>[^\s'\"#]+)(?P=q)(?P<post>\s*(#.*)?)$"
)
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
API = os.environ.get("GITHUB_API_URL", "https://api.github.com")


def api(path):
    req = urllib.request.Request(API + path, headers={"Accept": "application/vnd.github+json"})
    tok = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN")
    if tok:
        req.add_header("Authorization", f"Bearer {tok}")
    with urllib.request.urlopen(req, timeout=20) as r:
        return json.load(r)


class GitHubResolver:
    def latest_tag(self, repo):
        return api(f"/repos/{repo}/releases/latest")["tag_name"]

    def tag_sha(self, repo, tag):
        obj = api(f"/repos/{repo}/git/ref/tags/{tag}")["object"]
        if obj["type"] == "tag":  # annotated tag -> commit
            obj = api(f"/repos/{repo}/git/tags/{obj['sha']}")["object"]
        return obj["sha"]

    def branch_sha(self, repo, branch):
        return api(f"/repos/{repo}/commits/{branch}")["sha"]

    def precise_tag(self, repo, sha, fallback):
        """Most specific semver tag pointing at sha (e.g. v6 -> v6.0.1)."""
        try:
            tags = api(f"/repos/{repo}/tags?per_page=100")
        except Exception:  # noqa: BLE001
            return fallback
        cands = [t["name"] for t in tags if t["commit"]["sha"] == sha]
        cands.sort(key=lambda n: (n.count("."), len(n)), reverse=True)
        return cands[0] if cands else fallback


def resolve(resolver, repo, ref):
    if ref == "<PIN-SHA>":
        ref = resolver.latest_tag(repo)
    try:
        sha = resolver.tag_sha(repo, ref)
    except urllib.error.HTTPError:
        sha = resolver.branch_sha(repo, ref)  # branch refs: pin, but warn
        print(f"  warning: {repo}@{ref} is a branch; pinned to its current head", file=sys.stderr)
    return sha, resolver.precise_tag(repo, sha, ref)


def process(path, resolver, dry_run=False):
    out, changed, errors = [], 0, 0
    with open(path, encoding="utf-8") as fh:
        lines = fh.read().split("\n")
    for line in lines:
        m = USES_RE.match(line)
        if not m or SHA_RE.match(m["ref"]):
            out.append(line)
            continue
        repo, ref = m["repo"], m["ref"]
        try:
            sha, label = resolve(resolver, repo, ref)
        except Exception as exc:  # noqa: BLE001
            print(f"  ERROR {path}: could not resolve {repo}@{ref}: {exc}", file=sys.stderr)
            out.append(line)
            errors += 1
            continue
        new = f"{m['pre']}{repo}{m['sub']}@{sha} # {label}"
        print(f"  {repo}{m['sub']}@{ref} -> {sha} # {label}")
        out.append(new)
        changed += 1
    if changed and not dry_run:
        with open(path, "w", encoding="utf-8") as fh:
            fh.write("\n".join(out))
    return changed, errors


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("files", nargs="+")
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()
    total_err = 0
    for f in args.files:
        print(f"{f}:")
        _, err = process(f, GitHubResolver(), args.dry_run)
        total_err += err
    sys.exit(1 if total_err else 0)


if __name__ == "__main__":
    main()
