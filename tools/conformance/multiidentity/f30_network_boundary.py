#!/usr/bin/env python3
"""Disposable F3.0 engine qualification; no public mapped activation.

Supply an explicit private runtime JSON containing env and prefix, such as the
retained runtime-preflight artifact. The engine graph/run roots must belong to
that runtime directory under /tmp. Run with a fresh external --output-dir.
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
import socket
import subprocess
import tarfile
import threading
import time

ROOT = Path(__file__).resolve().parents[3]
PAYLOAD = Path(__file__).with_name("f30_network_payload.go.txt")
LABEL = "reploy.f30"


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def events(text):
    return [json.loads(line[4:]) for line in text.splitlines() if line.startswith("F30 ")]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--runtime-json", required=True, type=Path)
    parser.add_argument("--output-dir", required=True, type=Path)
    args = parser.parse_args()
    output = args.output_dir.resolve()
    if output.is_relative_to(ROOT):
        parser.error("evidence must be external to the candidate checkout")
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    runtime_path = args.runtime_json.resolve(strict=True)
    runtime = json.loads(runtime_path.read_text())
    prefix = runtime["prefix"]
    private = runtime_path.parent
    if private.parent != Path("/tmp") or not private.name.startswith("reploy-F30-runtime-"):
        parser.error("runtime must have its own /tmp/reploy-F30-runtime-* directory")
    for flag, directory in (("--root", "graph"), ("--runroot", "run")):
        if Path(prefix[prefix.index(flag) + 1]).resolve() != private / directory:
            parser.error("engine storage must belong to the explicit private runtime")
    env = {**os.environ, **runtime["env"], "CGO_ENABLED": "0"}
    token = secrets.token_hex(12)
    result = {
        "schema": "reploy-f30-private-network-proof-v1", "token": token,
        "scope": "F3.0 mechanism qualification only; no public activation or F3.1/F3.2 completion",
        "pass": False, "commands": [], "cleanup": [], "attempts": [],
        "source": {str(p.relative_to(ROOT)): digest(p) for p in (Path(__file__), PAYLOAD)},
        "runtime_json_sha256": digest(runtime_path), "engine_binary_sha256": digest(prefix[0]),
        "containers_config_sha256": digest(env["CONTAINERS_CONF"]),
        "image_policy_sha256": digest(Path(env["XDG_CONFIG_HOME"]) / "containers/policy.json"),
    }
    containers, networks, streams = [], [], []
    monitor_fds, offsets = {}, {}
    image = None
    start = time.monotonic()

    def run(argv, *, engine=True, check=True, timeout=60, input_text=None):
        argv = (prefix if engine else []) + [str(x) for x in argv]
        index = len(result["commands"])
        p = subprocess.run(argv, cwd=ROOT, env=env, input=input_text,
                           capture_output=True, text=True, timeout=timeout)
        stdout, stderr = output / f"{index:03d}.stdout", output / f"{index:03d}.stderr"
        stdout.write_text(p.stdout)
        stderr.write_text(p.stderr)
        result["commands"].append({
            "argv": argv, "exit_code": p.returncode, "stdout": str(stdout),
            "stdout_sha256": digest(stdout), "stderr": str(stderr), "stderr_sha256": digest(stderr),
        })
        if check and p.returncode:
            raise RuntimeError(f"command {index} exited {p.returncode}: {p.stderr[-1500:]}")
        return p

    def inspect(identity):
        return json.loads(run(["inspect", identity]).stdout)[0]

    def remove_container(identity):
        present = run(["container", "exists", identity], check=False)
        if present.returncode == 1:
            containers.remove(identity)
            result["cleanup"].append({"container": identity, "absent": True})
            return
        if present.returncode:
            raise RuntimeError("could not verify container existence for cleanup")
        obj = inspect(identity)
        if obj["Id"] != identity or obj["Config"]["Labels"].get(LABEL) != token:
            raise RuntimeError("container cleanup ownership changed")
        run(["rm", "--force", "--time", "1", identity])
        containers.remove(identity)
        result["cleanup"].append({"container": identity, "removed": True})

    def stream(identity, name):
        stdout = output / f"{name}.stdout"
        stderr = (output / f"{name}.stderr").open("w")
        p = subprocess.Popen(prefix + ["start", "--attach", "--interactive", identity],
                             cwd=ROOT, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                             stderr=stderr, text=True, bufsize=1)
        received = queue.Queue(maxsize=1024)

        def read():
            with stdout.open("w") as log:
                for line in p.stdout:
                    log.write(line)
                    log.flush()
                    if line.startswith("F30 "):
                        received.put(json.loads(line[4:]), timeout=2)

        thread = threading.Thread(target=read, daemon=True)
        thread.start()
        entry = (p, thread, stderr)
        streams.append(entry)

        def wait_event(kind, timeout=100):
            deadline = time.monotonic() + timeout
            while time.monotonic() < deadline:
                try:
                    event = received.get(timeout=0.2)
                except queue.Empty:
                    if p.poll() is not None and not thread.is_alive():
                        detail = (output / f"{name}.stderr").read_text()
                        raise RuntimeError(f"{name} exited before {kind}: {stdout.read_text()[-800:]} {detail[-1600:]}")
                    continue
                if event["event"] == kind:
                    return event["value"]
            raise RuntimeError(f"{name} timed out waiting for {kind}")

        def finish(expected=0):
            p.stdin.close()
            code = p.wait(timeout=30)
            thread.join(timeout=3)
            stderr.close()
            if thread.is_alive() or code != expected:
                raise RuntimeError(f"{name} exit={code}, expected={expected}: {stdout.read_text()[-1200:]}")
            return stdout.read_text()

        return p, wait_event, finish

    def create(name, network, offset, role, *, ip4=None, ip6=None, published=False, config=None, client=None):
        options = ["create", "--pull=never", "--name", name, "--label", f"{LABEL}={token}",
                   "--network", network, "--cgroups", "disabled", "--read-only",
                   "--uidmap", f"0:{offset}:1", "--gidmap", f"0:{offset}:1",
                   "--user", "0:0", "--cap-drop", "all",
                   "--security-opt", f"seccomp={ROOT / 'internal/dockerdeploy/seccomp/docker-v0.2.4.json'}",
                   "--env", f"F30_TOKEN={token}", "--env", f"F30_ROLE={role}"]
        if role == "stage":
            options += ["--interactive"]
            for capability in ("NET_ADMIN", "NET_RAW", "SETPCAP", "SETUID", "SETGID", "NET_BIND_SERVICE"):
                options += ["--cap-add", capability]
        elif role == "server":
            options += ["--cap-add", "NET_BIND_SERVICE"]
        if ip4:
            options += ["--ip", ip4, "--ip6", ip6]
        if published:
            options += ["--publish", "127.0.0.1::18080/tcp"]
        if role == "stage":
            options += ["--dns", external4]
        if config:
            encoded = base64.b64encode(json.dumps(config).encode()).decode()
            options += ["--env", f"F30_CONFIG={encoded}", "--env", f"F30_CLIENT_CASE={client}"]
        identity = run(options + [image, "/probe", "-test.run=^TestF30Network$", "-test.v"]).stdout.strip()
        if not re.fullmatch("[a-f0-9]{64}", identity):
            raise RuntimeError("engine returned an invalid container identity")
        containers.append(identity)
        offsets[identity] = offset
        if inspect(identity)["Config"]["Labels"].get(LABEL) != token:
            raise RuntimeError("container ownership label missing")
        return identity

    def namespace(identity):
        obj = inspect(identity)
        pid = obj["State"]["Pid"]
        if not obj["State"]["Running"] or pid <= 0:
            raise RuntimeError("fixture is not a running pinned process")
        for process_id in (pid, obj["State"]["ConmonPid"]):
            if process_id not in monitor_fds:
                monitor_fds[process_id] = os.pidfd_open(process_id)
        mapping = run(["unshare", "/usr/bin/cat", f"/proc/{pid}/uid_map"]).stdout
        rows = [tuple(map(int, line.split())) for line in mapping.splitlines()]
        if rows != [(0, offsets[identity], 1)]:
            raise RuntimeError("container did not retain its exact subordinate mapping")
        name = run(["unshare", "/usr/bin/readlink", f"/proc/{pid}/ns/net"]).stdout.strip()
        return pid, name

    def net_command(pid, *arguments):
        return run(["unshare", "/usr/bin/nsenter", f"--net=/proc/{pid}/ns/net",
                    "/usr/bin/ip", *arguments])

    def host_echo(port):
        with socket.create_connection(("127.0.0.1", port), timeout=2) as c:
            c.sendall(token.encode())
            data = b""
            while len(data) < len(token):
                part = c.recv(len(token) - len(data))
                if not part:
                    break
                data += part
            if data != token.encode():
                raise RuntimeError("published endpoint did not identify this exact fixture")

    def client(name, network, config, case):
        identity = create(name, network, 4, "client", config=config, client=case)
        p = run(["start", "--attach", identity])
        remove_container(identity)
        return events(p.stdout)

    try:
        info = json.loads(run(["info", "--format", "json"]).stdout)
        if not info["host"]["security"]["rootless"] or Path(info["store"]["graphRoot"]).resolve() != private / "graph":
            raise RuntimeError("an exact private rootless engine is required")
        if json.loads(run(["ps", "--all", "--format", "json"]).stdout):
            raise RuntimeError("the private engine already contains containers")
        if json.loads(run(["images", "--format", "json"]).stdout):
            raise RuntimeError("the private engine already contains images")
        original_networks = {n["id"] for n in json.loads(run(["network", "ls", "--format", "json"]).stdout)}
        result["engine"] = {"version": info["version"], "host": info["host"], "store": info["store"]}
        result["reploy_head"] = run(["sl", "log", "-r", ".", "-T", "{node}"], engine=False).stdout
        result["package_sources"] = {
            str(path.relative_to(ROOT)): digest(path)
            for path in sorted((ROOT / "internal/probe").glob("*.go"))
        }
        result["seccomp_seed_sha256"] = digest(ROOT / "internal/dockerdeploy/seccomp/docker-v0.2.4.json")
        result["engine_uid_map"] = run(["unshare", "/usr/bin/cat", "/proc/self/uid_map"]).stdout
        result["oci_launcher_sha256"] = digest(info["host"]["ociRuntime"]["path"])
        result["private_helpers"] = {
            str(path.relative_to(private)): digest(path)
            for path in (private / "deps/usr/bin/crun", private / "deps/usr/bin/conmon",
                         private / "deps/usr/lib/podman/netavark",
                         private / "deps/usr/lib/podman/aardvark-dns", private / "deps/usr/bin/pasta")
        }
        result["go_version"] = run(["go", "version"], engine=False).stdout.strip()
        overlay = output / "overlay.json"
        overlay.write_text(json.dumps({"Replace": {str(ROOT / "internal/probe/f30_network_boundary_test.go"): str(PAYLOAD)}}))
        binary = output / "probe"
        run(["go", "test", "-c", "-buildvcs=false", "-overlay", overlay, "-o", binary, "./internal/probe"],
            engine=False, timeout=300)
        result["probe_sha256"] = digest(binary)
        rootfs = output / "rootfs.tar"
        with tarfile.open(rootfs, "w") as archive:
            for directory in ("etc", "tmp", "dev", "proc", "sys", "run"):
                entry = tarfile.TarInfo(directory)
                entry.type, entry.mode = tarfile.DIRTYPE, 0o755
                archive.addfile(entry)
            for name, data, mode in [("probe", binary.read_bytes(), 0o555),
                                     ("etc/resolv.conf", b"", 0o644), ("etc/hosts", b"", 0o644),
                                     ("etc/hostname", b"", 0o644)]:
                entry = tarfile.TarInfo(name)
                entry.mode, entry.size = mode, len(data)
                archive.addfile(entry, io.BytesIO(data))
        image = run(["import", "--change", f"LABEL {LABEL}={token}", rootfs,
                     f"localhost/reploy-f30:{token}"]).stdout.strip()
        result["image"] = image
        result["rootfs_sha256"] = digest(rootfs)
        octet = int(token[:2], 16)
        stem = int(token[2:6], 16)
        nets = []
        for i, first in enumerate((239, 240)):
            name = f"reploy-f30-{token}-{i}"
            networks.append(name)
            run(["network", "create", "--label", f"{LABEL}={token}", "--opt", "isolate=strict",
                 "--ipv6", "--subnet", f"10.{first}.{octet}.0/24",
                 "--subnet", f"fd30:{stem:x}:{i+1}::/64", name])
            nets.append(name)
        external4, external6 = f"10.239.{octet}.2", f"fd30:{stem:x}:1::2"
        peer4, peer6 = f"10.240.{octet}.2", f"fd30:{stem:x}:2::2"
        external = create(f"f30-{token}-external", nets[0], 2, "server", ip4=external4, ip6=external6)
        peer = create(f"f30-{token}-peer", nets[1], 3, "server", ip4=peer4, ip6=peer6, published=True)
        server_namespaces = {}
        for identity, name in ((external, "external"), (peer, "peer")):
            _, ready, _ = stream(identity, name)
            if ready("server-ready")["token"] != token:
                raise RuntimeError("wrong ready fixture")
            server_namespaces[name] = namespace(identity)
        external_pid = server_namespaces["external"][0]
        if server_namespaces["external"][1] == server_namespaces["peer"][1]:
            raise RuntimeError("fixtures share a network namespace")
        for family, address in (("-4", "192.0.0.9/32"), ("-6", "2001:3::1/128")):
            extra = ["nodad"] if family == "-6" else []
            net_command(external_pid, family, "address", "add", address, "dev", "eth0", *extra)
        host_address = run(["port", peer, "18080/tcp"]).stdout.strip()
        if not host_address.startswith("127.0.0.1:"):
            raise RuntimeError("peer control was not published only on loopback")
        peer_port = int(host_address.rsplit(":", 1)[1])
        host_echo(peer_port)
        base = {"Token": token, "Local4": external4, "Local6": external6, "Public4": "192.0.0.9",
                "Public6": "2001:3::1", "Peer4": peer4, "Peer6": peer6, "GatewayPort": peer_port}
        result["shared_attachment_control"] = client(f"f30-{token}-control", nets[1], base, "peer-positive")
        for index, mask in enumerate((0, 1 << 10, 0)):
            stale_control = index == 2
            app4, app6 = f"10.239.{octet}.3", f"fd30:{stem:x}:1::3"
            app = create(f"f30-{token}-app-{index}", nets[0], 1, "stage",
                         ip4=app4, ip6=app6, published=True)
            process, ready, finish = stream(app, f"app-{index}")
            ready("stage-ready")
            pid, ns = namespace(app)
            if ns in {value[1] for value in server_namespaces.values()}:
                raise RuntimeError("workload shared a fixture network namespace")
            for family, address in (("-4", "192.0.0.9/32"), ("-6", "2001:3::1/128")):
                net_command(pid, family, "route", "add", address, "dev", "eth0")
            config = {**base, "Mask": mask, "Namespace": "net:[invalid]" if stale_control else ns,
                      "App4": app4, "App6": app6}
            process.stdin.write(json.dumps(config) + "\n")
            process.stdin.flush()
            if stale_control:
                text = finish(expected=1)
                if "network namespace changed" not in text or any(e["event"] == "final-ready" for e in events(text)):
                    raise RuntimeError("stale namespace did not refuse before the final transition")
                result["stale_namespace_refused"] = True
            else:
                final = ready("final-ready")
                if final["namespace"] != ns or final["mask"] != mask or not final["native_transition"]:
                    raise RuntimeError("native final transition identity mismatch")
                host_address = run(["port", app, "18080/tcp"]).stdout.strip()
                if not host_address.startswith("127.0.0.1:"):
                    raise RuntimeError("declared endpoint was not loopback-only")
                host_echo(int(host_address.rsplit(":", 1)[1]))
                inbound = client(f"f30-{token}-inbound-{index}", nets[0], config, "inbound")
                process.stdin.write("done\n")
                process.stdin.flush()
                text = finish()
                observed = events(text)
                names = {e["value"]["name"] for e in observed if e["event"] == "case"}
                required = {f"{phase}/{target}/{proto}" for phase in ("deny", "public", "local", "all", "final")
                            for target in ("local4", "local6", "public4", "public6", "peer4", "peer6")
                            for proto in ("tcp", "udp")}
                required |= {f"{phase}/dns/{proto}/{kind}" for phase in ("deny", "public", "local", "all", "final")
                             for proto in ("tcp", "udp") for kind in (1, 28)}
                if (not required.issubset(names)
                        or not any(name.startswith("gateway-peer-bypass/") for name in names)
                        or not any(e["event"] == "final-complete" for e in observed)):
                    raise RuntimeError("incomplete traffic matrix")
                result["attempts"].append({"mask": mask, "container": app, "namespace": ns,
                                           "events": observed, "inbound": inbound})
            remove_container(app)
        result["qualified_capability_masks"] = [0, 1 << 10]
        result["unqualified_network_authority"] = ["CAP_NET_ADMIN", "CAP_NET_RAW", "CAP_SYS_ADMIN"]
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
                process.terminate()
                try:
                    process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=3)
            thread.join(timeout=2)
            stderr.close()
        for name in reversed(networks):
            try:
                obj = json.loads(run(["network", "inspect", name]).stdout)[0]
                if obj["labels"].get(LABEL) != token:
                    raise RuntimeError("network cleanup ownership changed")
                run(["network", "rm", name])
                result["cleanup"].append({"network": name, "removed": True})
            except Exception as error:
                result["cleanup"].append({"network": name, "error": str(error)})
        if image:
            try:
                obj = json.loads(run(["image", "inspect", image]).stdout)[0]
                if obj["Labels"].get(LABEL) != token:
                    raise RuntimeError("image cleanup ownership changed")
                run(["image", "rm", image])
                result["cleanup"].append({"image": image, "removed": True})
            except Exception as error:
                result["cleanup"].append({"image": image, "error": str(error)})
        try:
            remaining = json.loads(run(["ps", "--all", "--format", "json"]).stdout)
            images = json.loads(run(["images", "--format", "json"]).stdout)
            current = {n["id"] for n in json.loads(run(["network", "ls", "--format", "json"]).stdout)}
            result["inventory_restored"] = not remaining and not images and current == original_networks
            result["source_unchanged"] = all(digest(ROOT / p) == h for p, h in result["source"].items())
            result["package_sources_unchanged"] = all(digest(ROOT / p) == h for p, h in result["package_sources"].items())
        except Exception as error:
            result["cleanup"].append({"inventory_error": str(error)})
        poll = select.poll()
        for fd in monitor_fds.values():
            poll.register(fd, select.POLLIN)
        deadline = time.monotonic() + 3
        remaining = set(monitor_fds.values())
        while remaining and time.monotonic() < deadline:
            remaining -= {fd for fd, _ in poll.poll(100)}
        result["monitors_retired"] = not remaining
        result["remaining_processes"] = [pid for pid, fd in monitor_fds.items() if fd in remaining]
        for fd in monitor_fds.values():
            os.close(fd)
        result["elapsed_seconds"] = time.monotonic() - start
        result["pass"] = (result.get("proof_complete", False) and result.get("inventory_restored", False)
                          and result.get("source_unchanged", False) and not result.get("error")
                          and result.get("package_sources_unchanged", False) and result["monitors_retired"]
                          and not any("error" in x for x in result["cleanup"]))
        (output / "report.json").write_text(json.dumps(result, indent=2) + "\n")
        print(json.dumps({"pass": result["pass"], "error": result.get("error"),
                          "report": str(output / "report.json")}))
    return 0 if result["pass"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
