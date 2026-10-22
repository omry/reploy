"""Private admission/cleanup regressions; the live build-failure test is opt-in."""

import contextlib
import copy
import io
import json
import os
import pathlib
import secrets
import shutil
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

import conformance as c
import operation_audit as audit


class FakeRunner:
    def __init__(self, root):
        self.root = pathlib.Path(root)
        self.commands = []
        self.resources = {}
        self.nonces = {}
        self.created = []
        self.released = []

    def run(self, args, **kwargs):
        args = list(map(str, args))
        self.commands.append(args)
        output = ""
        code = 0
        if args[2] == "exists":
            code = 0 if (args[1], args[3]) in self.resources else 1
        elif args[1:3] == ["volume", "create"]:
            name = args[-1]
            mount = self.root / name
            mount.mkdir()
            self.resources["volume", name] = {
                "Labels": {c.OWNER_LABEL: args[-2].split("=", 1)[1]},
                "Mountpoint": str(mount),
            }
        elif args[1] == "create":
            name = args[args.index("--name") + 1]
            identity = f"{len(self.created) + 1:064x}"
            self.created.append(identity)
            self.nonces[identity] = args[-2]
            self.resources["container", identity] = {
                "Config": {"Labels": {c.OWNER_LABEL: args[args.index("--label") + 1].split("=", 1)[1]}},
                "State": {"Pid": len(self.created)},
                "Name": name,
            }
            output = identity
        elif args[1] == "logs":
            output = f"admission-ready {self.nonces[args[2]]}\n"
        elif args[1] == "exec":
            self.released.append(args[2])
        elif args[2] == "rm":
            self.resources.pop((args[1], args[-1]), None)
        return subprocess.CompletedProcess(args, code, output, "")

    def json(self, args, **kwargs):
        args = list(args)
        if args[1] == "inspect":
            return [self.resources["container", args[2]]]
        if args[2] == "inspect":
            return [self.resources[args[1], args[3]]]
        raise AssertionError(args)


def maps(slot, gids=False):
    base = 100000 + slot * 20
    ids = [0, 5, 100, 2001, 3001, 3002] if gids else [0, 100, 1001]
    return [(identity, base + index, 1) for index, identity in enumerate(ids)]


def authority(uid_maps, gid_maps, **extra):
    defaults = dict(
        caller_names={"caller", "1000"},
        uid_ranges=[("caller", 100000, 1000)],
        gid_ranges=[("caller", 100000, 1000)],
        own_uids=[(100000, 1000)], own_gids=[(100000, 1000)],
        resolve_uids=lambda values: {}, resolve_gids=lambda values: {},
    )
    return c.validate_host_authority(uid_maps, gid_maps, **{**defaults, **extra})


class SupervisorCapabilityTests(unittest.TestCase):
    def report(self):
        report = valid_thread_supervisor_report()
        report.pop("omitted_chown")
        return report

    def test_masks_alone_do_not_prove_omitted_capability_denial(self):
        with self.assertRaises(c.ConformanceError):
            c.check_supervisor(self.report())

    def test_failed_supervisor_operation_rejects_trial(self):
        report = self.report()
        report["omitted_chown"] = {"pass": False}
        with self.assertRaises(c.ConformanceError):
            c.check_supervisor(report)


def valid_thread_supervisor_report():
    caps = f"{c.EXPECTED_SUPERVISOR_CAPS:016x}"
    threads = []
    for tid in (10, 11, 12):
        status = {
            "Pid": str(tid), "Tgid": "10", "Uid": "100\t100\t100\t100",
            "Gid": "100\t100\t100\t100", "Groups": "100",
            "CapEff": caps if tid == 11 else "0000000000000000",
            "CapPrm": caps if tid == 11 else "0000000000000000",
            "CapInh": caps if tid == 11 else "0000000000000000",
            "CapBnd": caps, "CapAmb": "0000000000000000",
            "NoNewPrivs": "1", "Seccomp": "2",
        }
        threads.append({"tid": tid, "status": status})
    return {"pid": 10, "launcher_tid": 11, "thread_ids": [10, 11, 12],
            "threads": threads, "status": copy.deepcopy(threads[1]["status"]),
            "dumpable": 0, "omitted_chown": {"pass": True}}


class SupervisorThreadEvidenceTests(unittest.TestCase):
    def test_complete_inventory_can_distinguish_leader_and_launcher(self):
        c.check_supervisor(valid_thread_supervisor_report())

    def test_bad_nonleader_rejects_despite_correct_launcher(self):
        malformed = {
            "Uid": "100\t100\t100\t1001", "Gid": "100\t100\t100\t2001",
            "Groups": "100 3001", "CapEff": "0000000000000001",
            "CapPrm": "0000000000000001", "CapInh": "0000000000000001",
            "CapBnd": "00000000000001c1", "CapAmb": "0000000000000080",
            "NoNewPrivs": "0", "Seccomp": "0", "Pid": "99", "Tgid": "99",
        }
        for key, value in malformed.items():
            with self.subTest(field=key):
                report = valid_thread_supervisor_report()
                report["threads"][2]["status"][key] = value
                with self.assertRaises(c.ConformanceError):
                    c.check_supervisor(report)

    def test_incomplete_or_malformed_thread_inventory_rejects(self):
        mutations = (
            lambda r: r.pop("threads"),
            lambda r: r.update(threads=[]),
            lambda r: r["threads"].pop(),
            lambda r: r["threads"].append(copy.deepcopy(r["threads"][-1])),
            lambda r: r.update(thread_ids=[10, 11, 99]),
            lambda r: r.update(thread_ids=[12, 11, 10]),
            lambda r: r.update(launcher_tid=99),
            lambda r: r.update(pid=True),
            lambda r: r["threads"][2].update(tid=True),
            lambda r: r["threads"][2].update(status=None),
            lambda r: r["threads"][2]["status"].update(CapEff="invalid"),
            lambda r: r.update(thread_ids=list(range(1, 130))),
        )
        for index, change in enumerate(mutations):
            with self.subTest(case=index):
                report = valid_thread_supervisor_report()
                change(report)
                with self.assertRaises(c.ConformanceError):
                    c.check_supervisor(report)


