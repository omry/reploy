from __future__ import annotations

import os
from importlib import resources
from pathlib import Path
from typing import Any

from fastapi import FastAPI, HTTPException
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel, Field

from . import __version__
from .config import AppPaths, config_summary, database_path, load_config, resolve_paths
from .merge import MergeLayer, merge_layers
from .storage import ProjectStore


MAX_FILE_BYTES = 100 * 1024


class ProjectIn(BaseModel):
    name: str = Field(min_length=1)
    ui_state: dict[str, Any] = Field(default_factory=dict)


class ProjectUpdate(BaseModel):
    name: str | None = None
    ui_state: dict[str, Any] | None = None


class FileIn(BaseModel):
    name: str = Field(min_length=1)
    content: str = ""
    order: int | None = None
    enabled: bool = True


class FileUpdate(BaseModel):
    name: str = Field(min_length=1)
    content: str = ""
    order: int = 0
    enabled: bool = True


class MergeIn(BaseModel):
    file_ids: list[str] | None = None
    path: str | None = None


def create_app(base_dir: str | Path | None = None) -> FastAPI:
    paths = resolve_paths(base_dir)
    cfg = load_config(base_dir)
    store = ProjectStore(database_path(cfg))
    title = cfg["server"]["title"]

    app = FastAPI(title=title, version=__version__)

    @app.get("/_health_")
    def health() -> dict[str, Any]:
        try:
            load_config(base_dir)
            store.health_check()
        except Exception as exc:
            raise HTTPException(status_code=503, detail=str(exc)) from exc
        return {
            "ok": True,
            "version": __version__,
            "config": str(paths.config_file),
            "database": str(store.database),
        }

    @app.get("/api/config")
    def api_config() -> dict[str, Any]:
        return config_summary(base_dir)

    @app.get("/api/projects")
    def list_projects() -> list[dict[str, Any]]:
        return store.list_projects()

    @app.post("/api/projects")
    def create_project(project: ProjectIn) -> dict[str, Any]:
        return store.create_project(project.name, project.ui_state)

    @app.get("/api/projects/{project_id}")
    def get_project(project_id: str) -> dict[str, Any]:
        return _or_404(lambda: store.get_project(project_id))

    @app.put("/api/projects/{project_id}")
    def update_project(project_id: str, project: ProjectUpdate) -> dict[str, Any]:
        return _or_404(
            lambda: store.update_project(
                project_id,
                name=project.name,
                ui_state=project.ui_state,
            )
        )

    @app.delete("/api/projects/{project_id}")
    def delete_project(project_id: str) -> dict[str, bool]:
        _or_404(lambda: store.delete_project(project_id))
        return {"ok": True}

    @app.get("/api/projects/{project_id}/files")
    def list_files(project_id: str) -> list[dict[str, Any]]:
        _or_404(lambda: store.get_project(project_id))
        return store.list_files(project_id)

    @app.post("/api/projects/{project_id}/files")
    def create_file(project_id: str, file: FileIn) -> dict[str, Any]:
        _validate_file_size(file.content)
        return _or_404(
            lambda: store.create_file(
                project_id,
                name=file.name,
                content=file.content,
                order=file.order,
                enabled=file.enabled,
            )
        )

    @app.put("/api/projects/{project_id}/files/{file_id}")
    def update_file(project_id: str, file_id: str, file: FileUpdate) -> dict[str, Any]:
        _validate_file_size(file.content)
        return _or_404(
            lambda: store.update_file(
                project_id,
                file_id,
                name=file.name,
                content=file.content,
                order=file.order,
                enabled=file.enabled,
            )
        )

    @app.delete("/api/projects/{project_id}/files/{file_id}")
    def delete_file(project_id: str, file_id: str) -> dict[str, bool]:
        _or_404(lambda: store.delete_file(project_id, file_id))
        return {"ok": True}

    @app.post("/api/projects/{project_id}/merge")
    def merge_project(project_id: str, request: MergeIn) -> dict[str, Any]:
        files = _selected_files(store, project_id, request.file_ids)
        return _merge_files(files, path=request.path)

    static_dir = resources.files("omegaconf_inspector").joinpath("static")
    app.mount("/", StaticFiles(directory=str(static_dir), html=True), name="static")
    return app


def serve(base_dir: str | Path | None = None, host: str | None = None, port: int | None = None) -> None:
    import uvicorn

    cfg = load_config(base_dir)
    selected_host = host or os.environ.get("REPLOY_CONTAINER_HOST") or cfg["server"]["host"]
    selected_port = int(os.environ.get("REPLOY_CONTAINER_PORT") or port or cfg["server"]["port"])
    uvicorn.run(create_app(base_dir), host=selected_host, port=selected_port, log_level="info")


def health_check(base_dir: str | Path | None = None) -> dict[str, Any]:
    cfg = load_config(base_dir)
    store = ProjectStore(database_path(cfg))
    store.health_check()
    return {"ok": True, "database": str(store.database)}


def _selected_files(
    store: ProjectStore,
    project_id: str,
    file_ids: list[str] | None,
) -> list[dict[str, Any]]:
    files = _or_404(lambda: store.list_files(project_id))
    if file_ids is None:
        return files
    wanted = set(file_ids)
    selected = [file for file in files if file["id"] in wanted]
    if len(selected) != len(wanted):
        found = {file["id"] for file in selected}
        missing = sorted(wanted - found)
        raise HTTPException(status_code=404, detail=f"unknown file ids: {', '.join(missing)}")
    order = {file_id: index for index, file_id in enumerate(file_ids)}
    return sorted(selected, key=lambda file: order[file["id"]])


def _merge_files(files: list[dict[str, Any]], *, path: str | None) -> dict[str, Any]:
    layers = [
        MergeLayer(name=file["name"], content=file["content"], enabled=file["enabled"])
        for file in files
    ]
    result = merge_layers(layers, path=path)
    result["layers"] = [
        {
            "id": file["id"],
            "name": file["name"],
            "enabled": file["enabled"],
            "order": file["order"],
        }
        for file in files
    ]
    return result


def _validate_file_size(content: str) -> None:
    if len(content.encode("utf-8")) > MAX_FILE_BYTES:
        raise HTTPException(status_code=413, detail="config file exceeds 100 KiB")


def _or_404(action):
    try:
        return action()
    except KeyError as exc:
        raise HTTPException(status_code=404, detail=str(exc)) from exc
