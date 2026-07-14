#!/usr/bin/env python3
"""Verify interactive ``reploy shell`` behavior through a real Unix PTY."""

from __future__ import annotations

import argparse
import errno
import os
from pathlib import Path
import select
import shutil
import signal
import struct
import subprocess
import sys
import time


INITIAL_ROWS = 29
INITIAL_COLUMNS = 101
RESIZED_ROWS = 41
RESIZED_COLUMNS = 117


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--reploy", required=True, help="Path to the Reploy binary.")
    parser.add_argument("--dir", required=True, help="Prepared environment deployment directory.")
    parser.add_argument("--timeout", type=float, default=30.0, help="Per-operation timeout in seconds.")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if os.name != "posix":
        print(f"[shell-pty] skipped on {os.name}")
        return 0
    if shutil.which("docker") is None:
        raise SystemExit("[shell-pty] docker command not found")

    reploy = Path(args.reploy).resolve()
    deployment = Path(args.dir).resolve()
    if not reploy.is_file() or not os.access(reploy, os.X_OK):
        raise SystemExit(f"[shell-pty] Reploy binary is not executable: {reploy}")

    container_prefix = docker_env_value(deployment / ".reploy" / "docker.env", "REPLOY_CONTAINER_NAME")
    assert_no_shell_containers(container_prefix, deployment)

    env = os.environ.copy()
    env["TERM"] = "xterm-256color"
    session = PTYSession(
        [str(reploy), "shell", "--dir", str(deployment)],
        cwd=deployment,
        env=env,
        rows=INITIAL_ROWS,
        columns=INITIAL_COLUMNS,
    )
    primary_error: BaseException | None = None
    try:
        session.send(b"stty -echo\n")
        session.send(
            b"printf 'REPLOY_PTY_READY '; "
            b"if test -t 0 && test -t 1 && test -t 2; then printf 'tty '; else printf 'notty '; fi; "
            b"stty size\n"
        )
        session.wait_for(f"REPLOY_PTY_READY tty {INITIAL_ROWS} {INITIAL_COLUMNS}", args.timeout)
        wait_for_shell_container(container_prefix, deployment, args.timeout)

        verify_resize(session, args.timeout)

        session.send(b"printf 'REPLOY_PTY_SLEEP_READY\\n'; sleep 30\n")
        session.wait_for("REPLOY_PTY_SLEEP_READY", args.timeout)
        session.send(b"\x03")
        session.send(b"printf 'REPLOY_PTY_INTERRUPT %s\\n' \"$?\"\n")
        session.wait_for("REPLOY_PTY_INTERRUPT 130", args.timeout)

        session.send(b"exit 42\n")
        return_code = session.wait(args.timeout)
        if return_code != 42:
            raise AssertionError(
                f"shell exit status was {return_code}, expected 42\n--- output ---\n{session.output_text()}"
            )
        session.assert_terminal_restored()
        wait_for_no_shell_containers(container_prefix, deployment, args.timeout)
    except BaseException as error:
        primary_error = error
        raise
    finally:
        session.close()
        if primary_error is not None:
            cleanup_shell_containers(container_prefix, deployment)

    print("[shell-pty] verified TTY allocation")
    print(f"[shell-pty] verified resize propagation: {INITIAL_ROWS}x{INITIAL_COLUMNS} -> {RESIZED_ROWS}x{RESIZED_COLUMNS}")
    print("[shell-pty] verified Ctrl-C forwarding with status 130")
    print("[shell-pty] verified terminal restoration and shell exit status 42")
    print("[shell-pty] verified transient container cleanup")
    return 0


def verify_resize(session: "PTYSession", timeout: float) -> None:
    session.resize(RESIZED_ROWS, RESIZED_COLUMNS)
    deadline = time.monotonic() + timeout
    attempt = 0
    while time.monotonic() < deadline:
        attempt += 1
        marker = f"REPLOY_PTY_RESIZE_{attempt}"
        session.send(f"printf '{marker} '; stty size\n".encode())
        line = session.wait_for_line(marker, min(2.0, max(0.1, deadline - time.monotonic())))
        if line.strip() == f"{marker} {RESIZED_ROWS} {RESIZED_COLUMNS}":
            return
        time.sleep(0.1)
    raise AssertionError(
        f"terminal resize did not reach the container\n--- output ---\n{session.output_text()}"
    )


