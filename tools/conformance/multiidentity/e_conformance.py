#!/usr/bin/env python3
"""Collect private E test evidence. Never qualify a live mapped engine."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import time


ROOT = Path(__file__).resolve().parents[3]
PACKAGE = "github.com/omry/reploy/internal/"
SUITES = (
    ("identity", "identitymapping", ".", ("TestHostPreflightRequiresCompleteSources",)),
    ("native-state", "deploy", "^TestNativeMappingV1", (
        "TestNativeMappingV1AdmissionIsOneDurableTransaction",
        "TestNativeMappingV1AdmissionWriteFailureNeverGrantsRelease",
    )),
    ("private-orchestration", "dockerdeploy", "Mapped", (
        "TestMappedConformanceFreshJourneyV1",
        "TestMappedConformanceRefusalJourneyV1",
        "TestMappedConformanceInterruptedJourneyV1",
        "TestMappedConformanceCancellationJourneyV1",
        "TestMappedReplacementEntryV1QualifiedOwnerRefusesBeforeEffects",
        "TestMappedEntryAdaptersV1CheckBothSessionOwners",
        "TestMappedSeccompCompilerRemovesEveryEscapeAllowance",
    )),
    ("production-exec-mechanism", "probe", "^TestMapped(Production|StartupPreflight|StandardDescriptors|KernelCapabilityLimit)", (
        "TestMappedProductionExecTailV1", "TestMappedProductionStartupV1",
    )),
)


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def command(argv: list[str], stderr_path: Path, *, timeout: int = 60) -> bytes:
    try:
        process = subprocess.run(argv, cwd=ROOT, timeout=timeout,
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    except subprocess.TimeoutExpired as error:
        stderr_path.write_bytes(error.stderr or b"")
        raise
    stderr_path.write_bytes(process.stderr)
    process.check_returncode()
    return process.stdout


def source_inventory(root: Path = ROOT) -> dict[str, dict]:
    # Include embedded policy/data, fixture code and build inputs, not just the
    # new test. Ignore Python caches and evidence directories by construction.
    paths = {root / name for name in ("go.mod", "go.sum", "AGENTS.md", ".github/workflows/ci.yml")}
    for directory in ("internal", "cmd"):
        paths.update(p for p in (root / directory).rglob("*") if p.is_file())
    here = root / "tools/conformance/multiidentity"
    paths.update(here / name for name in ("e_conformance.py", "test_e_conformance.py", "E_CONFORMANCE.md"))
    return {
        p.relative_to(root).as_posix(): {"sha256": digest(p.read_bytes()), "mode": p.stat().st_mode & 0o777}
        for p in sorted(paths)
    }


def toolchain(output: Path, phase: str) -> dict:
    env = json.loads(command(["go", "env", "-json", "GOOS", "GOARCH", "GOVERSION", "GOTOOLDIR", "GOEXPERIMENT", "GOFLAGS", "CGO_ENABLED"],
                             output / f"go-env-{phase}.stderr"))
    tools = {}
    suffix = ".exe" if os.name == "nt" else ""
    for name in ("compile", "link"):
        path = Path(env["GOTOOLDIR"]) / (name + suffix)
        tools[name] = {"path": str(path), "sha256": digest(path.read_bytes())}
    for name in ("go", "cc", "unshare", "newuidmap", "newgidmap"):
        located = shutil.which(name)
        tools[name] = None if located is None else {"path": located, "sha256": digest(Path(located).read_bytes())}
    return {"go_env": env, "tools": tools,
            "runtime_flags": {name: os.environ.get(name) for name in ("GODEBUG", "GOMAXPROCS", "GOAMD64", "GOARM64")},
            "host": {"system": platform.system(), "release": platform.release(), "machine": platform.machine()}}


def summarize(raw: bytes, package: str, required: tuple[str, ...], returncode: int | None) -> dict:
    events = [json.loads(line) for line in raw.splitlines() if line.strip()]
    if any(not isinstance(e, dict) or e.get("Package") != PACKAGE + package for e in events):
        raise ValueError("unexpected Go event package or shape")
    terminal = {}
    package_actions = []
    for event in events:
        action = event.get("Action")
        if action in ("pass", "fail", "skip"):
            if "Test" in event:
                if event["Test"] in terminal:
                    raise ValueError("duplicate terminal test event")
                terminal[event["Test"]] = action
            else:
                package_actions.append(action)
    missing = [name for name in required if name not in terminal]
    failed = sorted(name for name, action in terminal.items() if action == "fail")
    skipped = sorted(name for name, action in terminal.items() if action == "skip")
    required_gaps = [name for name in required if terminal.get(name) != "pass"]
    ok = returncode == 0 and package_actions == ["pass"] and not missing and not failed
    return {"status": "failed" if not ok else "partial" if skipped or required_gaps else "passed",
            "missing": missing, "failed": failed, "skipped": skipped,
            "required_gaps": required_gaps, "terminal_tests": terminal,
            "returncode": returncode, "log_sha256": digest(raw)}


def fresh_matrix(result: dict) -> list[str]:
    terminals = result["terminal_tests"]
    return [f"{profile}/{entry}/{repeat}"
            for profile in ("exact", "bounded")
            for entry in ("workload", "application", "runtime-test", "hook")
            for repeat in range(3)
            if terminals.get(f"TestMappedConformanceFreshJourneyV1/{profile}/{entry}/{repeat}") != "pass"]


def report_results(results: dict, stable: bool) -> dict:
    private_names = ("identity", "native-state", "private-orchestration")
    private = all(results[name]["status"] == "passed" for name in private_names)
    matrix_gaps = fresh_matrix(results["private-orchestration"])
    kernel = results["production-exec-mechanism"]
    # A zero process exit is collection success, never a mapped-support claim.
    collection_ok = stable and not matrix_gaps and private and kernel["status"] != "failed"
    return {"collection_ok": collection_ok, "source_and_toolchain_stable": stable,
            "private_conformance_passed": stable and private and not matrix_gaps,
            "fresh_instance_matrix_gaps": matrix_gaps,
            "production_exec_mechanism": kernel["status"],
            "live_engine": {"status": "not-qualified", "engine": None, "oci_runtime": None,
                            "reason": "No live engine is exercised by this runner; fixture engine/native/F observations are injected."},
            "mapped_runtime_qualified": False, "public_activation": "inhibited",
            "remaining_gates": ["Production F code/mount/network observation and exact live engine/OCI policy qualification.",
                                "Working replacement and occupied entry remain refused; G public activation is separate.",
                                "Every skipped/missing kernel case remains an explicit qualification gap."]}


def run(output: Path) -> dict:
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    inventory = source_inventory()
    context = toolchain(output, "before")
    results = {}
    for name, package, selection, required in SUITES:
        argv = ["go", "test", "-json", "-count=1", "-timeout=10m", "./internal/" + package, "-run", selection]
        if name == "private-orchestration":
            # The opt-in engine test belongs to separate live qualification.
            # Never run it here, even if its enabling environment is inherited.
            argv += ["-skip", "^TestMappedPodmanCreateLiveV1$"]
        started = time.monotonic()
        try:
            process = subprocess.run(argv, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=660, check=False)
            raw, stderr, code = process.stdout, process.stderr, process.returncode
        except subprocess.TimeoutExpired as error:
            raw, stderr, code = error.stdout or b"", error.stderr or b"", None
        (output / (name + ".jsonl")).write_bytes(raw)
        (output / (name + ".stderr")).write_bytes(stderr)
        try:
            result = summarize(raw, package, required, code)
        except (ValueError, TypeError, KeyError) as error:
            result = {"status": "failed", "error": str(error), "terminal_tests": {}, "log_sha256": digest(raw), "returncode": code}
        result.update(command=argv, elapsed_seconds=time.monotonic() - started,
                      stderr_sha256=digest(stderr))
        results[name] = result
        print(json.dumps({"suite": name, "status": result["status"], "elapsed_seconds": result["elapsed_seconds"]}), flush=True)
    final_context = toolchain(output, "after")
    stable = inventory == source_inventory() and context == final_context
    report = {"schema_version": 1, "collected_at": dt.datetime.now(dt.timezone.utc).isoformat(),
              "repository": str(ROOT), "source": inventory, "context": context,
              "metadata_stderr_sha256": {phase: digest((output / f"go-env-{phase}.stderr").read_bytes())
                                          for phase in ("before", "after")},
              "policy_source": "internal/dockerdeploy/seccomp/docker-v0.2.4.json",
              "selection_exclusions": {"TestMappedPodmanCreateLiveV1": "Separate opt-in live-engine qualification; not exercised or qualified here."},
              "suites": results, "summary": report_results(results, stable)}
    (output / "report.json").write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", required=True, type=Path, help="new evidence directory; its parent must exist")
    args = parser.parse_args()
    try:
        report = run(args.output_dir)
    except (OSError, subprocess.SubprocessError, ValueError) as error:
        print(f"E conformance collection failed: {error}", file=sys.stderr)
        return 1
    print(json.dumps(report["summary"], indent=2))
    return 0 if report["summary"]["collection_ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
