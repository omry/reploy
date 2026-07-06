#!/usr/bin/env python3
"""Probe Docker Compose interrupt behavior for long-running commands."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import platform
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
from typing import NamedTuple
import uuid


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--keep-workdir",
        action="store_true",
        help="Keep the temporary Compose project for debugging.",
    )
    parser.add_argument(
        "--interrupt-delay",
        type=float,
        default=1.0,
        help="Seconds to wait after the test container appears before sending the interrupt.",
    )
    parser.add_argument(
        "--return-timeout",
        type=float,
        default=20.0,
        help="Maximum seconds to wait for Docker Compose to return after the interrupt.",
    )
    parser.add_argument(
        "--startup-timeout",
        type=float,
        default=40.0,
        help="Maximum seconds to wait for the test container to appear.",
    )
    parser.add_argument(
        "--include-up",
        action="store_true",
        help="Also compare docker compose up interrupt behavior.",
    )
    parser.add_argument(
        "--include-raw-compose",
        action="store_true",
        help="Also record raw docker compose run --rm behavior without Reploy-style cleanup.",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if shutil.which("docker") is None:
        print("[interrupt] docker command not found", file=sys.stderr)
        return 2

    workdir = Path(tempfile.mkdtemp(prefix="reploy-docker-interrupt-"))
    project = "reploy-int-" + uuid.uuid4().hex[:12]
    try:
        write_compose_file(workdir)

        print(f"[interrupt] project: {project}")
        print(f"[interrupt] workdir: {workdir}")
        print(f"[interrupt] host: {platform.platform()} ({sys.platform})")
        print(f"[interrupt] shell hint: {host_shell_hint()}")
        print(f"[interrupt] interrupt mechanism: {interrupt_mechanism()}")
        run_docker(["docker", "info"], workdir, timeout=20)

        results = []

        if args.include_raw_compose:
            results.append(
                run_interrupt_case(
                    name="raw-compose-run",
                    argv=compose_base(project, workdir)
                    + [
                        "run",
                        "--rm",
                        "--no-deps",
                        "app",
                        "sh",
                        "-c",
                        long_running_shell(),
                    ],
                    project=project,
                    workdir=workdir,
                    interrupt_delay=args.interrupt_delay,
                    startup_timeout=args.startup_timeout,
                    return_timeout=args.return_timeout,
                    cleanup_container="",
                )
            )
            cleanup_project(project, workdir)

        one_off_container = project + "-app-command-interrupt"
        results.append(
            run_interrupt_case(
                name="reploy-style-compose-run",
                argv=compose_base(project, workdir)
                + [
                    "run",
                    "--rm",
                    "--no-deps",
                    "--name",
                    one_off_container,
                    "app",
                    "sh",
                    "-c",
                    long_running_shell(),
                ],
                project=project,
                workdir=workdir,
                interrupt_delay=args.interrupt_delay,
                startup_timeout=args.startup_timeout,
                return_timeout=args.return_timeout,
                cleanup_container=one_off_container,
            )
        )
        cleanup_project(project, workdir)

        if args.include_up:
            results.append(
                run_interrupt_case(
                    name="compose-up",
                    argv=compose_base(project, workdir) + ["up"],
                    project=project,
                    workdir=workdir,
                    interrupt_delay=args.interrupt_delay,
                    startup_timeout=args.startup_timeout,
                    return_timeout=args.return_timeout,
                    cleanup_container="",
                )
            )
            cleanup_project(project, workdir)

        print_summary(results)
        return 0
    finally:
        cleanup_project(project, workdir)
        if args.keep_workdir:
            kept = Path(tempfile.gettempdir()) / ("kept-" + workdir.name)
            if kept.exists():
                shutil.rmtree(kept)
            shutil.copytree(workdir, kept)
            print(f"[interrupt] kept workdir copy: {kept}")
        shutil.rmtree(workdir, ignore_errors=True)


def compose_base(project: str, workdir: Path) -> list[str]:
    return [
        "docker",
        "compose",
        "--project-name",
        project,
        "--project-directory",
        str(workdir),
        "-f",
        str(workdir / "compose.yaml"),
    ]


def write_compose_file(workdir: Path) -> None:
    (workdir / "compose.yaml").write_text(
        "\n".join(
            [
                "services:",
                "  app:",
                "    image: busybox:1.36",
                "    command:",
                "      - sh",
                "      - -c",
                f"      - {long_running_shell()!r}",
                "",
            ]
        ),
        encoding="utf-8",
    )


def long_running_shell() -> str:
    return "trap 'exit 130' INT TERM; echo reploy-interrupt-ready; while true; do sleep 1; done"


def run_interrupt_case(
    *,
    name: str,
    argv: list[str],
    project: str,
    workdir: Path,
    interrupt_delay: float,
    startup_timeout: float,
    return_timeout: float,
    cleanup_container: str,
) -> dict[str, object]:
    print("+ " + " ".join(argv), flush=True)
    process = start_process(argv, workdir)
    capture = stream_process_output(process, name)
    try:
        wait_for_project_container(project, workdir, startup_timeout)
        time.sleep(interrupt_delay)
        interrupt_started = time.monotonic()
        send_interrupt(process)
        try:
            return_code = process.wait(timeout=return_timeout)
        except subprocess.TimeoutExpired:
            terminate_process(process)
            raise SystemExit(f"[interrupt] {name} did not return within {return_timeout:.1f}s after interrupt")
        elapsed = time.monotonic() - interrupt_started
    finally:
        join_process_streams(capture)

    containers_before = docker_lines(
        [
            "docker",
            "ps",
            "-a",
            "--filter",
            f"label=com.docker.compose.project={project}",
            "--format",
            "{{.ID}} {{.Names}} {{.Status}}",
        ],
        workdir,
    )
    networks_before = docker_lines(
        [
            "docker",
            "network",
            "ls",
            "--filter",
            f"label=com.docker.compose.project={project}",
            "--format",
            "{{.ID}} {{.Name}}",
        ],
        workdir,
    )

    print(f"[interrupt] {name}: return code {return_code}; returned after {elapsed:.2f}s")
    print_observed("containers before cleanup", containers_before)
    print_observed("networks before cleanup", networks_before)

    if cleanup_container:
        print(f"[interrupt] removing named one-off container: {cleanup_container}")
        subprocess.run(
            ["docker", "container", "rm", "-f", cleanup_container],
            cwd=workdir,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )

    containers_after = docker_lines(
        [
            "docker",
            "ps",
            "-a",
            "--filter",
            f"label=com.docker.compose.project={project}",
            "--format",
            "{{.ID}} {{.Names}} {{.Status}}",
        ],
        workdir,
    )
    networks_after = docker_lines(
        [
            "docker",
            "network",
            "ls",
            "--filter",
            f"label=com.docker.compose.project={project}",
            "--format",
            "{{.ID}} {{.Name}}",
        ],
        workdir,
    )
    print_observed("containers after cleanup", containers_after)
    print_observed("networks after cleanup", networks_after)

    if cleanup_container and containers_after:
        raise SystemExit(f"[interrupt] {name} left container(s) after targeted cleanup:\n" + "\n".join(containers_after))

    return {
        "name": name,
        "return_code": return_code,
        "elapsed": elapsed,
        "containers_before": containers_before,
        "networks_before": networks_before,
        "containers_after": containers_after,
        "networks_after": networks_after,
        "stdout": "".join(capture.stdout_chunks),
        "stderr": "".join(capture.stderr_chunks),
    }


def start_process(argv: list[str], workdir: Path) -> subprocess.Popen[bytes]:
    if os.name == "nt":
        creationflags = subprocess.CREATE_NEW_PROCESS_GROUP
        return subprocess.Popen(
            argv,
            cwd=workdir,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            creationflags=creationflags,
        )
    return subprocess.Popen(
        argv,
        cwd=workdir,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        preexec_fn=os.setsid,
    )


def send_interrupt(process: subprocess.Popen[bytes]) -> None:
    print("[interrupt] sending interrupt", flush=True)
    if os.name == "nt":
        process.send_signal(signal.CTRL_BREAK_EVENT)
        return
    os.killpg(process.pid, signal.SIGINT)


def interrupt_mechanism() -> str:
    if os.name == "nt":
        return "Windows CTRL_BREAK_EVENT to a new process group"
    return "SIGINT to a POSIX process group"


def host_shell_hint() -> str:
    values = []
    for name in ("SHELL", "ComSpec", "PSModulePath"):
        value = os.environ.get(name)
        if value:
            values.append(f"{name}={value}")
    if not values:
        return "unknown"
    return "; ".join(values)


def terminate_process(process: subprocess.Popen[bytes]) -> None:
    if process.poll() is not None:
        return
    if os.name == "nt":
        process.terminate()
    else:
        os.killpg(process.pid, signal.SIGTERM)
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()


class StreamCapture(NamedTuple):
    stdout_chunks: list[str]
    stderr_chunks: list[str]
    threads: list[threading.Thread]


def stream_process_output(
    process: subprocess.Popen[bytes],
    name: str,
) -> StreamCapture:
    stdout_chunks: list[str] = []
    stderr_chunks: list[str] = []
    threads = [
        threading.Thread(
            target=stream_pipe,
            args=(process.stdout, sys.stdout, f"[{name}:stdout] ", stdout_chunks),
            daemon=True,
        ),
        threading.Thread(
            target=stream_pipe,
            args=(process.stderr, sys.stderr, f"[{name}:stderr] ", stderr_chunks),
            daemon=True,
        ),
    ]
    for thread in threads:
        thread.start()
    return StreamCapture(stdout_chunks, stderr_chunks, threads)


def stream_pipe(pipe, target, prefix: str, chunks: list[str]) -> None:
    if pipe is None:
        return
    while True:
        line = pipe.readline()
        if line == b"":
            return
        text = line.decode("utf-8", errors="replace")
        chunks.append(text)
        target.write(prefix + text)
        target.flush()


def join_process_streams(capture: StreamCapture) -> None:
    for thread in capture.threads:
        thread.join(timeout=5)


def wait_for_project_container(project: str, workdir: Path, timeout: float) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        containers = docker_lines(
            [
                "docker",
                "ps",
                "-a",
                "--filter",
                f"label=com.docker.compose.project={project}",
                "--format",
                "{{.ID}}",
            ],
            workdir,
        )
        if containers:
            return
        time.sleep(0.25)
    raise SystemExit(f"[interrupt] timed out waiting for Compose project container for {project}")


def docker_lines(argv: list[str], workdir: Path) -> list[str]:
    completed = run_docker(argv, workdir, timeout=20)
    return [line for line in completed.stdout.splitlines() if line.strip()]


def run_docker(argv: list[str], workdir: Path, *, timeout: float) -> subprocess.CompletedProcess[str]:
    completed = subprocess.run(
        argv,
        cwd=workdir,
        text=True,
        encoding="utf-8",
        errors="replace",
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=timeout,
        check=False,
    )
    if completed.returncode != 0:
        raise SystemExit(
            f"[interrupt] command failed with exit code {completed.returncode}: {' '.join(argv)}\n"
            f"--- stdout ---\n{completed.stdout}\n--- stderr ---\n{completed.stderr}"
        )
    return completed


def print_observed(label: str, values: list[str]) -> None:
    if not values:
        print(f"[interrupt] observed {label}: none")
        return
    print(f"[interrupt] observed {label}:")
    for value in values:
        print(f"[interrupt]   {value}")


def cleanup_project(project: str, workdir: Path) -> None:
    print("[interrupt] cleaning up Compose project")
    subprocess.run(
        compose_base(project, workdir) + ["down", "--remove-orphans", "--volumes", "--timeout", "1"],
        cwd=workdir,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    container_ids = docker_lines_optional(
        [
            "docker",
            "ps",
            "-aq",
            "--filter",
            f"label=com.docker.compose.project={project}",
        ],
        workdir,
    )
    if container_ids:
        subprocess.run(
            ["docker", "container", "rm", "-f", *container_ids],
            cwd=workdir,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )
    network_ids = docker_lines_optional(
        [
            "docker",
            "network",
            "ls",
            "-q",
            "--filter",
            f"label=com.docker.compose.project={project}",
        ],
        workdir,
    )
    if network_ids:
        subprocess.run(
            ["docker", "network", "rm", *network_ids],
            cwd=workdir,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )


def docker_lines_optional(argv: list[str], workdir: Path) -> list[str]:
    try:
        return docker_lines(argv, workdir)
    except (subprocess.SubprocessError, SystemExit):
        return []


def print_summary(results: list[dict[str, object]]) -> None:
    print("[interrupt] summary:")
    for result in results:
        print(
            f"[interrupt]   {result['name']}: return_code={result['return_code']} "
            f"return_after_interrupt={result['elapsed']:.2f}s "
            f"containers_before={len(result['containers_before'])} "
            f"containers_after={len(result['containers_after'])} "
            f"networks_before={len(result['networks_before'])} "
            f"networks_after={len(result['networks_after'])}"
        )


if __name__ == "__main__":
    raise SystemExit(main())