class PTYSession:
    def __init__(
        self,
        argv: list[str],
        *,
        cwd: Path,
        env: dict[str, str],
        rows: int,
        columns: int,
    ) -> None:
        import fcntl
        import pty
        import termios

        self._fcntl = fcntl
        self._termios = termios
        self.master_fd, self.slave_fd = pty.openpty()
        self.slave_name = os.ttyname(self.slave_fd)
        self._initial_terminal = termios.tcgetattr(self.slave_fd)
        self._output = bytearray()
        self._search_offset = 0
        self._return_code: int | None = None
        self.resize(rows, columns)

        pid = os.fork()
        if pid == 0:
            try:
                os.setsid()
                fcntl.ioctl(self.slave_fd, termios.TIOCSCTTY, 0)
                os.dup2(self.slave_fd, 0)
                os.dup2(self.slave_fd, 1)
                os.dup2(self.slave_fd, 2)
                os.close(self.master_fd)
                if self.slave_fd > 2:
                    os.close(self.slave_fd)
                os.chdir(cwd)
                os.execve(argv[0], argv, env)
            except BaseException as error:
                os.write(2, f"shell PTY child setup failed: {error}\n".encode(errors="replace"))
                os._exit(127)
        self.pid = pid
        os.set_blocking(self.master_fd, False)

    def send(self, content: bytes) -> None:
        view = memoryview(content)
        while view:
            written = os.write(self.master_fd, view)
            view = view[written:]

    def resize(self, rows: int, columns: int) -> None:
        winsize = struct.pack("HHHH", rows, columns, 0, 0)
        self._fcntl.ioctl(self.master_fd, self._termios.TIOCSWINSZ, winsize)

    def wait_for(self, expected: str, timeout: float) -> None:
        expected_bytes = expected.encode()
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if expected_bytes in self._output[self._search_offset :]:
                self._search_offset = self._output.find(expected_bytes, self._search_offset) + len(expected_bytes)
                return
            self._read_once(min(0.2, max(0.0, deadline - time.monotonic())))
            if self.poll() is not None:
                self._drain()
                break
        raise AssertionError(f"did not observe {expected!r}\n--- output ---\n{self.output_text()}")

    def wait_for_line(self, marker: str, timeout: float) -> str:
        deadline = time.monotonic() + timeout
        marker_bytes = marker.encode()
        while time.monotonic() < deadline:
            data = bytes(self._output[self._search_offset :])
            marker_index = data.find(marker_bytes)
            if marker_index >= 0:
                newline_index = data.find(b"\n", marker_index)
                if newline_index >= 0:
                    absolute_end = self._search_offset + newline_index + 1
                    line = data[marker_index : newline_index + 1].decode("utf-8", errors="replace")
                    self._search_offset = absolute_end
                    return line.rstrip("\r\n")
            self._read_once(min(0.2, max(0.0, deadline - time.monotonic())))
            if self.poll() is not None:
                self._drain()
                break
        raise AssertionError(f"did not observe a complete {marker!r} line\n--- output ---\n{self.output_text()}")

    def poll(self) -> int | None:
        if self._return_code is not None:
            return self._return_code
        waited, status = os.waitpid(self.pid, os.WNOHANG)
        if waited == 0:
            return None
        self._return_code = os.waitstatus_to_exitcode(status)
        return self._return_code

    def wait(self, timeout: float) -> int:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            self._read_once(0.1)
            return_code = self.poll()
            if return_code is not None:
                self._drain()
                return return_code
        self.terminate()
        raise AssertionError(f"shell did not exit within {timeout:.1f}s\n--- output ---\n{self.output_text()}")

    def assert_terminal_restored(self) -> None:
        try:
            current = self._termios.tcgetattr(self.slave_fd)
        except self._termios.error:
            # macOS may revoke the original slave descriptor when its
            # controlling session exits. Reopening the same PTY exposes the
            # terminal state Docker restored before returning.
            reopened = os.open(self.slave_name, os.O_RDWR | os.O_NOCTTY)
            try:
                current = self._termios.tcgetattr(reopened)
            finally:
                os.close(reopened)
        if current != self._initial_terminal:
            raise AssertionError(
                "host terminal attributes were not restored\n"
                f"before={self._initial_terminal!r}\nafter={current!r}"
            )

    def output_text(self) -> str:
        return self._output.decode("utf-8", errors="replace")

    def terminate(self) -> None:
        if self.poll() is not None:
            return
        try:
            os.killpg(self.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        deadline = time.monotonic() + 5.0
        while time.monotonic() < deadline:
            if self.poll() is not None:
                return
            time.sleep(0.05)
        try:
            os.killpg(self.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        os.waitpid(self.pid, 0)

    def close(self) -> None:
        self.terminate()
        os.close(self.master_fd)
        os.close(self.slave_fd)

    def _read_once(self, timeout: float) -> None:
        readable, _, _ = select.select([self.master_fd], [], [], timeout)
        if not readable:
            return
        try:
            content = os.read(self.master_fd, 65536)
        except OSError as error:
            if error.errno == errno.EIO:
                return
            raise
        self._output.extend(content)

    def _drain(self) -> None:
        while True:
            before = len(self._output)
            self._read_once(0.02)
            if len(self._output) == before:
                return


def docker_env_value(path: Path, key: str) -> str:
    prefix = key + "="
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.startswith(prefix):
            value = line[len(prefix) :].strip()
            if value:
                return value
    raise SystemExit(f"[shell-pty] missing {key} in {path}")


def shell_containers(prefix: str, cwd: Path) -> list[str]:
    completed = subprocess.run(
        ["docker", "ps", "-a", "--filter", f"name={prefix}-command-", "--format", "{{.Names}}"],
        cwd=cwd,
        text=True,
        encoding="utf-8",
        errors="replace",
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=20,
        check=False,
    )
    if completed.returncode != 0:
        raise SystemExit(f"[shell-pty] docker ps failed: {completed.stderr.strip()}")
    return [line.strip() for line in completed.stdout.splitlines() if line.strip()]


def assert_no_shell_containers(prefix: str, cwd: Path) -> None:
    names = shell_containers(prefix, cwd)
    if names:
        raise SystemExit("[shell-pty] stale shell container(s) before test: " + ", ".join(names))


def wait_for_shell_container(prefix: str, cwd: Path, timeout: float) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if shell_containers(prefix, cwd):
            return
        time.sleep(0.1)
    raise AssertionError("Reploy shell container did not appear")


def wait_for_no_shell_containers(prefix: str, cwd: Path, timeout: float) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        names = shell_containers(prefix, cwd)
        if not names:
            return
        time.sleep(0.1)
    raise AssertionError("Reploy shell left transient container(s): " + ", ".join(shell_containers(prefix, cwd)))


def cleanup_shell_containers(prefix: str, cwd: Path) -> None:
    names = shell_containers(prefix, cwd)
    if not names:
        return
    subprocess.run(
        ["docker", "container", "rm", "-f", *names],
        cwd=cwd,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )


if __name__ == "__main__":
    raise SystemExit(main())
