#!/usr/bin/env python3
"""Disposable F3.1.1 trusted setup ordering, not public mapped activation.

Use the explicit private rootless runtime retained by F3.0. Evidence stays
outside the checkout. No default engine, host firewall or account is changed.
"""
import argparse
import base64
import hashlib
import io
import json
import os
from pathlib import Path
import queue
import re
import secrets
import select
import subprocess
import tarfile
import threading
import time

ROOT = Path(__file__).resolve().parents[3]
LABEL = "reploy.f311"
PAYLOAD = Path(__file__).with_name("f311_network_startup_payload.go.txt")
F30 = Path(__file__).with_name("f30_network_payload.go.txt")


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def events(text, prefix="F311 "):
    return [json.loads(line[len(prefix):]) for line in text.splitlines() if line.startswith(prefix)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--runtime-json", required=True, type=Path)
    parser.add_argument("--output-dir", required=True, type=Path)
    args = parser.parse_args()
    output = args.output_dir.resolve()
    if output.is_relative_to(ROOT):
        parser.error("evidence must be external to the candidate checkout")
    runtime_path = args.runtime_json.resolve(strict=True)
    private = runtime_path.parent
    runtime = json.loads(runtime_path.read_text())
    prefix = runtime["prefix"]
    if private.parent != Path("/tmp") or not private.name.startswith("reploy-F30-runtime-"):
        parser.error("use the explicit F3.0 private runtime")
    for flag, directory in (("--root", "graph"), ("--runroot", "run")):
        if Path(prefix[prefix.index(flag) + 1]).resolve() != private / directory:
            parser.error("engine storage must belong to that private runtime")
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    env = {**os.environ, **runtime["env"], "CGO_ENABLED": "0"}
    token = secrets.token_hex(12)
    sources = [Path(__file__), PAYLOAD, F30, *sorted((ROOT / "internal/probe").glob("*.go"))]
    result = {"scope": "F3.1.1 setup before release only; no production provider or public activation",
              "pass": False, "token": token, "commands": [], "cases": [], "cleanup": [],
              "sources": {str(p.relative_to(ROOT)): digest(p) for p in sources},
              "runtime_sha256": digest(runtime_path), "engine_sha256": digest(prefix[0])}
    containers, streams, monitor_fds = [], [], []
    image, original_networks = None, None

    def run(argv, *, engine=True, check=True, timeout=60, extra_env=None):
        command = (prefix if engine else []) + [str(x) for x in argv]
        p = subprocess.run(command, cwd=ROOT, env={**env, **(extra_env or {})},
                           capture_output=True, text=True, timeout=timeout)
        index = len(result["commands"])
        stdout, stderr = output / f"{index:03d}.stdout", output / f"{index:03d}.stderr"
        stdout.write_text(p.stdout)
        stderr.write_text(p.stderr)
        result["commands"].append({"argv": command, "exit_code": p.returncode,
                                   "stdout": str(stdout), "stdout_sha256": digest(stdout),
                                   "stderr": str(stderr), "stderr_sha256": digest(stderr)})
        if check and p.returncode:
            raise RuntimeError(f"command {index} exited {p.returncode}: {p.stderr[-1200:]} {p.stdout[-1200:]}")
        return p

    def inspect(identity):
        return json.loads(run(["inspect", identity]).stdout)[0]

    def remove_container(identity):
        exists = run(["container", "exists", identity], check=False)
        if exists.returncode == 1:
            containers.remove(identity)
            result["cleanup"].append({"container": identity, "absent": True})
            return
        if exists.returncode:
            raise RuntimeError("could not verify owned container existence")
        obj = inspect(identity)
        exact = obj["Id"]
        matches = exact == identity if re.fullmatch("[a-f0-9]{64}", identity) else obj["Name"].lstrip("/") == identity
        if not re.fullmatch("[a-f0-9]{64}", exact) or not matches or obj["Config"]["Labels"].get(LABEL) != token:
            raise RuntimeError("container cleanup ownership changed")
        run(["rm", "--force", "--time", "1", exact])
        containers.remove(identity)
        result["cleanup"].append({"container": identity, "removed": True})

    def stream(identity, name):
        stdout = output / f"{name}.stdout"
        stderr = (output / f"{name}.stderr").open("wb")
        process = subprocess.Popen(prefix + ["start", "--attach", "--interactive", identity],
                                   cwd=ROOT, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                   stderr=stderr)
        received, observed = queue.Queue(maxsize=32), []

        def read():
            with stdout.open("wb") as log:
                for line in process.stdout:
                    log.write(line)
                    log.flush()
                    if line.startswith(b"F311 "):
                        event = json.loads(line[5:])
                        observed.append(event)
                        received.put(event, timeout=2)

        thread = threading.Thread(target=read, daemon=True)
        thread.start()
        streams.append((process, thread, stderr))

        def started():
            deadline = time.monotonic() + 8
            while time.monotonic() < deadline:
                try:
                    event = received.get(timeout=0.1)
                except queue.Empty:
                    if process.poll() is not None:
                        break
                    continue
                if event["event"] == "stage-started":
                    return
            raise RuntimeError(f"stage did not start: {stdout.read_text()[-1200:]}")

        def finish(expected, timeout=15):
            code = process.wait(timeout=timeout)
            process.stdin.close()
            thread.join(timeout=3)
            stderr.close()
            if code != expected or thread.is_alive():
                raise RuntimeError(f"{name} exit={code}, expected={expected}: {stdout.read_text()[-1600:]}")
            return observed

        return process, started, finish, observed

    try:
        info = json.loads(run(["info", "--format", "json"]).stdout)
        if not info["host"]["security"]["rootless"] or Path(info["store"]["graphRoot"]).resolve() != private / "graph":
            raise RuntimeError("an exact private rootless engine is required")
        if json.loads(run(["ps", "--all", "--format", "json"]).stdout) or json.loads(run(["images", "--format", "json"]).stdout):
            raise RuntimeError("private engine contains existing containers or images")
        original_networks = {n["id"] for n in json.loads(run(["network", "ls", "--format", "json"]).stdout)}
        result["engine"] = info["version"]
        result["head"] = run(["sl", "log", "-r", ".", "-T", "{node}"], engine=False).stdout
        result["go_version"] = run(["go", "version"], engine=False).stdout.strip()
        overlay = output / "overlay.json"
        overlay.write_text(json.dumps({"Replace": {
            str(ROOT / "internal/probe/f30_network_boundary_test.go"): str(F30),
            str(ROOT / "internal/probe/f311_network_startup_test.go"): str(PAYLOAD)}}))
        binary = output / "probe"
        run(["go", "test", "-c", "-buildvcs=false", "-overlay", overlay, "-o", binary, "./internal/probe"],
            engine=False, timeout=300)
        result["probe_sha256"] = digest(binary)
        rootfs = output / "rootfs.tar"
        with tarfile.open(rootfs, "w") as archive:
            for name in ("etc", "tmp", "dev", "proc", "sys", "run"):
                entry = tarfile.TarInfo(name)
                entry.type, entry.mode = tarfile.DIRTYPE, 0o755
                archive.addfile(entry)
            for name, data, mode in (("probe", binary.read_bytes(), 0o555), ("etc/resolv.conf", b"", 0o644)):
                entry = tarfile.TarInfo(name)
                entry.mode, entry.size = mode, len(data)
                archive.addfile(entry, io.BytesIO(data))
        image = f"localhost/reploy-f311:{token}"
        created = run(["import", "--change", f"LABEL {LABEL}={token}", rootfs, image]).stdout.strip()
        if not re.fullmatch("(?:sha256:)?[a-f0-9]{64}", created):
            raise RuntimeError("invalid imported image identity")
        image = created
        for name, mask, setup, release in (("empty", 0, True, "valid"), ("bind", 1 << 10, True, "valid"),
                                           ("deadline", 0, True, "none"), ("malformed", 0, True, "malformed"),
                                           ("setup-failure", 0, False, "none"),
                                           ("invalid-plan", 0, False, "none")):
            common = {"F311_TOKEN": token, "F311_CASE": name, "F311_MASK": f"{mask:016x}",
                      "F311_DEADLINE_SECONDS": "10" if name == "deadline" else "30"}
            container_name = f"reploy-f311-{token}-{name}"
            options = ["create", "--pull=never", "--name", container_name, "--label", f"{LABEL}={token}",
                       "--interactive", "--network", "none", "--cgroups", "disabled", "--read-only",
                       "--uidmap", "0:1:1", "--gidmap", "0:1:1", "--user", "0:0", "--cap-drop", "all",
                       "--security-opt", f"seccomp={ROOT / 'internal/dockerdeploy/seccomp/docker-v0.2.4.json'}",
                       "--security-opt", "no-new-privileges"]
            for capability in ["NET_RAW", "SETPCAP", "SETUID", "SETGID", "NET_BIND_SERVICE"] + (["NET_ADMIN"] if setup else []):
                options += ["--cap-add", capability]
            for key, value in {**common, "F311_ROLE": "stage"}.items():
                options += ["--env", f"{key}={value}"]
            containers.append(container_name)
            identity = run(options + [image, "/probe", "-test.run=^TestF311NetworkStartup$", "-test.v"]).stdout.strip()
            if not re.fullmatch("[a-f0-9]{64}", identity):
                raise RuntimeError("invalid container identity")
            containers[containers.index(container_name)] = identity
            process, started, finish, observed = stream(identity, name)
            started()
            row = {"name": name, "mask": mask, "container": identity, "release": release}
            if setup:
                obj = inspect(identity)
                if not obj["State"]["Running"] or obj["State"]["Pid"] <= 0:
                    raise RuntimeError("waiter is not a pinned running process")
                for pid in (obj["State"]["Pid"], obj["State"]["ConmonPid"]):
                    monitor_fds.append(os.pidfd_open(pid))
                namespace = run(["unshare", "/usr/bin/readlink", f"/proc/{obj['State']['Pid']}/ns/net"]).stdout.strip()
                deadline = time.monotonic() + 6
                while True:
                    probe = run(["exec", "--env", "F311_ROLE=observe", "--env", f"F311_NAMESPACE={namespace}",
                                 identity, "/probe", "-test.run=^TestF311NetworkStartup$", "-test.v"], timeout=5)
                    if any(e["event"] == "policy-observed" for e in events(probe.stdout)):
                        row["pre_release_policy"] = events(probe.stdout, "F30 ")
                        break
                    if process.poll() is not None or time.monotonic() >= deadline:
                        raise RuntimeError("effective policy was unavailable before release")
                    time.sleep(0.1)
                if process.poll() is not None or any(e["event"] == "payload-complete" for e in observed):
                    raise RuntimeError("payload completed without release")
                if release == "valid":
                    framed = run([binary, "-test.run=^TestF311NetworkStartup$", "-test.v"], engine=False,
                                 extra_env={**common, "F311_ROLE": "frame", "F311_CONTAINER": identity})
                    frames = [e["value"] for e in events(framed.stdout) if e["event"] == "frame"]
                    if len(frames) != 1:
                        raise RuntimeError("missing exact release frame")
                    process.stdin.write(base64.b64decode(frames[0]) + token.encode() + b"\n")
                    process.stdin.flush()
                elif release == "malformed":
                    process.stdin.write(b"\0\0\0\0")
                    process.stdin.flush()
            final = finish(0 if release == "valid" else 1, timeout=15 if setup else 5)
            completed = [e["value"] for e in final if e["event"] == "payload-complete"]
            refused = [e["value"] for e in final if e["event"] == "refused"]
            if release == "valid":
                if len(completed) != 1 or completed[0]["mask"] != mask or refused:
                    raise RuntimeError("final payload authority was not proved")
            elif completed or len(refused) != 1 or not any(word in refused[0] for word in {
                    "deadline": ["deadline exceeded"], "malformed": ["invalid mapped release length"],
                    "setup-failure": ["install application network policy"],
                    "invalid-plan": ["mapped executable must be a normalized absolute path"]}[name]):
                raise RuntimeError(f"wrong pre-release refusal: {final}")
            row["events"], row["passed"] = final, True
            result["cases"].append(row)
            remove_container(identity)
        result["proof_complete"] = True
    except Exception as error:
        result["error"] = f"{type(error).__name__}: {error}"
    finally:
        for identity in containers[:]:
            try:
                remove_container(identity)
            except Exception as error:
                result["cleanup"].append({"container": identity, "error": str(error)})
        for process, thread, stderr in streams:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=3)
            process.stdin.close()
            thread.join(timeout=3)
            stderr.close()
        if image:
            try:
                exists = run(["image", "exists", image], check=False)
                if exists.returncode == 0:
                    obj = json.loads(run(["image", "inspect", image]).stdout)[0]
                    exact = obj["Id"]
                    matches = exact == image.removeprefix("sha256:") if re.fullmatch("(?:sha256:)?[a-f0-9]{64}", image) else image in obj["RepoTags"]
                    if not re.fullmatch("[a-f0-9]{64}", exact) or not matches or obj["Labels"].get(LABEL) != token:
                        raise RuntimeError("image cleanup ownership changed")
                    run(["image", "rm", exact])
                elif exists.returncode != 1:
                    raise RuntimeError("could not verify owned image existence")
                result["cleanup"].append({"image": image, "retired": True})
            except Exception as error:
                result["cleanup"].append({"image": image, "error": str(error)})
        poll = select.poll()
        remaining = set(monitor_fds)
        for fd in remaining:
            poll.register(fd, select.POLLIN)
        deadline = time.monotonic() + 3
        while remaining and time.monotonic() < deadline:
            remaining -= {fd for fd, _ in poll.poll(100)}
        result["monitors_retired"] = not remaining
        for fd in monitor_fds:
            os.close(fd)
        try:
            result["inventory_restored"] = (not json.loads(run(["ps", "--all", "--format", "json"]).stdout)
                                             and not json.loads(run(["images", "--format", "json"]).stdout)
                                             and {n["id"] for n in json.loads(run(["network", "ls", "--format", "json"]).stdout)} == original_networks)
        except Exception as error:
            result["cleanup"].append({"inventory_error": str(error)})
        result["source_unchanged"] = all(digest(ROOT / name) == value for name, value in result["sources"].items())
        result["pass"] = (result.get("proof_complete", False) and not result.get("error")
                          and result.get("inventory_restored", False) and result["monitors_retired"]
                          and result["source_unchanged"] and not any("error" in x for x in result["cleanup"]))
        (output / "report.json").write_text(json.dumps(result, indent=2) + "\n")
        print(json.dumps({"pass": result["pass"], "error": result.get("error"), "report": str(output / "report.json")}))
    return 0 if result["pass"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
