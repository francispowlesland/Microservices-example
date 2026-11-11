#!/usr/bin/env python3
"""Offline best-practice checker for GitHub Actions workflows and composite actions.

Usage:
    python check_workflow.py .github/workflows/*.yml .github/actions/*/action.yml
    python check_workflow.py --json path/to/file.yml

Not a replacement for actionlint (syntax/types) or zizmor (deep security audits);
it catches the high-value rules from SKILL.md quickly and without network.
Exit code: 1 if any HIGH finding, else 0.
"""
import argparse
import json
import re
import sys

try:
    import yaml
except ImportError:  # pragma: no cover
    sys.exit("PyYAML is required: pip install pyyaml")

SHA_RE = re.compile(r"^[0-9a-f]{40}$")
EXPR_RE = re.compile(r"\$\{\{(.*?)\}\}", re.S)
# Context paths an external user can influence (GitHub Docs script-injections, GHSL part 2)
UNTRUSTED_RE = re.compile(
    r"github\.event\.(issue|pull_request|comment|review|review_comment|discussion|pages|commits|head_commit|workflow_run)"
    r"[\w.\[\]*]*\.(title|body|message|name|email|head_ref|ref|label|page_name|default_branch|head_branch|display_title)\b"
    r"|github\.head_ref\b"
    r"|github\.event\.pull_request\.head\.(ref|label|repo\.default_branch)\b",
    re.I,
)
INPUTS_RE = re.compile(r"\binputs\.[\w-]+")
SEV_ORDER = {"HIGH": 0, "MEDIUM": 1, "LOW": 2, "INFO": 3}


