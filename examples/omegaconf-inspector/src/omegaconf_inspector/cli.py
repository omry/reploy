from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from . import __version__
from .config import as_json, config_summary, database_path, init_config, load_config, show_config
from .server import health_check, serve
from .storage import ProjectStore


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        return args.func(args)
    except FileExistsError as exc:
        print(f"omegaconf-inspector error: {exc}; use --force to replace it", file=sys.stderr)
        return 1
    except Exception as exc:
        print(f"omegaconf-inspector error: {exc}", file=sys.stderr)
        return 1


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="omegaconf-inspector")
    parser.add_argument("--version", action="store_true", help="Print the app version and exit.")
    subparsers = parser.add_subparsers(dest="command")

    serve_parser = subparsers.add_parser("serve", help="Run the web service.")
    add_dir(serve_parser)
    serve_parser.add_argument("--host")
    serve_parser.add_argument("--port", type=int)
    serve_parser.set_defaults(func=serve_command)

    config_parser = subparsers.add_parser("config", help="Manage service config.")
    config_sub = config_parser.add_subparsers(dest="config_command")
    init_parser = config_sub.add_parser("init", help="Create conf/inspector.yaml.")
    add_dir(init_parser)
    init_parser.add_argument("--force", action="store_true")
    init_parser.set_defaults(func=config_init_command)
    check_parser = config_sub.add_parser("check", help="Validate service config.")
    add_dir(check_parser)
    check_parser.add_argument("--live", action="store_true")
    check_parser.set_defaults(func=config_check_command)
    show_parser = config_sub.add_parser("show", help="Print service config.")
    add_dir(show_parser)
    show_parser.set_defaults(func=config_show_command)

    project_parser = subparsers.add_parser("project", help="Inspect saved projects.")
    project_sub = project_parser.add_subparsers(dest="project_command")
    project_list = project_sub.add_parser("list", help="List projects.")
    add_dir(project_list)
    project_list.set_defaults(func=project_list_command)
    project_show = project_sub.add_parser("show", help="Show a project.")
    add_dir(project_show)
    project_show.add_argument("project_id")
    project_show.set_defaults(func=project_show_command)

    version_parser = subparsers.add_parser("version", help="Print the app version.")
    version_parser.set_defaults(func=version_command)

    parser.set_defaults(func=default_command)
    return parser


def add_dir(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--dir", default=None, help="Deployment root for local runs.")


def default_command(args: argparse.Namespace) -> int:
    if getattr(args, "version", False):
        return version_command(args)
    build_parser().print_help()
    return 0


def version_command(args: argparse.Namespace) -> int:
    print(f"omegaconf-inspector {__version__}")
    return 0


def serve_command(args: argparse.Namespace) -> int:
    serve(args.dir, host=args.host, port=args.port)
    return 0


def config_init_command(args: argparse.Namespace) -> int:
    path = init_config(args.dir, force=args.force)
    print(f"created config: {path}")
    return 0


def config_check_command(args: argparse.Namespace) -> int:
    summary = config_summary(args.dir)
    if args.live:
        summary.update(health_check(args.dir))
    print(as_json({"check": "pass", **summary}))
    return 0


def config_show_command(args: argparse.Namespace) -> int:
    print(show_config(args.dir), end="")
    return 0


def project_list_command(args: argparse.Namespace) -> int:
    store = _store(args.dir)
    print(as_json(store.list_projects()))
    return 0


def project_show_command(args: argparse.Namespace) -> int:
    store = _store(args.dir)
    print(as_json(store.get_project(args.project_id)))
    return 0


def _store(base_dir: str | Path | None) -> ProjectStore:
    cfg = load_config(base_dir)
    store = ProjectStore(database_path(cfg))
    store.init()
    return store
