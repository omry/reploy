#!/usr/bin/env python3
"""Run the private multi-identity Podman conformance probe.

This harness deliberately operates below Reploy's public blueprint surface. It
creates only uniquely named disposable Podman resources, captures evidence,
and removes those exact resources in a finally block.
"""

from __future__ import annotations

import argparse
import dataclasses
import datetime as dt
import getpass
import grp
import hashlib
import json
import os
import pathlib
import pwd
import secrets
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
from typing import Any, Iterable


ROOT = pathlib.Path(__file__).resolve().parents[3]
HERE = pathlib.Path(__file__).resolve().parent
PROBE_PACKAGE = "./tools/conformance/multiidentity/probe"
SUPERVISOR_UID = 100
SUPERVISOR_GID = 100
DECLARED_UID = 1001
DECLARED_GID = 2001
DECLARED_GROUPS = (3001, 3002)
RUNTIME_GID = 5
EXPECTED_SUPERVISOR_CAPS = 0x1C0  # SETGID, SETUID, SETPCAP


class ConformanceError(RuntimeError):
    pass


@dataclasses.dataclass
class Runner:
    output: pathlib.Path
    commands: list[str] = dataclasses.field(default_factory=list)

    def run(
        self,
        argv: Iterable[str | os.PathLike[str]],
        *,
        check: bool = True,
        env: dict[str, str] | None = None,
        timeout: float = 120,
    ) -> subprocess.CompletedProcess[str]:
        args = [os.fspath(value) for value in argv]
        self.commands.append(shlex.join(args))
        result = subprocess.run(
            args,
            cwd=ROOT,
            env={**os.environ, **(env or {})},
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=timeout,
            check=False,
        )
        if check and result.returncode != 0:
            raise ConformanceError(
                f"command failed ({result.returncode}): {shlex.join(args)}\n"
                f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}"
            )
        return result

    def json(self, argv: Iterable[str | os.PathLike[str]], **kwargs: Any) -> Any:
        result = self.run(argv, **kwargs)
        try:
            return json.loads(result.stdout)
        except json.JSONDecodeError as exc:
            raise ConformanceError(
                f"invalid JSON from {self.commands[-1]}: {result.stdout!r}"
            ) from exc

    def save_commands(self) -> None:
        (self.output / "commands.txt").write_text(
            "\n".join(self.commands) + "\n", encoding="utf-8"
        )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--output-dir",
        type=pathlib.Path,
        default=None,
        help="evidence directory (default: a new directory under /tmp)",
    )
    parser.add_argument("--iterations", type=int, default=3)
    parser.add_argument(
        "--profiles",
        default="exact,bounded",
        help="comma-separated subset of exact,bounded",
    )
    return parser.parse_args()


