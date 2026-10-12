#!/usr/bin/env python3
"""Disposable operation audit of a separately scoped Podman compatibility API.

Native Podman builds the offline fixture and supplies independent observations;
all audited operations use the Docker-compatible endpoint. No native fallback
turns a failed compatible operation into a passing result.
"""

import argparse
import http.client
import json
from pathlib import Path
import secrets
import socket
import subprocess
import time
from urllib.parse import quote

import conformance as c


class UnixHTTP(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("localhost", timeout=30)
        self.path = str(path)

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.path)


def api(path, method, route, body=None):
    connection = UnixHTTP(path)
    try:
        connection.request(method, "/v1.41" + route,
                           body=json.dumps(body) if body is not None else None,
                           headers={"Content-Type": "application/json"})
        response = connection.getresponse()
        raw = response.read(1 << 20)
        if len(raw) == 1 << 20:
            raise c.ConformanceError("audit response exceeded one MiB")
        value = json.loads(raw) if raw else None
        if response.status >= 400:
            raise HTTPError(method, route, response.status, value)
        return value
    finally:
        connection.close()


class HTTPError(c.ConformanceError):
    """API failure retaining the response status independently of its body."""

    def __init__(self, method, route, status, value):
        self.method = method
        self.route = route
        self.status = status
        self.value = value
        super().__init__(f"{method} {route}: HTTP {status}: {value}")


def resource_route(kind, identity):
    return {"container": f"/containers/{identity}/json",
            "volume": f"/volumes/{identity}",
            "network": f"/networks/{identity}"}[kind]


def resource_labels(item):
    return item.get("Labels") or item.get("Config", {}).get("Labels") or {}


def is_not_found(error):
    return isinstance(error, HTTPError) and error.status == 404


def cleanup_targets(resources, attempts):
    """Return each known resource target once, including uncertain attempts."""

    targets = []
    seen = set()
    for kind, identity in resources:
        key = (kind, identity)
        if key not in seen:
            seen.add(key)
            targets.append(key)
    for attempt in attempts:
        identity = attempt.get("identity") or attempt["name"]
        key = (attempt["kind"], identity)
        if key not in seen:
            seen.add(key)
            targets.append(key)
    return targets


def verify_owned(item, kind, identity, token):
    if resource_labels(item).get(c.OWNER_LABEL) != token:
        raise c.ConformanceError(f"refusing foreign audit {kind}: {identity}")


def reconcile_ambiguous_create(path, kind, name, token, resources):
    """Find a create that may have succeeded after its response was lost."""
    try:
        item = api(path, "GET", resource_route(kind, name))
    except Exception as exc:
        if is_not_found(exc):
            return False
        raise
    verify_owned(item, kind, name, token)
    resources.append((kind, name))
    return True


def tracked_create(path, kind, name, token, create, resources, attempts):
    """Create a deterministic object and retain ownership across response loss."""
    attempt = {"kind": kind, "name": name, "identity": None}
    attempts.append(attempt)
    try:
        value = create()
    except Exception:
        if reconcile_ambiguous_create(path, kind, name, token, resources):
            attempt["identity"] = name
        raise
    identity = value.get("Id", name) if isinstance(value, dict) else name
    try:
        item = api(path, "GET", resource_route(kind, identity))
        verify_owned(item, kind, identity, token)
    except Exception:
        if reconcile_ambiguous_create(path, kind, name, token, resources):
            attempt["identity"] = name
        raise
    attempt["identity"] = identity
    resources.append((kind, identity))
    return value


def tracked_post_create_step(path, kind, name, identity, token, resources, attempts, inspect):
    """Run an inspect step and reconcile by deterministic name if it fails."""
    try:
        return inspect()
    except Exception:
        for attempt in reversed(attempts):
            if attempt["kind"] == kind and attempt["name"] == name:
                # Keep the deterministic target available if reconciliation
                # itself fails; final cleanup must retry that target.
                attempt["identity"] = None
                break
        if reconcile_ambiguous_create(path, kind, name, token, resources):
            for attempt in reversed(attempts):
                if attempt["kind"] == kind and attempt["name"] == name:
                    attempt["identity"] = name
                    break
            if identity != name:
                try:
                    resources.remove((kind, identity))
                except ValueError:
                    pass
        raise