class Checker:
    def __init__(self, path):
        self.path = path
        self.findings = []

    def add(self, sev, where, msg, ref):
        self.findings.append({"file": self.path, "severity": sev, "where": where, "issue": msg, "ref": ref})

    # ---------- shared step checks ----------
    def check_uses(self, uses, where):
        if not isinstance(uses, str):
            return
        if uses.startswith("./") or uses.startswith("$/"):
            return
        if uses.startswith("docker://"):
            if "@sha256:" not in uses:
                self.add("MEDIUM", where, f"docker image not pinned by digest: {uses}", "GH-SECURE-USE")
            return
        if "@" not in uses:
            self.add("MEDIUM", where, f"action reference without version: {uses}", "GH-SECURE-USE")
            return
        ref = uses.rsplit("@", 1)[1]
        if ref == "<PIN-SHA>":
            self.add("INFO", where, f"unresolved pin placeholder: {uses} (run scripts/pin_actions.py)", "GH-SECURE-USE")
        elif not SHA_RE.match(ref):
            self.add("MEDIUM", where, f"mutable ref '{ref}' in {uses}; pin to full commit SHA (or use actions.lock)",
                     "GH-SECURE-USE, GH-LOCKFILE")

    def check_injection(self, text, where, composite=False):
        if not isinstance(text, str):
            return
        for expr in EXPR_RE.findall(text):
            if UNTRUSTED_RE.search(expr):
                self.add("HIGH", where, f"untrusted expression inlined into script: ${{{{{expr.strip()}}}}}; move to env:",
                         "GH-SCRIPT-INJ, GHSL-2")
            elif composite and INPUTS_RE.search(expr):
                self.add("MEDIUM", where, f"action input inlined into script: ${{{{{expr.strip()}}}}}; route via env:",
                         "GH-SCRIPT-INJ")
            elif re.search(r"steps\.[\w-]+\.outputs|needs\.[\w-]+\.outputs", expr):
                self.add("LOW", where, f"step/job output inlined into script: ${{{{{expr.strip()}}}}}; prefer env: if it can carry user data",
                         "GH-SCRIPT-INJ")

    def check_step(self, step, where, triggers, composite=False):
        if not isinstance(step, dict):
            return
        uses = step.get("uses")
        self.check_uses(uses, where)
        self.check_injection(step.get("run"), where, composite)
        with_ = step.get("with") or {}
        if isinstance(uses, str) and uses.startswith("actions/github-script"):
            self.check_injection(with_.get("script"), where + " (github-script)", composite)
        if composite and "run" in step and "shell" not in step:
            self.add("HIGH", where, "composite action run step missing required 'shell:'", "GH-SYNTAX")
        if isinstance(uses, str) and uses.startswith("actions/checkout"):
            if str(with_.get("persist-credentials", "true")).lower() != "false":
                self.add("LOW", where, "checkout persists credentials; set persist-credentials: false unless pushing",
                         "ZIZMOR-ARTIPACKED")
            ref = str(with_.get("ref", ""))
            if triggers & {"pull_request_target", "workflow_run"} and re.search(r"head|pull_request|workflow_run", ref):
                self.add("HIGH", where, f"privileged trigger checks out PR/untrusted ref ({ref}) — pwn request risk",
                         "GH-PRT, GHSL-1")

    # ---------- workflow ----------
    def check_workflow(self, doc):
        on = doc.get("on", doc.get(True))
        if isinstance(on, str):
            triggers = {on}
        elif isinstance(on, list):
            triggers = set(on)
        elif isinstance(on, dict):
            triggers = set(on.keys())
        else:
            triggers = set()

        perms = doc.get("permissions", None)
        if "permissions" not in doc:
            self.add("HIGH", "workflow", "no top-level permissions; set `permissions: {}` or `contents: read`",
                     "GH-SECURE-USE, SCORECARD")
        elif perms == "write-all":
            self.add("HIGH", "workflow", "permissions: write-all", "GH-SECURE-USE")
        elif isinstance(perms, dict) and any(v == "write" for v in perms.values()):
            self.add("MEDIUM", "workflow", "write permission at workflow level; move to the job that needs it", "SCORECARD")

        if "pull_request_target" in triggers:
            self.add("MEDIUM", "on", "pull_request_target runs with secrets; ensure no PR code is executed. "
                     "Blocked by default in public repos from 2026-12-02 unless allowed by policy",
                     "GH-PRT, GH-PRT-BLOCK")
        if "concurrency" not in doc and triggers & {"push", "pull_request"}:
            self.add("INFO", "workflow", "no concurrency group; superseded runs will not be cancelled", "GH-CONCURRENCY")

        for jid, job in (doc.get("jobs") or {}).items():
            if not isinstance(job, dict):
                continue
            w = f"jobs.{jid}"
            if "uses" in job:  # reusable workflow call
                self.check_uses(job["uses"], w)
                if job.get("secrets") == "inherit":
                    self.add("LOW", w, "secrets: inherit — pass named secrets unless the callee is in your org",
                             "ZIZMOR-AUDITS")
                continue
            if "timeout-minutes" not in job:
                self.add("LOW", w, "no timeout-minutes (default 360)", "GH-CONCURRENCY")
            jp = job.get("permissions")
            if jp == "write-all":
                self.add("HIGH", w, "job permissions: write-all", "GH-SECURE-USE")
            runs_on = job.get("runs-on")
            if "self-hosted" in json.dumps(runs_on):
                self.add("INFO", w, "self-hosted runner: never use on public repos; prefer ephemeral runners",
                         "GH-SECURE-USE")
            if job.get("container") and isinstance(job["container"], dict):
                self.check_uses("docker://" + str(job["container"].get("image", "")), w + ".container")
            env = job.get("environment")
            if "id-token" in json.dumps(jp or {}) and not env:
                self.add("INFO", w, "OIDC job without environment:; consider environment-scoped trust", "GH-OIDC")
            for i, step in enumerate(job.get("steps") or []):
                name = step.get("name") or step.get("uses") or step.get("id") or f"#{i}"
                self.check_step(step, f"{w}.steps[{i}] {name}", triggers)

    # ---------- composite action ----------
    def check_action(self, doc):
        runs = doc.get("runs") or {}
        using = str(runs.get("using", ""))
        if using.startswith("node") and using != "node24":
            self.add("HIGH", "runs.using", f"{using} is not available on runners; use node24", "GH-NODE20")
        if using == "composite":
            for i, step in enumerate(runs.get("steps") or []):
                name = step.get("name") or step.get("uses") or f"#{i}"
                self.check_step(step, f"runs.steps[{i}] {name}", set(), composite=True)

    def run(self):
        try:
            with open(self.path, encoding="utf-8") as fh:
                doc = yaml.safe_load(fh)
        except Exception as exc:  # noqa: BLE001
            self.add("HIGH", "file", f"YAML parse error: {exc}", "GH-SYNTAX")
            return self.findings
        if not isinstance(doc, dict):
            self.add("HIGH", "file", "not a YAML mapping", "GH-SYNTAX")
        elif "runs" in doc:
            self.check_action(doc)
        elif "jobs" in doc:
            self.check_workflow(doc)
        else:
            self.add("INFO", "file", "neither a workflow (jobs:) nor an action (runs:); skipped", "-")
        return self.findings


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("files", nargs="+")
    ap.add_argument("--json", action="store_true")
    args = ap.parse_args()
    findings = []
    for f in args.files:
        findings.extend(Checker(f).run())
    findings.sort(key=lambda x: (SEV_ORDER[x["severity"]], x["file"]))
    if args.json:
        print(json.dumps(findings, indent=2))
    else:
        if not findings:
            print("No findings.")
        for x in findings:
            print(f"[{x['severity']}] {x['file']} :: {x['where']}\n    {x['issue']}  (ref: {x['ref']})")
        counts = {s: sum(1 for x in findings if x["severity"] == s) for s in SEV_ORDER}
        print("\nSummary: " + ", ".join(f"{k}={v}" for k, v in counts.items()))
    sys.exit(1 if any(x["severity"] == "HIGH" for x in findings) else 0)


if __name__ == "__main__":
    main()
