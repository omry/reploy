#!/usr/bin/env python3
"""Disposable F1.0 filesystem mechanisms, not public mapped qualification.

Run as the non-root owner of a Linux Podman engine. All resources use a new
private store. The supplied binary is built from f10_filesystem_payload.go.txt;
its expected digest must come from the retained build receipt. No host files,
default engine store, host services or account configuration are changed.
"""

from __future__ import annotations

import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import secrets
import select
import shutil
import signal
import stat
import subprocess
import tarfile
import tempfile
import time

import operation_audit as audit


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


class SourceBindingRefused(RuntimeError):
    """The retained source object no longer matches the protected binding."""


def source_snapshot(path):
    """Read a source through a non-following descriptor and retain its identity."""
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        st = os.fstat(fd)
        data = os.read(fd, 16 << 20)
        if os.read(fd, 1):
            raise RuntimeError("source fixture exceeds observation limit")
    finally:
        os.close(fd)
    return {"device": st.st_dev, "inode": st.st_ino, "size": st.st_size,
            "sha256": hashlib.sha256(data).hexdigest()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--expected-sha256", required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--podman", default="/usr/bin/podman")
    parser.add_argument("--runtime", default="/usr/bin/crun")
    args = parser.parse_args()
    args.binary = args.binary.absolute()
    if os.geteuid() == 0:
        parser.error("run as a non-root engine owner")
    args.output_dir.mkdir(mode=0o700)
    expected = args.expected_sha256
    if len(expected) != 64 or any(c not in "0123456789abcdef" for c in expected):
        parser.error("expected digest must be a lowercase SHA-256")
    if args.binary.is_symlink() or sha(args.binary) != expected:
        parser.error("fixture bytes do not match the supplied build digest")
    work = Path(tempfile.mkdtemp(prefix="reploy-f10-"))
    token = secrets.token_hex(12)
    env = dict(os.environ)
    for name, key in [("runtime", "XDG_RUNTIME_DIR"), ("config", "XDG_CONFIG_HOME"),
                      ("data", "XDG_DATA_HOME")]:
        (work / name).mkdir(mode=0o700)
        env[key] = str(work / name)
    prefix = [args.podman, "--remote=false", "--runtime", args.runtime,
              "--root", str(work / "graph"), "--runroot", str(work / "run"),
              "--storage-driver", "vfs", "--cgroup-manager", "cgroupfs",
              "--events-backend", "file"]
    socket_path = work / "api.sock"
    label = "reploy.f10.attempt"
    result = {"scope": "F1.0 disposable filesystem mechanisms; no public launch or F provider qualification",
              "attempt": token, "work": str(work), "cases": [], "cleanup": [],
              "binary_sha256": expected, "passed": False,
              "limits": ["No application admission, generation binding or production provider is claimed",
                         "Engine identity and actual kernel maps are recorded; this does not qualify another engine build",
                         "Host root or engine-owner interference after admission is outside the integrity guarantee"]}
    commands, containers = [], []
    server, image, log = None, None, None
    started = time.monotonic()

    def run(argv, check=True, timeout=60):
        p = subprocess.run(argv, cwd=work, env=env, capture_output=True, text=True, timeout=timeout)
        commands.append({"argv": argv, "exit_code": p.returncode,
                         "stdout": p.stdout, "stderr": p.stderr})
        if check and p.returncode:
            raise RuntimeError(f"command failed: {argv[:3]}: {p.stderr[-1500:]}")
        return p

    def api(method, route, body=None):
        return audit.api(socket_path, method, route, body)

    def inventory():
        return {kind: sorted(run(prefix + command).stdout.splitlines()) for kind, command in {
            "containers": ["ps", "-aq"], "images": ["images", "-aq"],
            "volumes": ["volume", "ls", "-q"], "networks": ["network", "ls", "-q"],
        }.items()}

    def logs(identity):
        # Native logs are an independent fixture observation, not API approval.
        p = run(prefix + ["logs", identity])
        return [json.loads(line) for line in p.stdout.splitlines() if line.startswith("{")]

    def wait_phase(identity, phase):
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            rows = logs(identity)
            if any(x.get("phase") == phase for x in rows):
                return rows
            time.sleep(0.05)
        raise TimeoutError("fixture phase " + phase)

    def stop(identity):
        obj = api("GET", f"/containers/{identity}/json")
        if obj["Config"]["Labels"].get(label) != token or obj["Id"] != identity:
            raise RuntimeError("refusing foreign container cleanup")
        api("DELETE", f"/containers/{identity}?force=true")
        result["cleanup"].append({"container": identity, "removed": True})

    def checked_effect(stage, source_path, expected_source, effect, bound=None,
                       check_source=True):
        """Check the retained source and protected bind before invoking an effect."""
        try:
            current = source_snapshot(source_path)
        except OSError as exc:
            raise SourceBindingRefused(f"{stage}: source observation failed") from exc
        if check_source:
            source_keys = ("device", "inode", "size", "sha256")
            if any(current[key] != expected_source[key] for key in source_keys):
                raise SourceBindingRefused(f"{stage}: source object/hash changed")
            if bound is not None and any(bound[key] != expected_source[key]
                                         for key in ("device", "inode", "sha256")):
                raise SourceBindingRefused(f"{stage}: protected bind changed")
        return {"source": current, "bound": bound, "effect": effect()}

    def expect_refusal(action):
        """Negative oracle: stale source must refuse before the supplied effect."""
        try:
            action()
        except SourceBindingRefused as exc:
            return {"refused": True, "error": str(exc)}
        raise AssertionError("negative oracle accepted a stale source")

    try:
        info = json.loads(run(prefix + ["info", "--format", "json"]).stdout)
        assert info["host"]["security"]["rootless"] is True
        result["initial_inventory"] = inventory()
        result["engine"] = {"version": info["version"], "kernel": info["host"]["kernel"],
                            "runtime": info["host"]["ociRuntime"], "rootless": True,
                            "binary_sha256": sha(args.podman), "runtime_sha256": sha(args.runtime)}
        # Descriptor/mount crossing proof runs in a disposable private mount ns.
        host = run(prefix + ["unshare", "unshare", "--mount", "--propagation", "private",
                             str(args.binary), "host"])
        result["descriptor_checks"] = json.loads(host.stdout)
        assert all(result["descriptor_checks"]["checks"].values())
        retained = work / "retained"
        retained.mkdir(mode=0o700)
        original = work / "original"
        shutil.copyfile(args.binary, original)
        artifact = retained / "probe"
        shutil.copyfile(original, artifact)
        artifact.chmod(0o555)
        assert sha(artifact) == expected
        expected_source = source_snapshot(artifact)
        # Changes to an independent original do not change retained bytes.
        original.write_bytes(b"changed original")
        assert sha(artifact) == expected
        result["protected_capture_independent"] = True
        archive = work / "rootfs.tar"
        with tarfile.open(archive, "w") as tar:
            for directory in [".", "data", "empty", "only", "only/child", "proc", "tmp"]:
                entry = tarfile.TarInfo(directory)
                entry.type, entry.mode = tarfile.DIRTYPE, 0o755
                tar.addfile(entry)
            payload = artifact.read_bytes()
            entry = tarfile.TarInfo("fixture")
            entry.mode, entry.size = 0o555, len(payload)
            tar.addfile(entry, io.BytesIO(payload))
        image = run(prefix + ["import", "--change", f"LABEL {label}={token}",
                              str(archive), "localhost/reploy-f10:" + token]).stdout.strip()
        log = (args.output_dir / "api.log").open("wb")
        server = subprocess.Popen(prefix + ["system", "service", "--time=0", "unix://" + str(socket_path)],
                                  cwd=work, env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT)
        deadline = time.monotonic() + 15
        while not socket_path.exists():
            if server.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError("private compatible API unavailable")
            time.sleep(0.05)
        assert stat.S_ISSOCK(socket_path.stat().st_mode)
        result["compatible_version"] = api("GET", "/version")
        # Positive root policies and all accepted empty/directory/absent targets.
        for profile in ["exact", "bounded"]:
            span = 1 if profile == "exact" else 4096
            namespace = f"auto:size=1,uidmapping=0:1:{span},gidmapping=0:1:{span}"
            if profile == "exact":
                namespace += ",gidmapping=5:2:1"  # devpts runtime group, as in the C fixture.
            for mode, target in [("readonly", "/empty"), ("readonly", "/only"),
                                 ("readonly", "/absent"), ("writable", "/data")]:
                case = f"{profile}-{mode}-{target[1:]}"
                data = work / case
                data.mkdir(mode=0o777)
                data.chmod(0o777)
                body = {"Image": image, "Entrypoint": ["/reploy-probe"],
                        "Cmd": ["container", mode, target], "User": "0:0", "Labels": {label: token},
                        "HostConfig": {"UsernsMode": namespace, "NetworkMode": "none",
                                       "ReadonlyRootfs": mode == "readonly", "CapDrop": ["ALL"],
                                       "SecurityOpt": ["no-new-privileges"], "RestartPolicy": {"Name": "no"},
                                       "Binds": [f"{artifact}:/reploy-probe:ro", f"{data}:/data:rw"]}}
                # A second bind proves each target on the actual engine.
                if target != "/data":
                    body["HostConfig"]["Binds"].append(f"{data}:{target}:rw")
                name = "reploy-f10-" + token + "-" + case

                def prepare():
                    created = checked_effect(
                        "pre-create", artifact, expected_source,
                        lambda: api("POST", "/containers/create?name=" + name, body))
                    identity = created["effect"]["Id"]
                    containers.append(identity)
                    api("POST", f"/containers/{identity}/start")
                    return identity

                # Exercise the same checked preparation path with a replaced source.
                if "source_substitution_refused_before_create" not in result:
                    before_inventory = inventory()
                    pinned = retained / "original-probe"
                    artifact.rename(pinned)
                    try:
                        retained_source = source_snapshot(pinned)
                        assert retained_source == expected_source
                        artifact.write_bytes(b"substitute")
                        stale_source = source_snapshot(artifact)
                        assert (stale_source["device"], stale_source["inode"]) != \
                            (expected_source["device"], expected_source["inode"])
                        assert stale_source["sha256"] != expected
                        try:
                            prepare()
                        except SourceBindingRefused as exc:
                            after_inventory = inventory()
                            assert after_inventory == before_inventory
                            assert not containers
                            precreate = {
                                "refused": True,
                                "effect": False,
                                "error": str(exc),
                                "retained_source": retained_source,
                                "stale_source": stale_source,
                                "inventory_unchanged": True,
                            }
                        else:
                            raise AssertionError("pre-create source substitution was accepted")

                        # Deliberately omit the source check. The oracle must fail
                        # closed rather than record a refusal for this mutation.
                        control_effects = []
                        try:
                            expect_refusal(lambda: checked_effect(
                                "pre-create-skipped-check", artifact, expected_source,
                                lambda: control_effects.append("invoked"),
                                check_source=False))
                        except AssertionError as exc:
                            assert control_effects == ["invoked"]
                            precreate["skipped_source_check_control"] = {
                                "negative_oracle_failed": True,
                                "effect_invoked": True,
                                "error": str(exc),
                            }
                        else:
                            raise AssertionError("skipped source check did not fail negative oracle")
                    finally:
                        artifact.unlink(missing_ok=True)
                        pinned.rename(artifact)
                    assert source_snapshot(artifact) == expected_source
                    result["source_substitution_refused_before_create"] = precreate

                identity = prepare()
                rows = wait_phase(identity, "inhibited")
                prepared = rows[-1]
                assert prepared["sha256"] == expected
                # Kernel evidence, independent of requested engine flags.
                observed = api("GET", f"/containers/{identity}/json")
                pid = observed["State"]["Pid"]
                assert isinstance(pid, int) and pid > 0 and observed["State"]["Running"]
                # Read from the guest's initial user namespace. The container's
                # own uid_map describes its parent namespace, not host IDs.
                proc = Path("/proc") / str(pid)
                host_uid_map = (proc / "uid_map").read_text()
                host_gid_map = (proc / "gid_map").read_text()
                maps = [list(map(int, line.split())) for line in host_uid_map.splitlines()]
                assert len(maps) == 1 and maps[0][0] == 0 and maps[0][2] == span
                assert maps[0][1] not in [0, os.getuid()]
                gids = [list(map(int, line.split())) for line in host_gid_map.splitlines()]
                gid_base = gids[0][1]
                assert gid_base not in [0, os.getgid()]
                assert gids == ([[0, gid_base, 1], [5, gid_base + 1, 1]]
                                if profile == "exact" else [[0, gid_base, span]])
                mounts = [line.split() for line in prepared["mountinfo"].splitlines()]
                protected = [m for m in mounts if m[4] == "/reploy-probe"]
                assert len(protected) == 1 and "ro" in protected[0][5].split(",")
                roots = [m for m in mounts if m[4] == "/"]
                assert len(roots) == 1 and ("ro" if mode == "readonly" else "rw") in roots[0][5].split(",")
                assert any(m[4] == target and "rw" in m[5].split(",") for m in mounts)
                assert "CapEff:\t0000000000000000" in prepared["status"]
                assert "NoNewPrivs:\t1" in prepared["status"]
                def observe():
                    return json.loads(run(prefix + ["unshare", str(args.binary), "observe", str(pid)]).stdout)
                observer = observe()
                source = artifact.stat()
                assert observer["sha256"] == expected and observer["pinned_process_alive"]
                assert (observer["device"], observer["inode"]) == (source.st_dev, source.st_ino)
                host_mounts = [line.split() for line in observer["mountinfo"].splitlines()]
                assert any(m[4] == "/reploy-probe" and "ro" in m[5].split(",") for m in host_mounts)
                substitution = None
                if mode == "writable":
                    pinned = retained / "pinned-probe"
                    artifact.rename(pinned)
                    try:
                        retained_source = source_snapshot(pinned)
                        assert retained_source == expected_source
                        artifact.write_bytes(b"SUBSTITUTE")
                        stale_source = source_snapshot(artifact)
                        still_bound = observe()
                        # The bind retains the original inode; name substitution
                        # cannot authorize release from a stale host observation.
                        assert still_bound["sha256"] == expected
                        assert still_bound["inode"] == source.st_ino
                        assert stale_source["inode"] != source.st_ino and stale_source["sha256"] != expected
                        markers_before = {name: (data / name).exists()
                                          for name in ["release", "payload", "target-payload"]}
                        assert not any(markers_before.values())
                        try:
                            checked_effect(
                                "pre-release", artifact, expected_source,
                                lambda: (data / "release").write_bytes(b"fixture release"),
                                bound=still_bound)
                        except SourceBindingRefused as exc:
                            after_bind = observe()
                            refusal_rows = logs(identity)
                            markers_after = {name: (data / name).exists()
                                             for name in ["release", "payload", "target-payload"]}
                            assert markers_after == markers_before
                            assert after_bind["pinned_process_alive"]
                            assert after_bind["sha256"] == expected
                            assert any(x.get("phase") == "inhibited" for x in refusal_rows)
                            assert not any(x.get("phase") == "completed" for x in refusal_rows)
                            substitution = {
                                "refused_before_release": True,
                                "effect": False,
                                "error": str(exc),
                                "retained_source": retained_source,
                                "stale_source": stale_source,
                                "bound_original": still_bound,
                                "post_refusal_bind": after_bind,
                                "markers_before": markers_before,
                                "markers_after": markers_after,
                                "process_remained_inhibited": True,
                            }
                        else:
                            raise AssertionError("post-bind source substitution was accepted")
                    finally:
                        artifact.unlink(missing_ok=True)
                        pinned.rename(artifact)
                    assert source_snapshot(artifact) == expected_source

                release_observation = checked_effect(
                    "pre-release", artifact, expected_source,
                    lambda: (data / "release").write_bytes(b"fixture release"),
                    bound=observe())
                rows = wait_phase(identity, "completed")
                assert all(rows[-1]["checks"].values()) and rows[-1]["sha256"] == expected
                waited = api("POST", f"/containers/{identity}/wait?condition=not-running")
                assert waited["StatusCode"] == 0
                assert run(prefix + ["unshare", "cat", str(data / "payload")]).stdout == "managed"
                assert run(prefix + ["unshare", "cat", str(data / "target-payload")]).stdout == "target"
                result["cases"].append({"case": case, "container": identity, "observations": rows,
                                        "host_pid": pid, "host_uid_map": host_uid_map,
                                        "host_gid_map": host_gid_map,
                                        "host_observer": observer, "source_substitution": substitution,
                                        "release_observation": release_observation,
                                        "exit_status": waited["StatusCode"], "passed": True})
                stop(identity)
                containers.remove(identity)
        result["passed"] = len(result["cases"]) == 8
    except Exception as exc:
        result["error"] = repr(exc)
    finally:
        for identity in containers:
            try:
                stop(identity)
            except Exception as exc:
                result["cleanup"].append({"container": identity, "error": repr(exc)})
                result["passed"] = False
        if server is not None:
            server.terminate()
            try:
                server.wait(timeout=8)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait(timeout=5)
            result["api_stopped"] = server.poll() is not None
        if log is not None:
            log.close()
        if image:
            removed = run(prefix + ["rmi", image], check=False)
            result["cleanup"].append({"image": image, "removed": removed.returncode == 0})
            result["passed"] = result["passed"] and removed.returncode == 0
        try:
            result["final_inventory"] = inventory()
            assert result["final_inventory"] == result["initial_inventory"]
            result["inventory_restored"] = True
            assert work.resolve() == work and work.parent == Path(tempfile.gettempdir()).resolve()
            run(prefix + ["unshare", "rm", "-rf", "--", str(work)])
            helpers = []
            for proc in Path("/proc").iterdir():
                if not proc.name.isdecimal():
                    continue
                try:
                    values = (proc / "environ").read_bytes().split(b"\0")
                    if ("XDG_RUNTIME_DIR=" + str(work / "runtime")).encode() not in values:
                        continue
                    argv = (proc / "cmdline").read_bytes().split(b"\0")
                    assert argv[0].endswith(b"/catatonit") and argv[1:2] == [b"-P"]
                    assert proc.stat().st_uid == os.getuid()
                    start = (proc / "stat").read_text().rsplit(") ", 1)[1].split()[19]
                    pinned = os.pidfd_open(int(proc.name))
                    try:
                        assert (proc / "stat").read_text().rsplit(") ", 1)[1].split()[19] == start
                        signal.pidfd_send_signal(pinned, signal.SIGTERM)
                        assert select.select([pinned], [], [], 5)[0]
                    finally:
                        os.close(pinned)
                    helpers.append({"pid": int(proc.name), "start": start, "exit_observed": True})
                except (FileNotFoundError, ProcessLookupError, PermissionError):
                    continue
            result["private_helpers_retired"] = helpers
            if work.exists():
                shutil.rmtree(work)
            result["private_root_removed"] = not work.exists()
        except Exception as exc:
            result["cleanup_error"] = repr(exc)
            result["passed"] = False
        result["elapsed_seconds"] = time.monotonic() - started
        result["sources"] = {str(p): sha(p) for p in [Path(__file__), Path(audit.__file__)]}
        (args.output_dir / "commands.json").write_text(json.dumps(commands, indent=2) + "\n")
        (args.output_dir / "report.json").write_text(json.dumps(result, indent=2) + "\n")
        print(json.dumps({"passed": result["passed"], "cases": len(result["cases"]),
                          "error": result.get("error"), "work": str(work)}))
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