class BoundedMappingGeometryTests(unittest.TestCase):
    def test_single_and_equivalent_segmented_maps_pass(self):
        for mapping in (
            [(0, 100000, 4096)],
            [(0, 100000, 2048), (2048, 102048, 2048)],
        ):
            with self.subTest(mapping=mapping):
                result = c.assert_bounded_mapping_geometry(
                    mapping, kind="UID", workload=0
                )
                self.assertEqual(result["container_start"], 0)
                self.assertEqual(result["container_end"], 4095)
                self.assertEqual(result["host_start"], 100000)
                self.assertEqual(result["host_end"], 104095)
                self.assertEqual(result["offset"], 100000)

    def test_reordered_gapped_overlapping_and_offset_changing_maps_fail(self):
        malformed = {
            "reordered": [(2048, 102048, 2048), (0, 100000, 2048)],
            "gapped": [(0, 100000, 2048), (2049, 102049, 2047)],
            "overlapping": [(0, 100000, 2048), (2047, 102047, 2049)],
            "offset-changing": [(0, 100000, 2048), (2048, 102049, 2048)],
            "non-positive": [(0, 100000, 0)],
        }
        for name, mapping in malformed.items():
            with self.subTest(name=name), self.assertRaisesRegex(
                c.ConformanceError, "bounded UID mapping geometry"
            ):
                c.assert_bounded_mapping_geometry(mapping, kind="UID", workload=0)

    def test_bounded_profile_checks_uid_and_gid_geometry_before_set_equality(self):
        uid_map = [(0, 100000, 2048), (2048, 102049, 2048)]
        gid_map = [(0, 110000, 2048), (2048, 112048, 2048)]
        with self.assertRaisesRegex(c.ConformanceError, "bounded UID mapping geometry"):
            c.assert_mapping_profile("bounded", [uid_map], [gid_map])

        valid_uid = [(0, 100000, 2048), (2048, 102048, 2048)]
        valid_gid = [(0, 110000, 2048), (2048, 112048, 2048)]
        self.assertTrue(c.assert_mapping_profile("bounded", [valid_uid], [valid_gid])["pass"])

        malformed_uid = [(2048, 102048, 2048), (0, 100000, 2048)]
        with self.assertRaisesRegex(c.ConformanceError, "bounded UID mapping geometry"):
            c.assert_mapping_profile("bounded", [valid_uid, malformed_uid], [valid_gid])

        malformed_gid = [(2048, 112048, 2048), (0, 110000, 2048)]
        with self.assertRaisesRegex(c.ConformanceError, "bounded GID mapping geometry"):
            c.assert_mapping_profile("bounded", [valid_uid], [valid_gid, malformed_gid])


