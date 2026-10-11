"""Private admission/cleanup regressions; the live build-failure test is opt-in."""

import json
import os
import pathlib
import secrets
import shutil
import subprocess
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


if __name__ == "__main__":
    unittest.main()
