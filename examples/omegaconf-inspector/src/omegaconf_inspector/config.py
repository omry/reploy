from __future__ import annotations

import json
import os
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from omegaconf import OmegaConf


CONFIG_FILE_NAME = "inspector.yaml"


@dataclass(frozen=True)
class AppPaths:
    config_dir: Path
    data_dir: Path

    @property
    def config_file(self) -> Path:
        return self.config_dir / CONFIG_FILE_NAME


def resolve_paths(base_dir: str | Path | None = None) -> AppPaths:
    if base_dir is not None:
        root = Path(base_dir)
        return AppPaths(config_dir=root / "conf", data_dir=root / "data")

    config_dir = (
        os.environ.get("OMEGACONF_INSPECTOR_CONFIG_DIR")
        or os.environ.get("REPLOY_CONFIG_CONTAINER_DIR")
        or ("/conf" if Path("/conf").exists() else "conf")
    )
    data_dir = (
        os.environ.get("OMEGACONF_INSPECTOR_DATA_DIR")
        or ("/data" if Path("/data").exists() else "data")
    )
    return AppPaths(config_dir=Path(config_dir), data_dir=Path(data_dir))


def default_config(paths: AppPaths) -> str:
    database = paths.data_dir / "projects.sqlite"
    return (
        "server:\n"
        "  host: 0.0.0.0\n"
        "  port: 8076\n"
        "  title: OmegaConf Inspector\n"
        "\n"
        "storage:\n"
        f"  database: {database.as_posix()}\n"
    )


def init_config(base_dir: str | Path | None = None, *, force: bool = False) -> Path:
    paths = resolve_paths(base_dir)
    paths.config_dir.mkdir(parents=True, exist_ok=True)
    paths.data_dir.mkdir(parents=True, exist_ok=True)
    if paths.config_file.exists() and not force:
        raise FileExistsError(f"config already exists: {paths.config_file}")
    paths.config_file.write_text(default_config(paths), encoding="utf-8")
    return paths.config_file


def load_config(base_dir: str | Path | None = None) -> dict[str, Any]:
    paths = resolve_paths(base_dir)
    if not paths.config_file.exists():
        raise FileNotFoundError(f"missing config: {paths.config_file}")
    cfg = OmegaConf.load(paths.config_file)
    data = OmegaConf.to_container(cfg, resolve=True)
    if not isinstance(data, dict):
        raise ValueError("config root must be a mapping")
    _validate_config(data)
    return data


def show_config(base_dir: str | Path | None = None) -> str:
    paths = resolve_paths(base_dir)
    if not paths.config_file.exists():
        raise FileNotFoundError(f"missing config: {paths.config_file}")
    return paths.config_file.read_text(encoding="utf-8")


def config_summary(base_dir: str | Path | None = None) -> dict[str, Any]:
    paths = resolve_paths(base_dir)
    cfg = load_config(base_dir)
    return {
        "config": str(paths.config_file),
        "data": str(paths.data_dir),
        "database": str(database_path(cfg)),
        "title": cfg["server"]["title"],
    }


def database_path(cfg: dict[str, Any]) -> Path:
    storage = cfg.get("storage")
    if not isinstance(storage, dict):
        raise ValueError("storage must be a mapping")
    database = storage.get("database")
    if not isinstance(database, str) or not database:
        raise ValueError("storage.database must be a non-empty string")
    return Path(database)


def as_json(data: Any) -> str:
    return json.dumps(data, indent=2, sort_keys=True)


def _validate_config(data: dict[str, Any]) -> None:
    server = data.get("server")
    if not isinstance(server, dict):
        raise ValueError("server must be a mapping")
    host = server.get("host")
    if not isinstance(host, str) or not host:
        raise ValueError("server.host must be a non-empty string")
    port = server.get("port")
    if not isinstance(port, int) or not 1 <= port <= 65535:
        raise ValueError("server.port must be an integer between 1 and 65535")
    title = server.get("title")
    if not isinstance(title, str) or not title:
        raise ValueError("server.title must be a non-empty string")

    storage = data.get("storage")
    if not isinstance(storage, dict):
        raise ValueError("storage must be a mapping")
    database = storage.get("database")
    if not isinstance(database, str) or not database:
        raise ValueError("storage.database must be a non-empty string")