class AdmissionTests(unittest.TestCase):
    @unittest.skipUnless(os.environ.get("REPLOY_MI_LIVE_BUILD_FAILURE_TEST") == "1",
                         "requires the explicitly selected disposable Podman test store")
    def test_failed_fixture_run_restores_image_and_build_container_inventory(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            fixture = root / "fixture"
            fixture.mkdir()
            shutil.copy2(c.HERE / "posix_ipc.c", fixture / "posix_ipc.c")
            (fixture / "Containerfile").write_text(
                'FROM scratch\nCOPY probe /probe\n'
                'COPY posix-ipc-probe /posix-ipc-probe\n'
                'RUN ["/probe", "injected-build-failure"]\n')
            output = root / "output"
            output.mkdir()
            runner = c.Runner(output)
            token = secrets.token_hex(16)
            image = f"localhost/reploy-local/mi-failed-build:{token}"
            before = c.inventory(runner, "podman")
            build_containers_before = runner.run(
                ["podman", "ps", "--external", "-aq"]).stdout.splitlines()
            self.assertFalse(c.resource_exists(runner, "image", image))
            try:
                with mock.patch.object(c, "HERE", fixture):
                    with self.assertRaisesRegex(c.ConformanceError,
                                                'unknown mode "injected-build-failure"'):
                        c.build_image(runner, output, image, token)
                self.assertFalse(c.resource_exists(runner, "image", image))
                self.assertEqual(c.inventory(runner, "podman"), before)
                self.assertEqual(runner.run(
                    ["podman", "ps", "--external", "-aq"]).stdout.splitlines(),
                    build_containers_before)
            finally:
                # Only an unexpected successful build can create this tag;
                # ownership must still be verified before removing it.
                c.remove_owned_resource(runner, "image", image, token)

    def test_audit_failure_remains_a_blocker_without_fallback(self):
        results = {}
        audit.observe(results, "unsupported", lambda: (_ for _ in ()).throw(c.ConformanceError("API unavailable")))
        self.assertEqual(results["unsupported"]["disposition"], "blocked")
        audit.observe(results, "supported", lambda: {"Id": "owned"})
        self.assertEqual(results["supported"]["evidence"], {"Id": "owned"})

    def test_audit_api_closes_connection_on_success_and_rejection(self):
        for status, body in ((201, b'{"Id":"created"}'),
                             (400, b'{"message":"unsupported"}'),
                             (500, b'{"message":"backend mentions HTTP 404"}')):
            with self.subTest(status=status), mock.patch.object(audit, "UnixHTTP") as factory:
                connection = factory.return_value
                response = connection.getresponse.return_value
                response.status = status
                response.read.return_value = body
                if status == 201:
                    self.assertEqual(audit.api("socket", "POST", "/containers/create", {"Image": "fixture"}), {"Id": "created"})
                else:
                    with self.assertRaises(audit.HTTPError) as raised:
                        audit.api("socket", "POST", "/containers/create", {})
                    self.assertEqual(raised.exception.status, status)
                connection.close.assert_called_once()

    def test_audit_accepts_empty_existing_directory_and_rejects_nonempty(self):
        with tempfile.TemporaryDirectory() as root:
            output = pathlib.Path(root)
            audit.prepare_output(output)
            audit.prepare_output(output / "fresh")
            (output / "foreign").write_text("keep")
            with mock.patch.object(c, "Runner") as runner, \
                    mock.patch.object(sys, "argv", ["audit", "--output-dir", str(output)]):
                with self.assertRaisesRegex(c.ConformanceError, "must be empty"):
                    audit.main()
            runner.assert_not_called()
            self.assertEqual((output / "foreign").read_text(), "keep")

    def test_restart_admission_requires_a_new_exact_nonce_line(self):
        runner = mock.Mock()
        nonce = "a" * 32
        old = f"admission-ready {nonce}\n" * 2
        runner.run.side_effect = [mock.Mock(stdout=old),
                                  mock.Mock(stdout=old + "admission-ready foreign\n"),
                                  mock.Mock(stdout=old + f"admission-ready {nonce}\n")]
        with mock.patch.object(audit.time, "sleep"):
            audit.wait_restarted_admission(runner, "owned", nonce, 2)
        self.assertEqual(runner.run.call_count, 3)
        runner.run.assert_called_with(["podman", "logs", "owned"])

    def test_restart_admission_rejects_persistent_old_logs(self):
        runner = mock.Mock()
        nonce = "a" * 32
        runner.run.return_value.stdout = f"admission-ready {nonce}\n"
        with mock.patch.object(audit.time, "sleep"):
            with self.assertRaisesRegex(c.ConformanceError, "did not re-enter"):
                audit.wait_restarted_admission(runner, "owned", nonce, 1)
        self.assertEqual(runner.run.call_count, 40)

    def test_audit_restart_compatibility_requires_all_postconditions(self):
        cleanup_failures = ("cleanup-api-error", "cleanup-absence-error")
        compatible_lifecycle = ("compatible", "preflight-error", *cleanup_failures)
        post_restart_failures = ("exec-error", "wait-error", "logs-error")
        for outcome in ("compatible", "mapping-change", "stale-admission", "restart-error",
                        "create-error", "start-error", "admission-error", "inspect-error",
                        "initial-mapping-error", "preflight-error", *cleanup_failures, *post_restart_failures):
            with self.subTest(outcome=outcome), tempfile.TemporaryDirectory() as root:
                output = pathlib.Path(root) / "audit"
                runner, service = mock.Mock(), mock.Mock()
                service.poll.return_value = None
                state = {"restarted": False, "removed": False, "cleanup_failed": False, "mapping_attempts": 0, "network_attempts": 0}
                identity = "a" * 64

                def start_service(argv, **_kwargs):
                    pathlib.Path(argv[-1].removeprefix("unix://")).touch()
                    return service

                def create(_path, kind, name, token, _action, resources, _attempts):
                    if kind == "container":
                        state["mapping_attempts"] += 1
                    if kind == "network":
                        state["network_attempts"] += 1
                    if kind != "container" or not name.endswith("bounded-auto"):
                        raise c.ConformanceError("independent seam unsupported")
                    if outcome == "create-error":
                        raise c.ConformanceError("create failed")
                    state["token"] = token
                    state["nonce"] = json.loads((output / "bounded-auto-request.json").read_text())["Cmd"][1]
                    resources.append((kind, identity))
                    return {"Id": identity}

                def api(_socket, method, route, _body=None):
                    if route == "/version":
                        return {"Components": [{"Name": "Podman"}]}
                    if method == "DELETE":
                        if outcome == "cleanup-api-error" and not state["cleanup_failed"]:
                            state["cleanup_failed"] = True
                            raise audit.HTTPError(method, route, 503, {})
                        state["removed"] = True
                        return {}
                    if route == f"/containers/{identity}/json" and state["removed"] and outcome == "cleanup-absence-error" and not state["cleanup_failed"]:
                        state["cleanup_failed"] = True
                        raise c.ConformanceError("removal confirmation unavailable")
                    if route == f"/containers/{identity}/json" and not state["removed"]:
                        return {"Config": {"Labels": {c.OWNER_LABEL: state["token"]}}}
                    raise audit.HTTPError(method, route, 404, {})

                def command(argv, **_kwargs):
                    if argv[:2] == ["go", "run"] and outcome == "preflight-error":
                        raise c.ConformanceError("capability probe unsupported")
                    if argv[:3] == ["docker", "image", "inspect"]:
                        return mock.Mock(stdout='[{"Id":"fixture"}]')
                    if argv[:2] == ["podman", "logs"]:
                        count = 2 if state["restarted"] and outcome != "stale-admission" else 1
                        return mock.Mock(stdout=f"admission-ready {state['nonce']}\n" * count)
                    if argv[:2] == ["docker", "start"] and outcome == "start-error":
                        raise c.ConformanceError("start failed")
                    if argv[:2] == ["docker", "restart"]:
                        if outcome == "restart-error":
                            raise c.ConformanceError("restart command failed")
                        state["restarted"] = True
                    if argv[:2] == ["docker", "inspect"]:
                        if outcome == "inspect-error":
                            raise c.ConformanceError("inspect failed")
                        return mock.Mock(stdout=json.dumps([{"State": {"Pid": 2 if state["restarted"] else 1}}]))
                    if argv[:2] == ["docker", "exec"] and outcome == "exec-error":
                        raise c.ConformanceError("exec failed")
                    if argv[:2] == ["docker", "wait"]:
                        return mock.Mock(stdout="1" if outcome == "wait-error" else "0")
                    if argv[:2] == ["docker", "logs"] and outcome == "logs-error":
                        raise c.ConformanceError("logs failed")
                    return mock.Mock(stdout="fixture evidence")

                def read_map(pid, _name):
                    if outcome == "initial-mapping-error":
                        return "1 100000 4096\n"
                    return "0 104096 4096\n" if pid == 2 and outcome == "mapping-change" else "0 100000 4096\n"

                runner.run.side_effect = command
                with mock.patch.object(sys, "argv", ["audit", "--output-dir", str(output)]), \
                        mock.patch.object(c, "Runner", return_value=runner), \
                        mock.patch.object(c, "inventory", return_value={}), \
                        mock.patch.object(c, "assert_host_authority", return_value={
                            "uid_host_ids": [(100000, 65536)], "gid_host_ids": [(100000, 65536)]}), \
                        mock.patch.object(c, "resource_exists", return_value=False), \
                        mock.patch.object(c, "build_image", return_value={"inspect": [{"Id": "fixture"}]}), \
                        mock.patch.object(c, "remove_owned_resource", return_value={"pass": True}), \
                        mock.patch.object(c, "wait_admission", side_effect=c.ConformanceError("admission failed") if outcome == "admission-error" else None), \
                        mock.patch.object(c, "read_proc", side_effect=read_map), \
                        mock.patch.object(audit, "tracked_create", side_effect=create), \
                        mock.patch.object(audit.subprocess, "Popen", side_effect=start_service), \
                        mock.patch.object(audit.time, "sleep"), \
                        mock.patch.object(audit, "api", side_effect=api):
                    self.assertEqual(audit.main(), 1 if outcome in cleanup_failures else 0)
                operations = json.loads((output / "summary.json").read_text())["operations"]
                self.assertIn("explicit-stop-start-restart", operations)
                self.assertIn("exec-wait-logs", operations)
                probe_expected = "blocked" if outcome == "preflight-error" else "observed-compatible"
                self.assertEqual(operations["existing-build-preflight"]["disposition"], probe_expected)
                self.assertEqual(operations["existing-build-command"]["disposition"], "blocked")
                if probe_expected == "observed-compatible":
                    self.assertNotIn("probe rejects", operations["existing-build-command"]["error"])
                expected = "observed-compatible" if outcome in compatible_lifecycle else "blocked"
                restart_expected = "observed-compatible" if outcome in post_restart_failures else expected
                self.assertEqual(operations["explicit-stop-start-restart"]["disposition"], restart_expected)
                self.assertEqual(operations["bounded-auto"]["disposition"], expected)
                self.assertEqual(operations["exec-wait-logs"]["disposition"], expected)
                released = any(call.args[0][1:4] == ["exec", identity, "/probe"]
                               for call in runner.run.call_args_list)
                self.assertEqual(released, outcome in compatible_lifecycle or outcome in post_restart_failures)
                self.assertIn("network-create-inspect", operations)
                if outcome in cleanup_failures:
                    self.assertEqual(state["mapping_attempts"], 1)
                    for profile in ("exact-sparse-candidate", "bounded-fixed-candidate"):
                        self.assertEqual(operations[profile]["disposition"], "blocked")
                        self.assertIn("cleanup", operations[profile]["error"])
                    self.assertEqual(state["network_attempts"], 1)
                self.assertEqual(state["removed"], outcome != "create-error")
                service.terminate.assert_called_once()
                self.assertFalse((output / "podman.sock").exists())

    def test_volume_failures_do_not_abort_independent_audit_cases(self):
        for failure in ("create", "ownership"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as root:
                output = pathlib.Path(root) / "audit"
                runner, service = mock.Mock(), mock.Mock()
                service.poll.return_value = None

                def start_service(argv, **_kwargs):
                    pathlib.Path(argv[-1].removeprefix("unix://")).touch()
                    return service

                def api(_socket, method, route, body=None):
                    if route == "/version":
                        return {"Components": [{"Name": "Podman"}]}
                    if method == "POST" and route == "/volumes/create":
                        if failure == "create":
                            raise audit.HTTPError(method, route, 501, {})
                        return {"Name": body["Name"]}
                    if route.startswith("/volumes/") and failure == "ownership":
                        return {"Labels": {c.OWNER_LABEL: "foreign"}}
                    if method == "GET":
                        raise audit.HTTPError(method, route, 404, {})
                    raise audit.HTTPError(method, route, 501, {})

                def command(argv, **_kwargs):
                    if argv[:3] == ["docker", "image", "inspect"]:
                        return mock.Mock(stdout='[{"Id":"fixture"}]')
                    raise c.ConformanceError("independent seam unsupported")

                runner.run.side_effect = command
                with mock.patch.object(sys, "argv", ["audit", "--output-dir", str(output)]), \
                        mock.patch.object(c, "Runner", return_value=runner), \
                        mock.patch.object(c, "inventory", return_value={}), \
                        mock.patch.object(c, "assert_host_authority", return_value={
                            "uid_host_ids": [(100000, 65536)], "gid_host_ids": [(100000, 65536)]}), \
                        mock.patch.object(c, "resource_exists", return_value=False), \
                        mock.patch.object(c, "build_image", return_value={"inspect": [{"Id": "fixture"}]}), \
                        mock.patch.object(c, "remove_owned_resource", return_value={"pass": True}), \
                        mock.patch.object(audit.subprocess, "Popen", side_effect=start_service), \
                        mock.patch.object(audit, "api", side_effect=api):
                    result = audit.main()
                summary = json.loads((output / "summary.json").read_text())
                operations = summary["operations"]
                for name in ("volume-create-inspect", "volume-inspect-command", "bounded-auto",
                             "exact-sparse-candidate", "bounded-fixed-candidate", "network-create-inspect"):
                    self.assertEqual(operations[name]["disposition"], "blocked")
                self.assertNotIn("error", summary)
                # A foreign post-create object is preserved and blocks cleanup;
                # unsupported creates with verified absence can finish cleanly.
                self.assertEqual(result, 0 if failure == "create" else 1)
                service.terminate.assert_called_once()
                self.assertFalse((output / "podman.sock").exists())

    def test_main_cleans_only_an_attempted_image_after_build_failure(self):
        with tempfile.TemporaryDirectory() as root:
            args = SimpleNamespace(profiles="exact", iterations=1, output_dir=pathlib.Path(root))
            with mock.patch.object(c, "parse_args", return_value=args), mock.patch.object(c, "inventory", return_value={}), mock.patch.object(c, "host_context", return_value={}), mock.patch.object(c, "docker_negative", return_value={"pass": True}), mock.patch.object(c, "assert_host_authority", return_value={"pass": True}), mock.patch.object(c, "resource_exists", return_value=False), mock.patch.object(c, "build_image", side_effect=c.ConformanceError("build failed")), mock.patch.object(c, "remove_owned_resource", return_value={"pass": True}) as remove:
                with self.assertRaisesRegex(c.ConformanceError, "build failed"):
                    c.main()
                self.assertEqual(remove.call_count, 1)
                self.assertEqual(remove.call_args.args[1], "image")
            summary = json.loads((pathlib.Path(root) / "summary.json").read_text())
            self.assertFalse(summary["pass"])
            self.assertTrue(summary["image_cleanup"]["pass"])

    def test_successful_admission_checks_all_maps_before_first_release(self):
        with tempfile.TemporaryDirectory() as root:
            runner = FakeRunner(root)
            admitted = []

            def check(_runner, uid_maps, gid_maps, *, planned_pool=False):
                if planned_pool:
                    return {"pass": True}
                admitted.append(authority(uid_maps, gid_maps))
                return admitted[-1]

            def release(_runner, container, nonce):
                self.assertEqual(len(admitted), 1)
                self.assertEqual(len(admitted[0]["uid_host_ids"]), 4)
                self.assertEqual(container, runner.created[0])
                raise c.ConformanceError("verified admission reached")

            def read(pid, kind):
                return "\n".join(" ".join(map(str, row)) for row in maps(pid - 1, kind == "gid_map"))

            with mock.patch.object(c, "assert_host_authority", side_effect=check), mock.patch.object(c, "read_proc", side_effect=read), mock.patch.object(c, "release_admission", side_effect=release):
                with self.assertRaisesRegex(c.ConformanceError, "verified admission reached"):
                    c.run_trial(runner, pathlib.Path(root), "image", "seccomp", "exact", 1, "attempt")
            self.assertEqual(runner.resources, {})

    def test_main_planned_rejection_does_not_build_or_delete_an_image(self):
        with tempfile.TemporaryDirectory() as root:
            args = SimpleNamespace(profiles="exact,bounded", iterations=3, output_dir=pathlib.Path(root))
            with mock.patch.object(c, "parse_args", return_value=args), mock.patch.object(c, "inventory", return_value={}), mock.patch.object(c, "host_context", return_value={}), mock.patch.object(c, "docker_negative", return_value={"pass": True}), mock.patch.object(c, "assert_host_authority", side_effect=c.ConformanceError("planned collision")), mock.patch.object(c, "build_image") as build, mock.patch.object(c, "remove_owned_resource") as remove:
                with self.assertRaisesRegex(c.ConformanceError, "planned collision"):
                    c.main()
                build.assert_not_called()
                remove.assert_not_called()
            summary = json.loads((pathlib.Path(root) / "summary.json").read_text())
            self.assertFalse(summary["pass"])
            self.assertTrue(summary["image_cleanup"]["not_attempted"])

    def test_full_delegation_pool_and_numeric_owner_are_validated(self):
        runner = mock.Mock()
        ranges = [("caller", 100000, 100), ("foreign", 100100, 100)]
        with mock.patch.object(c, "require_enumerable_subid_backend"), mock.patch.object(c.getpass, "getuser", return_value="caller"), mock.patch.object(c.os, "getuid", return_value=1000), mock.patch.object(c.os, "geteuid", return_value=1000), mock.patch.object(c.pwd, "getpwuid", return_value=SimpleNamespace(pw_name="caller")), mock.patch.object(c, "parse_subids", return_value=ranges), mock.patch.object(c, "query_subids", return_value=[(100000, 100)]), mock.patch.object(c, "resolved_principals", return_value={}) as resolve:
            record = c.assert_host_authority(runner, [], [], planned_pool=True)
        self.assertTrue(record["planned_pool"])
        self.assertTrue(record["numeric_owner_alias_supported"])
        self.assertEqual(record["uid_host_ids"], [list(range(100000, 100100))])
        self.assertEqual(resolve.call_count, 2, "synthetic cases should reuse only this check's NSS results")
        self.assertTrue(all(value["pass"] for value in record["negative_collision_cases"].values()))

    def test_host_authority_rejects_hostile_environment_login_name(self):
        runner = mock.Mock()
        ranges = [("foreign", 100000, 100), ("caller", 100100, 100)]
        with mock.patch.object(c, "require_enumerable_subid_backend"), mock.patch.object(c.getpass, "getuser", return_value="foreign"), mock.patch.object(c.os, "getuid", return_value=1000), mock.patch.object(c.os, "geteuid", return_value=1000), mock.patch.object(c.pwd, "getpwuid", return_value=SimpleNamespace(pw_name="caller")), mock.patch.object(c, "parse_subids", return_value=ranges), mock.patch.object(c, "query_subids", return_value=[(100000, 100)]):
            with self.assertRaisesRegex(c.ConformanceError, "disagrees with passwd account"):
                c.assert_host_authority(runner, [], [], planned_pool=True)

    def test_host_authority_rejects_real_effective_uid_mismatch(self):
        with mock.patch.object(c.os, "getuid", return_value=1000), mock.patch.object(c.os, "geteuid", return_value=1001):
            with self.assertRaisesRegex(c.ConformanceError, "matching real/effective UIDs"):
                c.effective_caller()

    def test_ambiguous_create_reconciles_server_side_resource_for_all_kinds(self):
        token = "attempt"
        for kind in ("network", "volume", "container"):
            with self.subTest(kind=kind):
                resources = []
                attempts = []
                name = f"reploy-mi-{kind}"
                item = {"Labels": {c.OWNER_LABEL: token}}

                def inspect(_path, method, route, _body=None):
                    self.assertEqual(method, "GET")
                    self.assertTrue(route.endswith(name) or route.endswith(f"{name}/json"))
                    return item

                with mock.patch.object(audit, "api", side_effect=inspect):
                    with self.assertRaisesRegex(c.ConformanceError, "response lost"):
                        audit.tracked_create(
                            "socket", kind, name, token,
                            lambda: (_ for _ in ()).throw(c.ConformanceError("response lost")),
                            resources, attempts,
                        )
                self.assertEqual(resources, [(kind, name)])
                self.assertEqual(attempts, [{"kind": kind, "name": name, "identity": name}])

    def test_successful_create_failed_inspect_reconciles_deterministic_name_for_all_kinds(self):
        token = "attempt"
        for kind in ("network", "volume", "container"):
            with self.subTest(kind=kind):
                resources = []
                attempts = []
                name = f"reploy-mi-{kind}"
                identity = f"{kind}-created-id"
                item = {"Labels": {c.OWNER_LABEL: token}}
                calls = []

                def inspect(_path, method, route, _body=None):
                    calls.append((method, route))
                    if len(calls) == 1:
                        raise c.ConformanceError("post-create inspect failed")
                    self.assertEqual(method, "GET")
                    self.assertTrue(route.endswith(name) or route.endswith(f"{name}/json"))
                    return item

                with mock.patch.object(audit, "api", side_effect=inspect):
                    with self.assertRaisesRegex(c.ConformanceError, "post-create inspect failed"):
                        audit.tracked_create(
                            "socket", kind, name, token,
                            lambda: {"Id": identity}, resources, attempts,
                        )
                self.assertEqual(resources, [(kind, name)])
                self.assertEqual(attempts, [{"kind": kind, "name": name, "identity": name}])

    def test_post_create_inspect_reconciles_deterministic_name_for_all_kinds(self):
        token = "attempt"
        for kind in ("network", "volume", "container"):
            with self.subTest(kind=kind):
                name = f"reploy-mi-{kind}"
                identity = f"{kind}-created-id"
                resources = [(kind, identity)]
                attempts = [{"kind": kind, "name": name, "identity": identity}]
                with mock.patch.object(audit, "api", return_value={"Labels": {c.OWNER_LABEL: token}}):
                    with self.assertRaisesRegex(c.ConformanceError, "post-create inspect failed"):
                        audit.tracked_post_create_step(
                            "socket", kind, name, identity, token, resources, attempts,
                            lambda: (_ for _ in ()).throw(c.ConformanceError("post-create inspect failed")),
                        )
                self.assertEqual(resources, [(kind, name)])
                self.assertEqual(attempts, [{"kind": kind, "name": name, "identity": name}])

    def test_ambiguous_foreign_collision_is_preserved(self):
        resources = []
        with mock.patch.object(audit, "api", return_value={"Labels": {c.OWNER_LABEL: "foreign"}}):
            with self.assertRaisesRegex(c.ConformanceError, "refusing foreign"):
                audit.tracked_create(
                    "socket", "network", "collision", "attempt",
                    lambda: (_ for _ in ()).throw(c.ConformanceError("response lost")),
                    resources, [],
                )
        self.assertEqual(resources, [])

    def test_reconcile_distinguishes_true_absence_from_engine_error(self):
        resources = []
        with mock.patch.object(audit, "api", side_effect=audit.HTTPError("GET", "/networks/missing", 404, {})):
            self.assertFalse(audit.reconcile_ambiguous_create("socket", "network", "missing", "attempt", resources))
        self.assertEqual(resources, [])
        with mock.patch.object(audit, "api", side_effect=c.ConformanceError("engine unavailable")):
            with self.assertRaisesRegex(c.ConformanceError, "engine unavailable"):
                audit.reconcile_ambiguous_create("socket", "network", "unknown", "attempt", resources)

    def test_reconcile_does_not_misclassify_deceptive_http_500_body(self):
        resources = []
        error = audit.HTTPError("GET", "/networks/object", 500, {"message": "HTTP 404 in backend text"})
        with mock.patch.object(audit, "api", side_effect=error):
            with self.assertRaises(audit.HTTPError) as raised:
                audit.reconcile_ambiguous_create("socket", "network", "object", "attempt", resources)
        self.assertIs(raised.exception, error)
        self.assertEqual(resources, [])
        with mock.patch.object(audit, "api", side_effect=c.ConformanceError("HTTP 404 in an engine error")):
            with self.assertRaisesRegex(c.ConformanceError, "HTTP 404"):
                audit.reconcile_ambiguous_create("socket", "network", "object", "attempt", resources)

    def test_cleanup_targets_retries_unresolved_attempts_once(self):
        resources = [("container", "created-id"), ("network", "network-id")]
        attempts = [
            {"kind": "container", "name": "container-name", "identity": "created-id"},
            {"kind": "network", "name": "network-name", "identity": None},
            {"kind": "volume", "name": "volume-name", "identity": None},
        ]
        self.assertEqual(
            audit.cleanup_targets(resources, attempts),
            [("container", "created-id"), ("network", "network-id"),
             ("network", "network-name"), ("volume", "volume-name")],
        )

    def test_failed_reconciliation_keeps_deterministic_final_cleanup_target(self):
        resources = [("network", "created-id")]
        attempts = [{"kind": "network", "name": "network-name", "identity": "created-id"}]
        with mock.patch.object(audit, "api", side_effect=c.ConformanceError("engine unavailable")):
            with self.assertRaisesRegex(c.ConformanceError, "engine unavailable"):
                audit.tracked_post_create_step(
                    "socket", "network", "network-name", "created-id", "attempt",
                    resources, attempts,
                    lambda: (_ for _ in ()).throw(c.ConformanceError("post-create inspect failed")),
                )
        self.assertEqual(attempts[0]["identity"], None)
        self.assertEqual(
            audit.cleanup_targets(resources, attempts),
            [("network", "created-id"), ("network", "network-name")],
        )

    def test_all_four_payloads_stay_blocked_on_effective_collision(self):
        for slot in range(4):
            for gids in (False, True):
                for foreign in (False, True):
                    with self.subTest(slot=slot, gids=gids, foreign=foreign), tempfile.TemporaryDirectory() as root:
                        runner = FakeRunner(root)
                        target = maps(slot, gids)[0][1]

                        def check(_runner, uid_maps, gid_maps, *, planned_pool=False):
                            if planned_pool:
                                return {"pass": True}
                            self.assertEqual(len(uid_maps), 4)
                            self.assertEqual(len(gid_maps), 4)
                            extra = {}
                            if foreign:
                                extra["gid_ranges" if gids else "uid_ranges"] = [
                                    ("caller", 100000, 1000), ("foreign", target, 1)]
                            else:
                                extra["resolve_gids" if gids else "resolve_uids"] = (
                                    lambda values: {target: "collision"} if target in values else {})
                            return authority(uid_maps, gid_maps, **extra)

                        def read(pid, kind):
                            return "\n".join(" ".join(map(str, row)) for row in maps(pid - 1, kind == "gid_map"))

                        with mock.patch.object(c, "assert_host_authority", side_effect=check), mock.patch.object(c, "read_proc", side_effect=read):
                            with self.assertRaisesRegex(c.ConformanceError, "aliases NSS|overlaps delegation"):
                                c.run_trial(runner, pathlib.Path(root), "image", "seccomp", "exact", 1, "attempt")
                        self.assertEqual(runner.released, [])
                        self.assertEqual(len(runner.created), 4)
                        self.assertEqual(runner.resources, {})
                        for mount in pathlib.Path(root).glob("reploy-mi-*"):
                            self.assertEqual(list(mount.iterdir()), [], "persistent payload data was written")
                        cleanup = json.loads((pathlib.Path(root) / "exact/iteration-1/cleanup.json").read_text())
                        self.assertTrue(cleanup["pass"])

    def test_planned_collision_prevents_resource_mutation(self):
        with tempfile.TemporaryDirectory() as root:
            runner = FakeRunner(root)
            with mock.patch.object(c, "assert_host_authority", side_effect=c.ConformanceError("planned collision")):
                with self.assertRaisesRegex(c.ConformanceError, "planned collision"):
                    c.run_trial(runner, pathlib.Path(root), "image", "seccomp", "exact", 1, "attempt")
            self.assertFalse(runner.resources)
            self.assertFalse(any("create" in command or "start" in command or "exec" in command for command in runner.commands))

    def test_primary_and_control_maps_must_be_disjoint(self):
        uid_maps = [maps(slot) for slot in range(4)]
        gid_maps = [maps(slot, True) for slot in range(4)]
        self.assertTrue(authority(uid_maps, gid_maps)["pass"])
        for slot in range(1, 4):
            with self.subTest(slot=slot):
                overlap = list(uid_maps)
                overlap[slot] = uid_maps[0]
                with self.assertRaisesRegex(c.ConformanceError, "overlap"):
                    authority(overlap, gid_maps)
        with self.assertRaisesRegex(c.ConformanceError, "matching nonempty"):
            authority([], [])
        with self.assertRaisesRegex(c.ConformanceError, "escaped"):
            authority([[(0, 1, 1)]], [maps(0, True)])

    def test_cleanup_requires_attempt_ownership(self):
        for kind in ("container", "volume", "image"):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as root:
                runner = FakeRunner(root)
                runner.resources[kind, "object"] = {"Labels": {c.OWNER_LABEL: "foreign"}}
                with self.assertRaisesRegex(c.ConformanceError, "refusing foreign"):
                    c.remove_owned_resource(runner, kind, "object", "attempt")
                self.assertIn((kind, "object"), runner.resources)
                runner.resources[kind, "object"]["Labels"][c.OWNER_LABEL] = "attempt"
                self.assertTrue(c.remove_owned_resource(runner, kind, "object", "attempt")["pass"])
                self.assertTrue(c.remove_owned_resource(runner, kind, "object", "attempt")["already_absent"])

    def test_existence_error_does_not_authorize_creation_or_cleanup(self):
        runner = mock.Mock()
        runner.run.return_value = subprocess.CompletedProcess([], 125, "", "engine unavailable")
        with self.assertRaisesRegex(c.ConformanceError, "cannot establish"):
            c.resource_exists(runner, "container", "object")
        self.assertEqual(runner.run.call_count, 1)

    def test_control_report_preserves_admission_record(self):
        nonce = "a" * 32
        runner = mock.Mock()
        runner.run.side_effect = [
            subprocess.CompletedProcess([], 0, "", ""),
            subprocess.CompletedProcess([], 0, "0\n", ""),
            subprocess.CompletedProcess([], 0, f'admission-ready {nonce}\n{{"pass":true}}\n', ""),
        ]
        self.assertEqual(c.control_report(runner, "object", nonce), {"pass": True})
        runner.run.side_effect = [subprocess.CompletedProcess([], 0, "", ""), subprocess.CompletedProcess([], 0, "1", "")]
        with self.assertRaisesRegex(c.ConformanceError, "control payload failed"):
            c.control_report(runner, "object", nonce)


class PeerMountVisibilityTests(unittest.TestCase):
    @staticmethod
    def snapshot(root, device="1:2"):
        state = {"device": device, "root": root, "target": "/probe-state"}
        return {"selected": {"/probe-state":
                f"9 2 {device} {root} /probe-state rw - ext4 /dev/test rw"},
                "mounts": [state]}

    def test_distinct_owned_mounts_pass_both_controls(self):
        result = c.assert_peer_mount_visibility([
            self.snapshot("/volumes/a"), self.snapshot("/volumes/b")])
        self.assertTrue(result["pass"])
        self.assertEqual(result["peer_mounts_absent"], [True, True])

    def test_same_mount_source_is_rejected(self):
        with self.assertRaisesRegex(c.ConformanceError, "share the state mount"):
            c.assert_peer_mount_visibility([
                self.snapshot("/volumes/a"), self.snapshot("/volumes/a")])

    def test_peer_mount_at_any_target_is_rejected(self):
        for root in ("/volumes/a", "/volumes/a/child", "/volumes", "/"):
            with self.subTest(root=root):
                snapshots = [self.snapshot("/volumes/a"), self.snapshot("/volumes/b")]
                snapshots[1]["mounts"].append(
                    {"device": "1:2", "root": root, "target": "/hidden-peer"})
                with self.assertRaisesRegex(c.ConformanceError, "peer state mount is visible"):
                    c.assert_peer_mount_visibility(snapshots)

    def test_root_name_on_another_device_is_not_a_peer_mount(self):
        snapshots = [self.snapshot("/volumes/a"), self.snapshot("/volumes/b")]
        snapshots[1]["mounts"].append(
            {"device": "8:9", "root": "/volumes/a", "target": "/unrelated"})
        self.assertTrue(c.assert_peer_mount_visibility(snapshots)["pass"])

    def test_missing_positive_control_is_rejected(self):
        snapshots = [self.snapshot("/volumes/a"), self.snapshot("/volumes/b")]
        snapshots[0]["mounts"] = []
        with self.assertRaisesRegex(c.ConformanceError, "positive control failed"):
            c.assert_peer_mount_visibility(snapshots)

    def test_snapshot_keeps_every_mount_record(self):
        records = [f"9 2 1:2 /volume{index} {target} rw - ext4 /dev/test rw"
                   for index, target in enumerate(
                       ("/", "/dev/shm", "/dev/mqueue", "/probe-state", "/extra"))]
        with mock.patch.object(c, "read_proc", return_value="\n".join(records)):
            result = c.mount_snapshot(42)
        self.assertEqual(len(result["mounts"]), 5)
        self.assertEqual(result["mounts"][-1]["target"], "/extra")
        with mock.patch.object(c, "read_proc", return_value="malformed"):
            with self.assertRaisesRegex(c.ConformanceError, "invalid mountinfo"):
                c.mount_snapshot(42)



class AuditCleanupFailureEvidenceTests(unittest.TestCase):
    def run_failed_audit(self, root, failure):
        output = pathlib.Path(root) / "audit"
        runner, service = mock.Mock(), mock.Mock()
        service.poll.return_value = None
        service.wait.return_value = 0
        baseline = {"containers": [], "images": [], "volumes": [], "networks": []}
        inventories = [baseline, baseline]
        if failure == "final-inventory":
            inventories[1] = c.ConformanceError("final inventory unavailable")
        if failure == "initial-inventory":
            inventories[0] = c.ConformanceError("initial inventory unavailable")
        if failure == "service-terminate":
            service.terminate.side_effect = OSError("service terminate failed")
        if failure == "service-wait":
            service.wait.side_effect = OSError("service wait failed")
        if failure == "service-first-wait":
            service.wait.side_effect = [OSError("service wait failed"), 0]
        if failure == "service-already-exited":
            service.terminate.side_effect = ProcessLookupError("service already exited")
            service.poll.return_value = 0
        if failure == "service-kill":
            service.wait.side_effect = subprocess.TimeoutExpired("service", 10)
            service.kill.side_effect = OSError("service kill failed")

        def start_service(argv, **_kwargs):
            pathlib.Path(argv[-1].removeprefix("unix://")).touch()
            return service

        runner.save_commands.side_effect = lambda: c.write_json(output / "commands.json", [])
        with contextlib.ExitStack() as stack:
            stack.enter_context(mock.patch.object(sys, "argv", ["audit", "--output-dir", str(output)]))
            for name, kwargs in (
                ("Runner", {"return_value": runner}),
                ("inventory", {"side_effect": inventories}),
                ("assert_host_authority", {"return_value": {
                    "uid_host_ids": [(100000, 65536)], "gid_host_ids": [(100000, 65536)]}}),
                ("resource_exists", {"return_value": False}),
                ("build_image", {"return_value": {"inspect": [{"Id": "fixture"}]}}),
            ):
                stack.enter_context(mock.patch.object(c, name, **kwargs))
            remove = stack.enter_context(mock.patch.object(c, "remove_owned_resource",
                                                          return_value={"pass": True}))
            stack.enter_context(mock.patch.object(audit.subprocess, "Popen", side_effect=start_service))
            stack.enter_context(mock.patch.object(audit, "observe",
                                                  side_effect=c.ConformanceError("original audit failure")))
            self.assertEqual(audit.main(), 1)
        runner.save_commands.assert_called_once()
        self.assertTrue((output / "commands.json").is_file())
        self.assertEqual((output / "podman.sock").exists(),
                         failure in ("service-wait", "service-kill"))
        self.assertEqual(remove.call_count, 0 if failure == "initial-inventory" else 1)
        summary = json.loads((output / "summary.json").read_text())
        if failure in ("service-terminate", "service-first-wait"):
            service.kill.assert_called_once()
            self.assertTrue(summary["service_stopped"])
        if failure == "service-already-exited":
            service.kill.assert_not_called()
            service.wait.assert_called_once_with(timeout=10)
            self.assertTrue(summary["service_stopped"])
        return summary

    def test_audit_keeps_error_and_receipts_when_final_inventory_fails(self):
        with tempfile.TemporaryDirectory() as root:
            summary = self.run_failed_audit(root, "final-inventory")
        self.assertIn("original audit failure", summary["error"])
        self.assertIn("final inventory unavailable", summary["inventory_error"])
        self.assertIsNone(summary["inventory_after"])
        self.assertFalse(summary["engine_restored"])

    def test_audit_continues_image_cleanup_and_receipts_when_service_stop_fails(self):
        for failure in ("service-wait", "service-kill"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as root:
                summary = self.run_failed_audit(root, failure)
            self.assertIn("original audit failure", summary["error"])
            self.assertFalse(summary["service_stopped"])
            self.assertTrue(any(not item["pass"] for item in summary["cleanup"]))
            self.assertEqual(summary["inventory_before"], summary["inventory_after"])
            self.assertTrue(any(item.get("socket_retained") for item in summary["cleanup"]))

    def test_audit_recovers_owned_service_after_terminate_or_wait_error(self):
        for failure in ("service-terminate", "service-first-wait", "service-already-exited"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as root:
                summary = self.run_failed_audit(root, failure)
            self.assertIn("original audit failure", summary["error"])
            self.assertTrue(any(not item["pass"] for item in summary["cleanup"]))

    def test_audit_retains_preflight_failure_without_claiming_restoration(self):
        with tempfile.TemporaryDirectory() as root:
            summary = self.run_failed_audit(root, "initial-inventory")
        self.assertIn("initial inventory unavailable", summary["error"])
        self.assertIsNone(summary["inventory_before"])
        self.assertFalse(summary["engine_restored"])


class CleanupFailureEvidenceTests(unittest.TestCase):
    def test_trial_preserves_original_error_when_inventory_fails(self):
        runner = mock.Mock()
        def inventory_command(args, **kwargs):
            if args[1] == "ps":
                raise c.ConformanceError("container inventory unavailable")
            return SimpleNamespace(stdout="", returncode=0)
        runner.run.side_effect = inventory_command
        with tempfile.TemporaryDirectory() as root:
            output = pathlib.Path(root)
            with mock.patch.object(c, "assert_host_authority",
                                   side_effect=c.ConformanceError("original admission failure")):
                with self.assertRaisesRegex(c.ConformanceError, "original admission failure"):
                    c.run_trial(runner, output, "image", "seccomp", "exact", 1, "attempt")
            cleanup = json.loads((output / "exact/iteration-1/cleanup.json").read_text())
        self.assertFalse(cleanup["pass"])
        self.assertIsNone(cleanup["remaining_containers"])
        self.assertIn("container inventory unavailable", cleanup["inventory_errors"]["remaining_containers"])
        self.assertEqual(cleanup["remaining_volumes"], [])
        self.assertEqual(cleanup["remaining_networks"], [])

    def run_failed_main(self, root, *, final_inventory_fails=False,
                        image_cleanup_passes=True):
        output = pathlib.Path(root) / "evidence"
        args = SimpleNamespace(profiles="exact", iterations=1, output_dir=output)
        baseline = {"containers": [], "images": [], "volumes": [], "networks": []}
        final = c.ConformanceError("final inventory unavailable") if final_inventory_fails else baseline
        runner = mock.Mock()
        with contextlib.ExitStack() as stack:
            for name, kwargs in (
                ("parse_args", {"return_value": args}),
                ("Runner", {"return_value": runner}),
                ("inventory", {"side_effect": [baseline, final]}),
                ("host_context", {"return_value": {"seccomp_profile": {"path": "seccomp"}}}),
                ("docker_negative", {"return_value": {"pass": True}}),
                ("assert_host_authority", {"return_value": {"pass": True}}),
                ("resource_exists", {"return_value": False}),
                ("build_image", {"return_value": {"fixture": True}}),
                ("run_trial", {"side_effect": c.ConformanceError("original trial failure")}),
                ("remove_owned_resource", {"return_value": {"pass": image_cleanup_passes,
                                                              "error": "image removal failed"}}),
            ):
                stack.enter_context(mock.patch.object(c, name, **kwargs))
            stack.enter_context(contextlib.redirect_stdout(io.StringIO()))
            with self.assertRaises(c.ConformanceError) as caught:
                c.main()
        runner.save_commands.assert_called_once()
        return json.loads((output / "summary.json").read_text()), str(caught.exception)

    def test_main_keeps_original_error_when_image_cleanup_also_fails(self):
        with tempfile.TemporaryDirectory() as root:
            summary, error = self.run_failed_main(root, image_cleanup_passes=False)
        self.assertFalse(summary["pass"])
        self.assertIn("original trial failure", summary["error"])
        self.assertIn("original trial failure", error)
        self.assertIn("attempt-owned image cleanup failed", error)
        self.assertFalse(summary["image_cleanup"]["pass"])

    def test_main_keeps_original_error_and_receipt_when_final_inventory_fails(self):
        with tempfile.TemporaryDirectory() as root:
            summary, error = self.run_failed_main(root, final_inventory_fails=True)
        self.assertFalse(summary["pass"])
        self.assertFalse(summary["engine_restored"])
        self.assertIsNone(summary["podman_inventory_after"])
        self.assertIn("original trial failure", error)
        self.assertIn("final inventory unavailable", summary["inventory_error"])


if __name__ == "__main__":
    unittest.main()
