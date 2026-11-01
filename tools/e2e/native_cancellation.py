#!/usr/bin/env python3
"""Bounded real-CLI native cancellation proof. Uses no credentials or live app."""

import argparse
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time
import uuid


PAYLOAD = r'''
import json, os, signal, subprocess, sys, time
from pathlib import Path
out = Path(os.environ["REPLOY_OUTPUT_DIR"])
mode = sys.argv[1]
if mode == "stdin":
    value = {"stdin": sys.stdin.read(), "argv": sys.argv[2:], "tty": sys.stdin.isatty()}
    (out / "result.json").write_text(json.dumps(value))
    print("NATIVE_STDOUT", flush=True)
    sys.exit(37)
child = subprocess.Popen(["/bin/sleep", "300"])
def finish(sig, frame):
    child.terminate()
    child.wait(timeout=2)
    time.sleep(1)
    value = {"cancelled": True, "signal": sig, "child_reaped": child.poll() is not None}
    tmp = out / "result.tmp"
    tmp.write_text(json.dumps(value))
    tmp.replace(out / "result.json")
    print("NATIVE_FINALIZED", flush=True)
    sys.exit(130)
for sig in (signal.SIGTERM, signal.SIGINT):
    signal.signal(sig, signal.SIG_IGN if mode == "ignore" else finish)
print("CANCEL_READY", flush=True)
while True:
    time.sleep(.1)
'''


def command(args, log, timeout=300):
    with log.open("w") as stream:
        result = subprocess.run(args, stdout=stream, stderr=subprocess.STDOUT, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"{args[1]} failed ({result.returncode}): {log.read_text()[-4000:]}")


def owned_containers(environment):
    result = subprocess.run(
        ["docker", "ps", "--all", "--filter", f"name={environment}-", "--format", "{{.ID}}"],
        check=True, capture_output=True, text=True, timeout=10,
    )
    return result.stdout.split()


def require_removed(containers):
    for container in containers:
        result = subprocess.run(
            ["docker", "container", "inspect", container], capture_output=True, text=True, timeout=10,
        )
        if result.returncode == 0 or not any(
            text in result.stderr for text in ("No such container", "No such object")
        ):
            raise AssertionError(f"container removal not verified: {container}: {result.stderr}")


def cancellation_case(executable, stage, root, environment, name, host_signal, group=False):
    output = root / name
    output.mkdir()
    log = output / "cli.log"
    containers = []
    with log.open("w") as stream:
        cli = subprocess.Popen(
            [executable, "app", "--dir", str(stage), "--output-dir", str(output), "run", "--",
             "ignore" if name == "forced" else "graceful"],
            stdin=subprocess.DEVNULL, stdout=stream, stderr=subprocess.STDOUT,
            start_new_session=group,
        )
        try:
            deadline = time.monotonic() + 40
            while "CANCEL_READY" not in log.read_text():
                if cli.poll() is not None or time.monotonic() > deadline:
                    raise AssertionError(f"workload did not become ready: {log.read_text()}")
                time.sleep(.05)
            containers = owned_containers(environment)
            if len(containers) != 1:
                raise AssertionError(f"expected one isolated fixture container: {containers}")
            start = time.monotonic()
            if group:
                os.killpg(cli.pid, host_signal)
            else:
                cli.send_signal(host_signal)
            code = cli.wait(timeout=45)
            elapsed = time.monotonic() - start
        finally:
            if cli.poll() is None:
                cli.kill()
                cli.wait(timeout=10)
    if code == 0:
        raise AssertionError("interrupted CLI reported success")
    require_removed(containers)
    result = output / "result.json"
    value = json.loads(result.read_text()) if result.exists() else None
    if name == "forced":
        if value is not None or elapsed >= 40:
            raise AssertionError(f"forced containment failed: {value}, elapsed={elapsed}")
    else:
        if value != {"cancelled": True, "signal": 15, "child_reaped": True}:
            raise AssertionError(f"application cleanup not established: {value}")
        if (output / "result.tmp").exists():
            raise AssertionError("result was not atomically published")
    return {"case": name, "exit_code": code, "seconds_after_interrupt": elapsed,
            "result": value, "removed_containers": containers}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--reploy", required=True, type=Path, help="packaged CLI with embedded runtime")
    parser.add_argument("--output-dir", required=True, type=Path, help="new evidence directory")
    parser.add_argument("--base-image", default="python:3.13-slim")
    args = parser.parse_args()
    if os.name != "posix":
        parser.error("this harness injects POSIX signals; it does not qualify native Windows interruption")
    executable = str(args.reploy.resolve(strict=True))
    root = args.output_dir.resolve()
    root.mkdir(parents=True, exist_ok=False)
    environment = "native-cancel-" + uuid.uuid4().hex[:10]
    with tempfile.TemporaryDirectory(prefix="reploy-native-cancel-") as work:
        workspace = Path(work)
        stage = workspace / "stage"
        blueprint = workspace / "fixture.blueprint.yaml"
        blueprint.write_text(f'''blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64, linux/arm64]
environment:
  id: {environment}
  base:
    image: {json.dumps(args.base_image)}
  applications:
    fixture:
      packages:
        os:
          - package: tini
            exports:
              launcher:
                executable: /usr/bin/tini
      executables:
        launcher:
          source: os
          binary: launcher
          argv_prefix: [--, /usr/local/bin/python, -c]
  commands:
    run:
      executable: fixture.launcher
      trigger: [run]
      native_command: true
      argv: [{json.dumps(PAYLOAD)}]
docker: {{}}
''')
        (root / "fixture.blueprint.yaml").write_bytes(blueprint.read_bytes())
        results = []
        try:
            command([executable, "stage", str(blueprint), "--dir", str(stage)], root / "stage.log")
            command([executable, "build", "--dir", str(stage)], root / "build.log")
            for name, sig, group in [("sigint", signal.SIGINT, False),
                                     ("sigterm", signal.SIGTERM, False),
                                     ("terminal-group", signal.SIGINT, True),
                                     ("forced", signal.SIGINT, False)]:
                results.append(cancellation_case(executable, stage, root, environment, name, sig, group))
                (root / "results.json").write_text(json.dumps(results, indent=2) + "\n")
            for name, data in [("stdin", "dummy input\nsecond line\n"), ("eof", "")]:
                output = root / name
                output.mkdir()
                args_tail = ["literal with spaces", "dollar-$value", "semi;colon"]
                run = subprocess.run(
                    [executable, "app", "--dir", str(stage), "--output-dir", str(output),
                     "run", "--", "stdin", *args_tail], input=data, capture_output=True, text=True, timeout=40,
                )
                (output / "cli.log").write_text(run.stdout + run.stderr)
                value = json.loads((output / "result.json").read_text())
                if run.returncode != 37 or value != {"stdin": data, "argv": args_tail, "tty": False}:
                    raise AssertionError(f"stdin/EOF/argv/exit-status regression: {run.returncode}, {value}")
                if "NATIVE_STDOUT" not in run.stdout or owned_containers(environment):
                    raise AssertionError("stdout or completed container cleanup regressed")
                results.append({"case": name, "exit_code": run.returncode, "result": value})
            (root / "results.json").write_text(json.dumps(results, indent=2) + "\n")
        finally:
            command([executable, "stage", "--remove", "--dir", str(stage), "--force"], root / "remove.log", timeout=60)
    print(json.dumps({"passed": len(results), "results": str(root / "results.json")}))


if __name__ == "__main__":
    main()