def observe(results, name, action):
    try:
        results[name] = {"disposition": "observed-compatible", "evidence": action()}
    except Exception as exc:
        results[name] = {"disposition": "blocked", "error": f"{type(exc).__name__}: {exc}"}


def prepare_output(output):
    output.mkdir(parents=True, exist_ok=True)
    if any(output.iterdir()):
        raise c.ConformanceError("audit evidence directory must be empty")


def audit_volume(path, name, token, resources, attempts, operations, inspect):
    observe(operations, "volume-create-inspect", lambda: tracked_create(
        path, "volume", name, token,
        lambda: api(path, "POST", "/volumes/create", {
            "Name": name, "Labels": {c.OWNER_LABEL: token}}),
        resources, attempts,
    ))
    if operations["volume-create-inspect"]["disposition"] == "observed-compatible":
        observe(operations, "volume-inspect-command", lambda: tracked_post_create_step(
            path, "volume", name, name, token, resources, attempts, inspect))
    else:
        operations["volume-inspect-command"] = {
            "disposition": "blocked",
            "error": "Volume create/ownership inspection did not pass; CLI inspection was not launched.",
        }


def wait_restarted_admission(runner, identity, nonce, previous_ready_count):
    for _ in range(40):
        logs = runner.run(["podman", "logs", identity]).stdout.splitlines()
        if logs.count(f"admission-ready {nonce}") > previous_ready_count:
            return
        time.sleep(0.1)
    raise c.ConformanceError(f"restarted container did not re-enter admission wait: {identity}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    output = args.output_dir.resolve()
    prepare_output(output)
    runner = c.Runner(output)
    token = secrets.token_hex(16)
    sock = output / "podman.sock"
    endpoint = f"unix://{sock}"
    image = f"localhost/reploy-local/mi-audit:{token}"
    resources = []
    attempts = []
    results = {"schema": "reploy-private-multiidentity-operation-audit-v1",
               "endpoint": endpoint, "source_sha256": c.source_hash(),
               "audit_sha256": c.sha256_file(Path(__file__)), "operations": {},
               "cleanup": [], "resource_attempts": attempts}
    operations = results["operations"]
    baseline = c.inventory(runner, "podman")
    planned = c.assert_host_authority(runner, [], [], planned_pool=True)
    uid_start = planned["uid_host_ids"][0][0]
    gid_start = planned["gid_host_ids"][0][0]
    service = None
    image_attempted = False
    started = time.monotonic()
    env = {"DOCKER_HOST": endpoint, "DOCKER_CONTEXT": "", "DOCKER_API_VERSION": "1.41"}

    def dk(*command):
        return runner.run(["docker", *command], env=env).stdout

    def remove(kind, identity):
        route = resource_route(kind, identity)
        try:
            item = api(sock, "GET", route)
        except Exception as exc:
            if is_not_found(exc):
                return {"kind": kind, "identity": identity, "pass": True, "already_absent": True}
            raise
        verify_owned(item, kind, identity, token)
        api(sock, "DELETE", route.removesuffix("/json") + "?force=true")
        try:
            api(sock, "GET", route)
        except Exception as exc:
            if is_not_found(exc):
                return {"kind": kind, "identity": identity, "pass": True}
            raise
        raise c.ConformanceError(f"{kind} remained after cleanup: {identity}")

    try:
        if c.resource_exists(runner, "image", image):
            raise c.ConformanceError("audit image tag collision")
        image_attempted = True
        fixture = c.build_image(runner, output, image, token)
        image_id = fixture["inspect"][0]["Id"]
        with (output / "api-service.log").open("w") as log:
            service = subprocess.Popen(["podman", "system", "service", "--time", "0", endpoint],
                                       cwd=c.ROOT, stdout=log, stderr=log)
        for _ in range(100):
            if sock.exists():
                break
            if service.poll() is not None:
                raise c.ConformanceError("attempt-owned compatibility service exited")
            time.sleep(0.05)
        observe(operations, "positive-engine-identity", lambda: api(sock, "GET", "/version"))
        version = operations["positive-engine-identity"].get("evidence", {})
        if "Podman" not in json.dumps(version.get("Components", [])):
            raise c.ConformanceError("scoped endpoint did not positively identify Podman")
        observe(operations, "image-inspect-immutable-id", lambda: json.loads(dk("image", "inspect", image_id)))
        # Audit the actual product gate, before any BuildKit bootstrap. Running
        # the raw client build command bypasses this gate and can create a
        # privileged shared builder outside this attempt's cleanup authority.
        observe(operations, "existing-build-preflight", lambda: runner.run(
            ["go", "run", "./tools/conformance/multiidentity/build_preflight"], env=env).stdout)
        operations["existing-build-command"] = {
            "disposition": "blocked",
            "error": "Not launched: the product preflight rejects this engine; a prior exploratory bypass bootstrapped a shared privileged BuildKit container. An attempt-owned builder and supported RUN-mount/frontend evidence are required before auditing builds.",
        }

        volume = f"reploy-mi-audit-{token}-volume"
        audit_volume(sock, volume, token, resources, attempts, operations,
                     lambda: json.loads(dk("volume", "inspect", volume)))

        for profile in ("bounded-auto", "exact-sparse-candidate", "bounded-fixed-candidate"):
            name = f"reploy-mi-audit-{token}-{profile}"
            nonce = secrets.token_hex(16)
            host = {"NetworkMode": "none", "ReadonlyRootfs": True,
                    "Tmpfs": {"/run": "rw,noexec,nosuid,nodev,size=4m,mode=1777"},
                    "PidsLimit": 128, "Memory": 134217728, "CapDrop": ["ALL"],
                    "SecurityOpt": ["no-new-privileges"], "UsernsMode": "auto:size=4096"}
            if profile == "exact-sparse-candidate":
                host["UsernsMode"] = "private"
                host["IDMappings"] = {
                    "UidMap": [{"ContainerID": row[0], "HostID": row[1], "Size": row[2]}
                               for row in [(0, 1, 1), (100, 2, 1), (1001, 3, 1)]],
                    "GidMap": [{"ContainerID": row[0], "HostID": row[1], "Size": row[2]}
                               for row in [(0, 1, 1), (5, 2, 1), (100, 3, 1), (2001, 4, 1), (3001, 5, 2)]],
                }
            if profile == "bounded-fixed-candidate":
                host["UsernsMode"] = "auto:size=4096,uidmapping=0:1:4096,gidmapping=0:1:4096"
            body = {"Image": image_id, "Entrypoint": ["/probe"],
                    "Cmd": ["admission-wait", nonce, "cap-report"], "User": "0:0",
                    "Labels": {c.OWNER_LABEL: token}, "HostConfig": host}
            c.write_json(output / f"{profile}-request.json", body)

            def create_and_admit():
                value = tracked_create(
                    sock, "container", name, token,
                    lambda: api(sock, "POST", "/containers/create?name=" + quote(name), body),
                    resources, attempts,
                )
                identity = value["Id"]
                if len(identity) != 64 or any(ch not in "0123456789abcdef" for ch in identity):
                    raise c.ConformanceError("compatibility create returned an invalid ID")
                def inspect_created():
                    created = api(sock, "GET", f"/containers/{identity}/json")
                    if created.get("Config", {}).get("Labels", {}).get(c.OWNER_LABEL) != token:
                        raise c.ConformanceError("compatibility create returned a foreign container")
                    return created

                tracked_post_create_step(
                    sock, "container", name, identity, token, resources, attempts, inspect_created)
                dk("start", identity)
                c.wait_admission(runner, identity, nonce)  # independent observation only
                item = json.loads(dk("inspect", identity))[0]
                pid = int(item["State"]["Pid"])
                uid_map = c.parse_id_map(c.read_proc(pid, "uid_map"))
                gid_map = c.parse_id_map(c.read_proc(pid, "gid_map"))
                c.assert_mapping_profile("exact" if profile.startswith("exact") else "bounded", [uid_map], [gid_map])
                c.assert_host_authority(runner, [uid_map], [gid_map])
                if profile == "bounded-fixed-candidate" and (uid_map != [(0, uid_start, 4096)] or gid_map != [(0, gid_start, 4096)]):
                    raise c.ConformanceError(f"fixed reservation was not honored: {uid_map}, {gid_map}")
                if profile == "bounded-auto":
                    previous_ready_count = runner.run(
                        ["podman", "logs", identity]).stdout.splitlines().count(
                            f"admission-ready {nonce}")
                    operations["explicit-stop-start-restart"] = {"disposition": "observed-compatible", "evidence": dk("restart", identity)}
                    after = json.loads(dk("inspect", identity))[0]
                    after_pid = int(after["State"]["Pid"])
                    if c.parse_id_map(c.read_proc(after_pid, "uid_map")) != uid_map or c.parse_id_map(c.read_proc(after_pid, "gid_map")) != gid_map:
                        raise c.ConformanceError("restart changed effective identity mappings")
                    wait_restarted_admission(runner, identity, nonce, previous_ready_count)
                    dk("exec", identity, "/probe", "admission-release", nonce)
                    if dk("wait", identity).strip() != "0":
                        raise c.ConformanceError("admitted compatible control payload failed")
                    operations["exec-wait-logs"] = {"disposition": "observed-compatible", "evidence": dk("logs", identity)}
                return {"identity": identity, "uid_map": uid_map, "gid_map": gid_map}

            first_resource = len(resources)
            observe(operations, profile, create_and_admit)
            # Release each candidate's allocation before testing another fixed
            # reservation. Rejections leave the trusted waiter unreleased.
            for kind, identity in reversed(resources[first_resource:]):
                results["cleanup"].append(remove(kind, identity))
            del resources[first_resource:]

        network = f"reploy-mi-audit-{token}-network"
        def create_network():
            value = tracked_create(
                sock, "network", network, token,
                lambda: {"Id": dk("network", "create", "--internal", "--label", f"{c.OWNER_LABEL}={token}", network).strip()},
                resources, attempts,
            )
            identity = value["Id"]
            return tracked_post_create_step(
                sock, "network", network, identity, token, resources, attempts,
                lambda: json.loads(dk("network", "inspect", identity)))
        observe(operations, "network-create-inspect", create_network)
        operations["supervisor-auto-restart-ownership"] = {"disposition": "blocked", "error": "Explicit restart is transport evidence only; the approved trusted supervisor/allocator launch and revocation fence are not implemented."}
        operations["network-endpoint-policy"] = {"disposition": "blocked", "error": "Network CRUD does not prove Reploy nftables/controller endpoint and egress enforcement against Podman; C/G design and supported workload fixture remain required."}
    except Exception as exc:
        results["error"] = f"{type(exc).__name__}: {exc}"
    finally:
        for kind, identity in cleanup_targets(reversed(resources), reversed(attempts)):
            try:
                results["cleanup"].append(remove(kind, identity))
            except Exception as exc:
                results["cleanup"].append({"kind": kind, "identity": identity, "pass": False, "error": str(exc)})
        if service is not None:
            service.terminate()
            try:
                service.wait(timeout=10)
            except subprocess.TimeoutExpired:
                service.kill()
                service.wait(timeout=10)
        for tag in ([image] if image_attempted else []):
            try:
                results["cleanup"].append({"image": tag, **c.remove_owned_resource(runner, "image", tag, token)})
            except Exception as exc:
                results["cleanup"].append({"image": tag, "pass": False, "error": str(exc)})
        if sock.exists():
            sock.unlink()
        results["inventory_before"] = baseline
        results["inventory_after"] = c.inventory(runner, "podman")
        results["engine_restored"] = results["inventory_before"] == results["inventory_after"]
        results["elapsed_seconds"] = round(time.monotonic() - started, 3)
        c.write_json(output / "summary.json", results)
        runner.save_commands()
    return 0 if results["engine_restored"] and all(item["pass"] for item in results["cleanup"]) and "error" not in results else 1


if __name__ == "__main__":
    raise SystemExit(main())
