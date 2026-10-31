from __future__ import annotations

import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("e_conformance", HERE / "e_conformance.py")
e = importlib.util.module_from_spec(spec)
spec.loader.exec_module(e)


def events(package: str, actions: list[tuple[str | None, str]]) -> bytes:
    return b"\n".join(json.dumps({"Package": e.PACKAGE + package, "Action": action,
                                 **({"Test": test} if test is not None else {})}).encode()
                       for test, action in actions)


def complete_results() -> dict:
    results = {name: {"status": "passed", "terminal_tests": {}} for name, _, _, _ in e.SUITES}
    terminals = results["private-orchestration"]["terminal_tests"]
    for profile in ("exact", "bounded"):
        for entry in ("workload", "application", "runtime-test", "hook"):
            for repeat in range(3):
                terminals[f"TestMappedConformanceFreshJourneyV1/{profile}/{entry}/{repeat}"] = "pass"
    return results


class EvidenceTests(unittest.TestCase):
    def test_metadata_json_remains_separate_from_stderr_on_success_and_failure(self):
        with tempfile.TemporaryDirectory() as temporary:
            stderr = Path(temporary) / "metadata.stderr"
            for code in (0, 3):
                argv = [sys.executable, "-c",
                        'import sys; print("{\\\"GOOS\\\":\\\"linux\\\"}"); '
                        'sys.stderr.write("go: downloading example.invalid/module v1\\n"); '
                        f'sys.exit({code})']
                if code:
                    with self.assertRaises(subprocess.CalledProcessError):
                        e.command(argv, stderr)
                else:
                    self.assertEqual(json.loads(e.command(argv, stderr)), {"GOOS": "linux"})
                self.assertEqual(stderr.read_bytes(), b"go: downloading example.invalid/module v1\n")

    def test_metadata_timeout_retains_stderr_and_stays_a_failure(self):
        with tempfile.TemporaryDirectory() as temporary:
            stderr = Path(temporary) / "metadata.stderr"
            error = subprocess.TimeoutExpired(["go", "env"], 60, output=b"{}", stderr=b"partial diagnostic")
            with mock.patch.object(e.subprocess, "run", side_effect=error):
                with self.assertRaises(subprocess.TimeoutExpired):
                    e.command(["go", "env"], stderr)
            self.assertEqual(stderr.read_bytes(), b"partial diagnostic")

    def test_suite_chatter_and_timeout_keep_separate_digest_bound_artifacts(self):
        for timeout in (False, True):
            with self.subTest(timeout=timeout), tempfile.TemporaryDirectory() as temporary:
                output = Path(temporary) / "evidence"
                responses = []
                for index, (_, package, _, required) in enumerate(e.SUITES):
                    cases = list(required)
                    if package == "dockerdeploy":
                        cases += list(complete_results()["private-orchestration"]["terminal_tests"])
                    raw = events(package, [(case, "pass") for case in cases] + [(None, "pass")])
                    stderr = b"go: downloading example.invalid/module v1\n"
                    if timeout and index == 0:
                        responses.append(subprocess.TimeoutExpired(["go", "test"], 660, output=raw, stderr=stderr))
                    else:
                        responses.append(subprocess.CompletedProcess(["go", "test"], 0, raw, stderr))

                def metadata(directory, phase):
                    (directory / f"go-env-{phase}.stderr").write_bytes(b"metadata chatter\n")
                    return {"same": "toolchain"}

                with mock.patch.object(e, "source_inventory", return_value={}), \
                        mock.patch.object(e, "toolchain", side_effect=metadata), \
                        mock.patch.object(e.subprocess, "run", side_effect=responses) as run:
                    report = e.run(output)
                self.assertEqual(report["summary"]["collection_ok"], not timeout)
                for call in run.call_args_list:
                    self.assertEqual(call.kwargs["stderr"], subprocess.PIPE)
                for name, _, _, _ in e.SUITES:
                    self.assertEqual((output / (name + ".stderr")).read_bytes(), stderr)
                    self.assertEqual(report["suites"][name]["stderr_sha256"], e.digest(stderr))
                    self.assertEqual(report["suites"][name]["log_sha256"], e.digest((output / (name + ".jsonl")).read_bytes()))
                for phase in ("before", "after"):
                    self.assertEqual(report["metadata_stderr_sha256"][phase], e.digest(b"metadata chatter\n"))

    def test_missing_required_case_cannot_pass_package_only(self):
        result = e.summarize(events("probe", [(None, "pass")]), "probe", ("TestRequired",), 0)
        self.assertEqual(result["status"], "failed")
        self.assertEqual(result["missing"], ["TestRequired"])

    def test_skipped_root_and_child_remain_visible(self):
        for actions in ([('TestRequired', 'skip'), (None, 'pass')],
                        [('TestRequired/blocked', 'skip'), ('TestRequired', 'pass'), (None, 'pass')]):
            with self.subTest(actions=actions):
                raw = events("probe", actions)
                result = e.summarize(raw, "probe", ("TestRequired",), 0)
                self.assertEqual(result["status"], "partial")
                self.assertTrue(result["skipped"])
                self.assertEqual(result["log_sha256"], e.digest(raw))

    def test_failed_subtest_nonzero_timeout_and_truncated_log_refuse(self):
        for code, actions in ((0, [('TestRequired/child', 'fail'), ('TestRequired', 'pass'), (None, 'pass')]),
                              (1, [('TestRequired', 'pass'), (None, 'pass')]),
                              (None, [('TestRequired', 'pass'), (None, 'pass')]),
                              (0, [('TestRequired', 'pass')])):
            with self.subTest(code=code, actions=actions):
                result = e.summarize(events("probe", actions), "probe", ("TestRequired",), code)
                self.assertEqual(result["status"], "failed")

    def test_duplicate_terminal_wrong_package_and_invalid_json_refuse(self):
        samples = [events("probe", [('TestRequired', 'pass'), ('TestRequired', 'pass'), (None, 'pass')]),
                   events("dockerdeploy", [('TestRequired', 'pass'), (None, 'pass')]),
                   b'not JSON', b'[]']
        for raw in samples:
            with self.subTest(raw=raw):
                with self.assertRaises((ValueError, TypeError)):
                    e.summarize(raw, "probe", ("TestRequired",), 0)

    def test_each_fresh_profile_entry_and_repetition_required(self):
        result = complete_results()["private-orchestration"]
        self.assertEqual(e.fresh_matrix(result), [])
        for case in list(result["terminal_tests"]):
            for status in ("skip", "fail", None):
                changed = copy.deepcopy(result)
                if status is None:
                    del changed["terminal_tests"][case]
                else:
                    changed["terminal_tests"][case] = status
                self.assertEqual(len(e.fresh_matrix(changed)), 1)

    def test_injected_pass_cannot_become_live_runtime_qualification(self):
        result = e.report_results(complete_results(), True)
        self.assertTrue(result["collection_ok"])
        self.assertTrue(result["private_conformance_passed"])
        self.assertFalse(result["mapped_runtime_qualified"])
        self.assertEqual(result["live_engine"]["status"], "not-qualified")
        self.assertIsNone(result["live_engine"]["engine"])
        self.assertEqual(result["public_activation"], "inhibited")

    def test_kernel_gap_is_explicit_and_not_an_injected_pass(self):
        results = complete_results()
        results["production-exec-mechanism"]["status"] = "partial"
        result = e.report_results(results, True)
        self.assertTrue(result["private_conformance_passed"])
        self.assertEqual(result["production_exec_mechanism"], "partial")
        self.assertFalse(result["mapped_runtime_qualified"])
        results["production-exec-mechanism"]["status"] = "failed"
        self.assertFalse(e.report_results(results, True)["collection_ok"])

    def test_any_private_gap_or_source_change_blocks_collection_success(self):
        for name in ("identity", "native-state", "private-orchestration"):
            results = complete_results()
            results[name]["status"] = "partial"
            self.assertFalse(e.report_results(results, True)["collection_ok"])
        result = e.report_results(complete_results(), False)
        self.assertFalse(result["collection_ok"])
        self.assertFalse(result["private_conformance_passed"])

    def test_source_inventory_binds_dependencies_modes_and_new_files(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            names = ["go.mod", "go.sum", "AGENTS.md", ".github/workflows/ci.yml",
                     "internal/dependency/policy.json", "cmd/reploy/main.go",
                     "tools/conformance/multiidentity/e_conformance.py",
                     "tools/conformance/multiidentity/test_e_conformance.py",
                     "tools/conformance/multiidentity/E_CONFORMANCE.md"]
            for name in names:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(name)
            initial = e.source_inventory(root)
            dependency = root / "internal/dependency/policy.json"
            dependency.write_text("changed policy")
            self.assertNotEqual(initial, e.source_inventory(root))
            dependency.write_text("internal/dependency/policy.json")
            dependency.chmod(0o700)
            self.assertNotEqual(initial, e.source_inventory(root))
            (root / "internal/new.go").write_text("new source")
            self.assertIn("internal/new.go", e.source_inventory(root))
            dependency.unlink()
            self.assertNotIn("internal/dependency/policy.json", e.source_inventory(root))


if __name__ == "__main__":
    unittest.main()
