from __future__ import annotations

import os
import platform
from pathlib import Path
import sys
import tempfile

import nox


nox.options.sessions = ["ci"]

DIST_CI = Path("dist-ci")

BUILD_DEPENDENCIES = (
    "build",
    "hatchling",
    "editables",
)

PY_COMPILE_FILES = (
    "tools/build_reploy",
    "tools/build_release_dists",
    "tools/build_release_notes",
    "tools/e2e/smoke",
    "packaging/python/hatch_build.py",
    "tests/e2e/python/packages/smoke-suite/src/smoke_suite/cli.py",
    "tests/e2e/python/packages/smoke-suite/src/smoke_suite/__init__.py",
    "tests/e2e/python/packages/smoke-imap/src/smoke_imap/__init__.py",
)


def _go_test(session: nox.Session) -> None:
    default_go_cache = Path(tempfile.gettempdir()) / "reploy-go-cache"
    env = {"GOCACHE": os.environ.get("GOCACHE", str(default_go_cache))}
    session.run("go", "test", "-timeout", "2m", "./...", env=env, external=True)


def _current_target_label() -> str:
    goos_by_platform = {
        "darwin": "darwin",
        "linux": "linux",
        "win32": "windows",
        "cygwin": "windows",
        "msys": "windows",
    }
    goarch_by_machine = {
        "amd64": "amd64",
        "x86_64": "amd64",
        "aarch64": "arm64",
        "arm64": "arm64",
    }
    goos = goos_by_platform.get(sys.platform)
    goarch = goarch_by_machine.get(platform.machine().lower())
    if goos is None or goarch is None:
        raise RuntimeError(
            f"could not infer current Reploy target from "
            f"{sys.platform!r}/{platform.machine()!r}"
        )
    return f"{goos}-{goarch}"


def _host_reploy_binary(bin_dir: Path) -> Path:
    binary_name = "reploy.exe" if sys.platform.startswith(("win32", "cygwin", "msys")) else "reploy"
    return bin_dir / _current_target_label() / binary_name


def _cli_smoke(session: nox.Session, *extra_args: str) -> None:
    with tempfile.TemporaryDirectory(prefix="reploy-cli-smoke-build-") as temp_dir:
        bin_dir = Path(temp_dir) / "bin"
        session.run(
            sys.executable,
            "tools/build_reploy",
            "--outdir",
            str(bin_dir),
            external=True,
        )
        session.run(
            sys.executable,
            "tools/e2e/smoke",
            "--reploy",
            str(_host_reploy_binary(bin_dir)),
            *extra_args,
            external=True,
        )


def _install_release_build_dependencies(session: nox.Session) -> None:
    session.install(*BUILD_DEPENDENCIES)


def _release_build_smoke(session: nox.Session) -> None:
    session.run("python", "-m", "py_compile", *PY_COMPILE_FILES)
    session.run(
        "python",
        "tools/build_release_dists",
        "--outdir",
        str(DIST_CI),
        "--clean",
        "--no-isolation",
    )


def _docs_build(session: nox.Session) -> None:
    with session.chdir("website"):
        session.run("npm", "ci", "--no-audit", "--no-fund", external=True)
        session.run("npm", "run", "sync:install", external=True)
        session.run("cmp", "../tools/install.sh", "static/install.sh", external=True)
        session.run("sh", "-n", "static/install.sh", external=True)
        session.run("npm", "run", "build", external=True)


@nox.session(name="go-test", python=False)
def go_test(session: nox.Session) -> None:
    _go_test(session)


@nox.session(name="cli-smoke", python=False)
def cli_smoke(session: nox.Session) -> None:
    _cli_smoke(session, *session.posargs)


@nox.session(name="cli-integration", python=False)
def cli_integration(session: nox.Session) -> None:
    _cli_smoke(session, "--runtime", *session.posargs)


@nox.session(name="release-build-smoke", python="3.12")
def release_build_smoke(session: nox.Session) -> None:
    _install_release_build_dependencies(session)
    _release_build_smoke(session)


@nox.session(name="docs-build", python=False)
def docs_build(session: nox.Session) -> None:
    _docs_build(session)


@nox.session(python="3.12")
def ci(session: nox.Session) -> None:
    _install_release_build_dependencies(session)
    _go_test(session)
    _cli_smoke(session)
    _release_build_smoke(session)
    _docs_build(session)