def sha256_file(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def source_hash() -> str:
    digest = hashlib.sha256()
    paths = [
        HERE / "conformance.py",
        HERE / "Containerfile",
        HERE / "probe" / "main.go",
    ]
    for path in paths:
        digest.update(path.relative_to(ROOT).as_posix().encode())
        digest.update(b"\0")
        digest.update(path.read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def write_json(path: pathlib.Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def inventory(runner: Runner, runtime: str) -> dict[str, list[str]]:
    if runtime == "podman":
        commands = {
            "containers": ["podman", "ps", "-aq"],
            "images": ["podman", "images", "-aq"],
            "volumes": ["podman", "volume", "ls", "-q"],
        }
    else:
        commands = {
            "containers": ["docker", "ps", "-aq"],
            "images": ["docker", "images", "-q", "--no-trunc"],
            "volumes": ["docker", "volume", "ls", "-q"],
        }
    return {
        name: sorted(filter(None, runner.run(command).stdout.splitlines()))
        for name, command in commands.items()
    }


def docker_negative(runner: Runner) -> dict[str, Any]:
    before = inventory(runner, "docker")
    info = runner.json(["docker", "info", "--format", "{{json .}}"])
    version = runner.json(["docker", "version", "--format", "{{json .Server}}"])
    components = json.dumps(version.get("Components", []))
    is_podman = "Podman" in components or "podman" in str(info.get("Name", "")).lower()
    if is_podman:
        raise ConformanceError("default Docker endpoint unexpectedly identifies as Podman")
    # The backend gate rejects here. No mapping request or mutation command is sent.
    after = inventory(runner, "docker")
    if before != after:
        raise ConformanceError("Docker inventory changed during pre-mutation rejection")
    return {
        "pass": True,
        "decision": "rejected_before_mutation",
        "reason": "server is Docker Engine, not Podman",
        "server": {
            "name": info.get("Name"),
            "operating_system": info.get("OperatingSystem"),
            "server_version": info.get("ServerVersion"),
            "components": version.get("Components"),
        },
        "before": before,
        "after": after,
        "mapping_request_sent": False,
    }


def current_head(runner: Runner) -> str:
    return runner.run(
        ["sl", "log", "-r", ".", "-T", "{node}"], env={"CHGDISABLE": "1"}
    ).stdout.strip()


def host_context(runner: Runner) -> dict[str, Any]:
    podman_version = runner.json(["podman", "version", "--format", "json"])
    podman_info = runner.json(["podman", "info", "--format", "json"])
    security = podman_info.get("host", {}).get("security", {})
    if not security.get("rootless"):
        raise ConformanceError("the selected Podman engine is not rootless")
    if not security.get("seccompEnabled"):
        raise ConformanceError("the selected Podman engine has no seccomp support")
    seccomp_path = pathlib.Path(str(security.get("seccompProfilePath", "")))
    if not seccomp_path.is_file():
        raise ConformanceError(f"Podman seccomp profile is missing: {seccomp_path}")
    username = getpass.getuser()
    subuid = pathlib.Path("/etc/subuid").read_text(encoding="utf-8")
    subgid = pathlib.Path("/etc/subgid").read_text(encoding="utf-8")
    uname = os.uname()
    return {
        "reploy_head_before_evidence": current_head(runner),
        "probe_source_sha256": source_hash(),
        "host": {
            "uname": dict(
                zip(
                    ("sysname", "nodename", "release", "version", "machine"),
                    uname,
                    strict=True,
                )
            ),
            "uid": os.getuid(),
            "gid": os.getgid(),
            "username": username,
            "subuid": subuid.splitlines(),
            "subgid": subgid.splitlines(),
        },
        "podman_version": podman_version,
        "podman_host": podman_info.get("host"),
        "podman_store": podman_info.get("store"),
        "seccomp_profile": {
            "path": str(seccomp_path),
            "sha256": sha256_file(seccomp_path),
        },
        "runtime_required_identities": [
            {
                "kind": "gid",
                "container_id": RUNTIME_GID,
                "reason": "the rootless OCI devpts mount requires container GID 5",
            }
        ],
        "supervisor_allowlist": ["CAP_SETGID", "CAP_SETUID", "CAP_SETPCAP"],
        "supervisor_setpcap_reason": (
            "the trusted launcher needs CAP_SETPCAP to empty each child bounding set; "
            "the container bounding set contains no other capability"
        ),
    }


def build_image(runner: Runner, output: pathlib.Path, tag: str) -> dict[str, Any]:
    staging = output / "build-context"
    staging.mkdir()
    probe = staging / "probe"
    env = {"GOCACHE": str(output / "go-cache"), "CGO_ENABLED": "0"}
    runner.run(
        [
            "go", "build", "-trimpath", "-buildvcs=false",
            "-o", probe, PROBE_PACKAGE,
        ],
        env=env,
    )
    shutil.copy2(HERE / "Containerfile", staging / "Containerfile")
    build = runner.run(
        [
            "podman", "build", "--network=none", "--pull=never",
            "--file", staging / "Containerfile", "--tag", tag, staging,
        ],
        timeout=300,
    )
    inspect = runner.json(["podman", "image", "inspect", tag])
    write_json(output / "image-inspect.json", inspect)
    (output / "image-build.stdout").write_text(build.stdout, encoding="utf-8")
    (output / "image-build.stderr").write_text(build.stderr, encoding="utf-8")
    return {"tag": tag, "probe_sha256": sha256_file(probe), "inspect": inspect}


def exact_map_args(slot: int) -> list[str]:
    base = 1 + slot * 20
    args: list[str] = []
    for container_id, intermediate_id, count in (
        (0, base, 1),
        (SUPERVISOR_UID, base + 1, 1),
        (DECLARED_UID, base + 2, 1),
    ):
        args.extend(["--uidmap", f"{container_id}:{intermediate_id}:{count}"])
    for container_id, intermediate_id, count in (
        (0, base, 1),
        (RUNTIME_GID, base + 1, 1),
        (SUPERVISOR_GID, base + 2, 1),
        (DECLARED_GID, base + 3, 1),
        (DECLARED_GROUPS[0], base + 4, 2),
    ):
        args.extend(["--gidmap", f"{container_id}:{intermediate_id}:{count}"])
    return args


def mapping_args(profile: str, slot: int) -> list[str]:
    if profile == "exact":
        return exact_map_args(slot)
    return ["--userns", "auto:size=4096"]


def common_run_args(
    name: str,
    seccomp_path: str,
    profile: str,
    slot: int,
    *,
    volume: str | None = None,
    capabilities: bool = True,
) -> list[str]:
    args = [
        "podman", "run", "--name", name,
        *mapping_args(profile, slot),
        "--network", "none", "--ipc", "private", "--pid", "private",
        "--uts", "private", "--read-only",
        "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=16m",
        "--tmpfs", "/run:rw,noexec,nosuid,nodev,size=4m,mode=1777",
        "--pids-limit", "128", "--memory", "128m", "--cpus", "0.5",
        "--cap-drop", "ALL",
        "--security-opt", "no-new-privileges",
        "--security-opt", f"seccomp={seccomp_path}",
        "--user", "0:0",
    ]
    if capabilities:
        for cap in ("SETGID", "SETUID", "SETPCAP"):
            args.extend(["--cap-add", cap])
    if volume:
        args.extend(["--mount", f"type=volume,src={volume},dst=/probe-state"])
    return args


def inspect_container(runner: Runner, name: str) -> dict[str, Any]:
    value = runner.json(["podman", "inspect", name])
    if not isinstance(value, list) or len(value) != 1:
        raise ConformanceError(f"unexpected inspect response for {name}")
    return value[0]


def request_action(runner: Runner, container: str, action: str, **values: Any) -> dict[str, Any]:
    args = [
        "podman", "exec", "--user", f"{SUPERVISOR_UID}:{SUPERVISOR_GID}",
        container, "/probe", "request", action,
    ]
    args.extend(f"{key}={value}" for key, value in values.items())
    outer = runner.json(args)
    if not outer.get("pass") or outer.get("error"):
        raise ConformanceError(f"supervisor request {action} failed: {outer}")
    data = outer.get("data")
    if not isinstance(data, dict):
        raise ConformanceError(f"supervisor request {action} returned no object: {outer}")
    return {"response": outer, "data": data}


def wait_ready(runner: Runner, container: str) -> dict[str, Any]:
    last = ""
    for _ in range(100):
        result = runner.run(
            [
                "podman", "exec", "--user", f"{SUPERVISOR_UID}:{SUPERVISOR_GID}",
                container, "/probe", "request", "ready",
            ],
            check=False,
            timeout=5,
        )
        last = result.stderr or result.stdout
        if result.returncode == 0:
            value = json.loads(result.stdout)
            if value.get("pass"):
                return value
        time.sleep(0.05)
    logs = runner.run(["podman", "logs", container], check=False, timeout=5)
    inspect = runner.run(["podman", "inspect", container], check=False, timeout=5)
    raise ConformanceError(
        f"supervisor {container} did not become ready: {last}\n"
        f"logs:\n{logs.stdout}{logs.stderr}\ninspect:\n{inspect.stdout}{inspect.stderr}"
    )


def read_proc(pid: int, name: str) -> str:
    return pathlib.Path(f"/proc/{pid}/{name}").read_text(encoding="utf-8")


def parse_id_map(text: str) -> list[tuple[int, int, int]]:
    return [tuple(map(int, line.split())) for line in text.splitlines() if line.strip()]


def mapped_host_ids(mapping: list[tuple[int, int, int]]) -> set[int]:
    result: set[int] = set()
    for _, host_start, count in mapping:
        result.update(range(host_start, host_start + count))
    return result


def mapped_container_ids(mapping: list[tuple[int, int, int]]) -> set[int]:
    result: set[int] = set()
    for container_start, _, count in mapping:
        result.update(range(container_start, container_start + count))
    return result


def assert_mapping_profile(
    profile: str,
    uid_maps: list[list[tuple[int, int, int]]],
    gid_maps: list[list[tuple[int, int, int]]],
) -> dict[str, Any]:
    expected_uids = (
        {0, SUPERVISOR_UID, DECLARED_UID}
        if profile == "exact"
        else set(range(4096))
    )
    expected_gids = (
        {0, RUNTIME_GID, SUPERVISOR_GID, DECLARED_GID, *DECLARED_GROUPS}
        if profile == "exact"
        else set(range(4096))
    )
    actual_uids = [mapped_container_ids(mapping) for mapping in uid_maps]
    actual_gids = [mapped_container_ids(mapping) for mapping in gid_maps]
    for index, values in enumerate(actual_uids):
        if values != expected_uids:
            raise ConformanceError(
                f"workload {index} has unexpected {profile} UID geometry"
            )
    for index, values in enumerate(actual_gids):
        if values != expected_gids:
            raise ConformanceError(
                f"workload {index} has unexpected {profile} GID geometry"
            )
    return {
        "pass": True,
        "profile": profile,
        "container_uids": sorted(expected_uids),
        "container_gids": sorted(expected_gids),
    }


def parse_subids(path: pathlib.Path) -> list[tuple[str, int, int]]:
    values = []
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        name, start, count = line.split(":", 2)
        values.append((name, int(start), int(count)))
    return values


def assert_host_authority(
    uid_maps: list[list[tuple[int, int, int]]],
    gid_maps: list[list[tuple[int, int, int]]],
) -> dict[str, Any]:
    username = getpass.getuser()
    uid_ranges = parse_subids(pathlib.Path("/etc/subuid"))
    gid_ranges = parse_subids(pathlib.Path("/etc/subgid"))
    own_uids = [(start, count) for name, start, count in uid_ranges if name == username]
    own_gids = [(start, count) for name, start, count in gid_ranges if name == username]
    if not own_uids or not own_gids:
        raise ConformanceError(f"no subordinate ranges for {username}")
    named_uids = {entry.pw_uid for entry in pwd.getpwall()}
    named_gids = {entry.gr_gid for entry in grp.getgrall()}

    def contained(value: int, ranges: list[tuple[int, int]]) -> bool:
        return any(start <= value < start + count for start, count in ranges)

    uid_sets = [mapped_host_ids(mapping) for mapping in uid_maps]
    gid_sets = [mapped_host_ids(mapping) for mapping in gid_maps]
    for values in uid_sets:
        if not all(contained(value, own_uids) for value in values):
            raise ConformanceError(f"UID mapping escaped caller subordinate ranges: {values}")
        collisions = values & named_uids
        if collisions:
            raise ConformanceError(f"UID mapping aliases NSS principals: {collisions}")
    for values in gid_sets:
        if not all(contained(value, own_gids) for value in values):
            raise ConformanceError(f"GID mapping escaped caller subordinate ranges: {values}")
        collisions = values & named_gids
        if collisions:
            raise ConformanceError(f"GID mapping aliases NSS principals: {collisions}")
    if uid_sets[0] & uid_sets[1] or gid_sets[0] & gid_sets[1]:
        raise ConformanceError("workload mappings overlap")
    for name, start, count in uid_ranges:
        if name != username and any(start <= value < start + count for value in uid_sets[0] | uid_sets[1]):
            raise ConformanceError(f"UID mapping overlaps delegation for {name}")
    for name, start, count in gid_ranges:
        if name != username and any(start <= value < start + count for value in gid_sets[0] | gid_sets[1]):
            raise ConformanceError(f"GID mapping overlaps delegation for {name}")
    return {
        "pass": True,
        "uid_host_ids": [sorted(values) for values in uid_sets],
        "gid_host_ids": [sorted(values) for values in gid_sets],
    }


def namespace_snapshot(pid: int) -> dict[str, str]:
    return {
        name: os.readlink(f"/proc/{pid}/ns/{name}")
        for name in ("pid", "ipc", "net", "mnt", "user")
    }


def mount_snapshot(pid: int) -> dict[str, Any]:
    text = read_proc(pid, "mountinfo")
    selected: dict[str, str] = {}
    for line in text.splitlines():
        fields = line.split()
        if len(fields) > 5 and fields[4] in {"/", "/dev/shm", "/dev/mqueue", "/probe-state"}:
            selected[fields[4]] = line
    root = selected.get("/", "")
    private = " shared:" not in f" {root}" and " master:" not in f" {root}"
    if not private:
        raise ConformanceError(f"root mount propagation is not private: {root}")
    for required in ("/", "/dev/shm", "/dev/mqueue", "/probe-state"):
        if required not in selected:
            raise ConformanceError(f"required mount missing: {required}")
    return {"private_root_propagation": private, "selected": selected}


def assert_sandbox(inspect: dict[str, Any], seccomp_path: str) -> dict[str, Any]:
    host = inspect.get("HostConfig", {})
    mounts = inspect.get("Mounts", [])
    failures = []
    checks = {
        "not_privileged": not host.get("Privileged"),
        "network_none": host.get("NetworkMode") == "none",
        "pid_private": host.get("PidMode") == "private",
        "ipc_private": host.get("IpcMode") == "private",
        "uts_private": host.get("UTSMode") == "private",
        "readonly_root": bool(host.get("ReadonlyRootfs")),
        "no_devices": not host.get("Devices"),
        "one_managed_volume": (
            len(mounts) == 1
            and mounts[0].get("Type") == "volume"
            and mounts[0].get("Destination") == "/probe-state"
            and mounts[0].get("Propagation") == "rprivate"
        ),
        "no_external_binds": all(mount.get("Type") == "volume" for mount in mounts),
        "no_runtime_socket": all("sock" not in str(mount.get("Destination", "")) for mount in mounts),
        "no_new_privileges": any("no-new-privileges" in item for item in host.get("SecurityOpt", [])),
        "seccomp_explicit": any(seccomp_path in item for item in host.get("SecurityOpt", [])),
    }
    for name, passed in checks.items():
        if not passed:
            failures.append(name)
    if failures:
        raise ConformanceError(f"sandbox inspection failed: {failures}")
    return {"pass": True, "checks": checks}


def check_supervisor(data: dict[str, Any]) -> None:
    status = data["status"]
    expected_caps = f"{EXPECTED_SUPERVISOR_CAPS:016x}"
    checks = {
        "uids": status.get("Uid") == "100\t100\t100\t100",
        "gids": status.get("Gid") == "100\t100\t100\t100",
        "groups": status.get("Groups") == "100",
        "dumpable": data.get("dumpable") == 0,
        "effective": status.get("CapEff") == expected_caps,
        "permitted": status.get("CapPrm") == expected_caps,
        "inheritable": status.get("CapInh") == expected_caps,
        "bounding": status.get("CapBnd") == expected_caps,
        "ambient_empty": status.get("CapAmb") == "0000000000000000",
    }
    failed = [name for name, passed in checks.items() if not passed]
    if failed:
        raise ConformanceError(f"supervisor report failed {failed}: {data}")


def require_pass(name: str, value: dict[str, Any]) -> None:
    if not value.get("pass"):
        raise ConformanceError(f"{name} failed: {json.dumps(value, sort_keys=True)}")


def run_trial(
    runner: Runner,
    output: pathlib.Path,
    image: str,
    seccomp_path: str,
    profile: str,
    iteration: int,
    token: str,
) -> dict[str, Any]:
    trial = output / profile / f"iteration-{iteration}"
    trial.mkdir(parents=True)
    names = [f"reploy-mi-{token}-{profile}-{iteration}-{side}" for side in ("a", "b")]
    volumes = [f"reploy-mi-{token}-{profile}-{iteration}-state-{side}" for side in ("a", "b")]
    created_containers: list[str] = []
    created_volumes: list[str] = []
    cleanup: dict[str, Any] = {"containers": {}, "volumes": {}}
    try:
        mountpoints = []
        for volume in volumes:
            runner.run(["podman", "volume", "create", volume])
            created_volumes.append(volume)
            mountpoint = runner.run(
                ["podman", "volume", "inspect", "--format", "{{.Mountpoint}}", volume]
            ).stdout.strip()
            pathlib.Path(mountpoint).chmod(0o777)
            mountpoints.append(mountpoint)
        for slot, (name, volume) in enumerate(zip(names, volumes)):
            args = common_run_args(name, seccomp_path, profile, slot, volume=volume)
            runner.run([*args, "--detach", image])
            created_containers.append(name)
            wait_ready(runner, name)

        inspections = [inspect_container(runner, name) for name in names]
        pids = [int(value["State"]["Pid"]) for value in inspections]
        uid_maps = [parse_id_map(read_proc(pid, "uid_map")) for pid in pids]
        gid_maps = [parse_id_map(read_proc(pid, "gid_map")) for pid in pids]
        mapping_profile = assert_mapping_profile(profile, uid_maps, gid_maps)
        namespaces = [namespace_snapshot(pid) for pid in pids]
        mounts = [mount_snapshot(pid) for pid in pids]
        for namespace in ("pid", "ipc", "net", "mnt", "user"):
            if namespaces[0][namespace] == namespaces[1][namespace]:
                raise ConformanceError(f"workloads share {namespace} namespace")

        supervisor = request_action(runner, names[0], "supervisor-report")["data"]
        check_supervisor(supervisor)
        app = request_action(runner, names[0], "app-report")["data"]
        require_pass("application report", app)
        policy = {}
        for action, value in (
            ("reject-uid", 1999),
            ("reject-uid", SUPERVISOR_UID),
            ("reject-gid", 2999),
            ("reject-gid", RUNTIME_GID),
            ("reject-gid", SUPERVISOR_GID),
            ("reject-group", 3999),
        ):
            key = f"{action}-{value}"
            response = request_action(runner, names[0], action, value=value)
            if not response["response"].get("rejected"):
                raise ConformanceError(f"supervisor did not reject {action}={value}")
            policy[key] = response

        private = []
        for name in names:
            private.append(request_action(runner, name, "write-private"))
        setid = request_action(runner, names[0], "setid-target")["data"]
        filecap = request_action(runner, names[0], "filecap-target")["data"]
        require_pass("set-ID privilege regain", setid)
        require_pass("file-capability privilege regain", filecap)

        service = request_action(runner, names[0], "start-services", token=token)["data"]
        cross = request_action(
            runner, names[1], "cross-probe", token=token, target_pid=pids[0]
        )["data"]
        require_pass("cross-workload isolation", cross)
        raw = runner.json(["podman", "exec", names[0], "/probe", "raw-matrix", profile])
        require_pass("raw identity matrix", raw)

        no_cap_name = f"reploy-mi-{token}-{profile}-{iteration}-nocap"
        no_cap_args = common_run_args(
            no_cap_name, seccomp_path, profile, 2, capabilities=False
        )
        created_containers.append(no_cap_name)
        no_cap = runner.json([*no_cap_args, "--rm", image, "cap-report"])
        require_pass("no-capability default", no_cap)

        owners = [
            {
                "uid": (pathlib.Path(mountpoint) / "app-private").stat().st_uid,
                "gid": (pathlib.Path(mountpoint) / "app-private").stat().st_gid,
            }
            for mountpoint in mountpoints
        ]
        authority = assert_host_authority(uid_maps, gid_maps)
        expected_owners = []
        for mapping_uid, mapping_gid in zip(uid_maps, gid_maps):
            uid_segment = next(item for item in mapping_uid if item[0] <= DECLARED_UID < item[0] + item[2])
            gid_segment = next(item for item in mapping_gid if item[0] <= DECLARED_GID < item[0] + item[2])
            expected_owners.append(
                {
                    "uid": uid_segment[1] + DECLARED_UID - uid_segment[0],
                    "gid": gid_segment[1] + DECLARED_GID - gid_segment[0],
                }
            )
        if owners != expected_owners:
            raise ConformanceError(f"unexpected persistent ownership: {owners} != {expected_owners}")

        sandbox = [assert_sandbox(value, seccomp_path) for value in inspections]
        evidence = {
            "pass": True,
            "profile": profile,
            "iteration": iteration,
            "containers": names,
            "volumes": volumes,
            "pids": pids,
            "uid_maps": uid_maps,
            "gid_maps": gid_maps,
            "mapping_profile": mapping_profile,
            "authority": authority,
            "namespaces": namespaces,
            "mounts": mounts,
            "supervisor": supervisor,
            "application": app,
            "policy_rejections": policy,
            "private_writes": private,
            "setid": setid,
            "filecap": filecap,
            "service": service,
            "cross_workload": cross,
            "raw_matrix": raw,
            "no_capability_default": no_cap,
            "owners": owners,
            "expected_owners": expected_owners,
            "sandbox": sandbox,
            "inspect": inspections,
        }
        write_json(trial / "evidence.json", evidence)
        return evidence
    finally:
        for name in reversed(created_containers):
            result = runner.run(["podman", "rm", "-f", name], check=False, timeout=30)
            cleanup["containers"][name] = {
                "returncode": result.returncode,
                "stdout": result.stdout,
                "stderr": result.stderr,
            }
        for volume in reversed(created_volumes):
            result = runner.run(["podman", "volume", "rm", "-f", volume], check=False)
            cleanup["volumes"][volume] = {
                "returncode": result.returncode,
                "stdout": result.stdout,
                "stderr": result.stderr,
            }
        cleanup["remaining_containers"] = runner.run(
            ["podman", "ps", "-aq", "--filter", f"name=reploy-mi-{token}-"]
        ).stdout.splitlines()
        cleanup["remaining_volumes"] = runner.run(
            ["podman", "volume", "ls", "-q", "--filter", f"name=reploy-mi-{token}-"]
        ).stdout.splitlines()
        cleanup["pass"] = not cleanup["remaining_containers"] and not cleanup["remaining_volumes"]
        write_json(trial / "cleanup.json", cleanup)
        if not cleanup["pass"] and sys.exc_info()[0] is None:
            raise ConformanceError(f"trial cleanup incomplete: {cleanup}")


def main() -> int:
    args = parse_args()
    profiles = [item.strip() for item in args.profiles.split(",") if item.strip()]
    if args.iterations < 1:
        raise ConformanceError("--iterations must be positive")
    if not profiles or any(profile not in {"exact", "bounded"} for profile in profiles):
        raise ConformanceError("--profiles must contain exact and/or bounded")
    timestamp = dt.datetime.now(dt.UTC).strftime("%Y%m%dT%H%M%SZ")
    output = args.output_dir or pathlib.Path(tempfile.mkdtemp(prefix="reploy-mi-conformance-"))
    output = output.resolve()
    if output.exists():
        if any(output.iterdir()):
            raise ConformanceError(f"evidence directory is not empty: {output}")
    else:
        output.mkdir(parents=True)
    runner = Runner(output)
    token = (
        f"{timestamp.lower().replace('t', '').replace('z', '')}-{os.getpid()}-"
        f"{secrets.token_hex(16)}"
    )
    image = f"localhost/reploy-local/multiidentity-conformance:{token}"
    baseline: dict[str, list[str]] | None = None
    summary: dict[str, Any] = {
        "schema": "reploy-private-multiidentity-conformance-v1",
        "started_at": timestamp,
        "iterations": args.iterations,
        "profiles": profiles,
        "pass": False,
    }
    try:
        baseline = inventory(runner, "podman")
        host = host_context(runner)
        write_json(output / "host.json", host)
        negative = docker_negative(runner)
        write_json(output / "docker-negative.json", negative)
        image_evidence = build_image(runner, output, image)
        write_json(output / "image.json", image_evidence)
        trials = []
        seccomp_path = host["seccomp_profile"]["path"]
        for profile in profiles:
            for iteration in range(1, args.iterations + 1):
                trials.append(
                    run_trial(
                        runner, output, image, seccomp_path,
                        profile, iteration, token,
                    )
                )
        summary.update(
            {
                "pass": True,
                "host": host,
                "docker_negative": negative,
                "image": image_evidence,
                "trials": [
                    {
                        "profile": trial["profile"],
                        "iteration": trial["iteration"],
                        "pass": trial["pass"],
                        "uid_maps": trial["uid_maps"],
                        "gid_maps": trial["gid_maps"],
                        "owners": trial["owners"],
                    }
                    for trial in trials
                ],
            }
        )
    except Exception as exc:
        summary["error"] = f"{type(exc).__name__}: {exc}"
    finally:
        remove = runner.run(["podman", "image", "rm", "-f", image], check=False, timeout=60)
        summary["image_cleanup"] = {
            "returncode": remove.returncode,
            "stdout": remove.stdout,
            "stderr": remove.stderr,
        }
        if baseline is not None:
            final = inventory(runner, "podman")
            summary["podman_inventory_before"] = baseline
            summary["podman_inventory_after"] = final
            summary["engine_restored"] = baseline == final
            if summary.get("pass") and baseline != final:
                summary["pass"] = False
                summary["error"] = "Podman inventory was not restored"
        summary["completed_at"] = dt.datetime.now(dt.UTC).strftime("%Y%m%dT%H%M%SZ")
        write_json(output / "summary.json", summary)
        runner.save_commands()
        print(output)
    if not summary.get("pass"):
        raise ConformanceError(str(summary.get("error", "conformance failed")))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ConformanceError as exc:
        print(f"conformance failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
