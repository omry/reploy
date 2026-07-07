from __future__ import annotations

import json
import sqlite3
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


def now() -> str:
    return datetime.now(timezone.utc).isoformat()


class ProjectStore:
    def __init__(self, database: Path):
        self.database = database
        self.database.parent.mkdir(parents=True, exist_ok=True)

    def connect(self) -> sqlite3.Connection:
        conn = sqlite3.connect(self.database)
        conn.row_factory = sqlite3.Row
        return conn

    def init(self) -> None:
        with self.connect() as conn:
            conn.executescript(
                """
                create table if not exists projects (
                  id text primary key,
                  name text not null,
                  ui_state text not null,
                  created_at text not null,
                  updated_at text not null
                );
                create table if not exists files (
                  id text primary key,
                  project_id text not null references projects(id) on delete cascade,
                  name text not null,
                  content text not null,
                  layer_order integer not null,
                  enabled integer not null,
                  created_at text not null,
                  updated_at text not null
                );
                """
            )

    def health_check(self) -> None:
        self.init()
        with self.connect() as conn:
            conn.execute("create table if not exists health_probe (id integer primary key)")
            conn.execute("insert into health_probe default values")
            conn.execute(
                "delete from health_probe where id not in "
                "(select id from health_probe order by id desc limit 1)"
            )

    def list_projects(self) -> list[dict[str, Any]]:
        self.init()
        with self.connect() as conn:
            rows = conn.execute(
                "select id, name, ui_state, created_at, updated_at from projects "
                "order by updated_at desc"
            ).fetchall()
        return [self._project_row(row) for row in rows]

    def create_project(self, name: str, ui_state: dict[str, Any] | None = None) -> dict[str, Any]:
        self.init()
        project_id = str(uuid.uuid4())
        timestamp = now()
        state = json.dumps(ui_state or {}, sort_keys=True)
        with self.connect() as conn:
            conn.execute(
                "insert into projects(id, name, ui_state, created_at, updated_at) "
                "values (?, ?, ?, ?, ?)",
                (project_id, name, state, timestamp, timestamp),
            )
        return self.get_project(project_id)

    def get_project(self, project_id: str) -> dict[str, Any]:
        self.init()
        with self.connect() as conn:
            project = conn.execute(
                "select id, name, ui_state, created_at, updated_at from projects where id = ?",
                (project_id,),
            ).fetchone()
        if project is None:
            raise KeyError(f"unknown project: {project_id}")
        result = self._project_row(project)
        result["files"] = self.list_files(project_id)
        return result

    def update_project(
        self,
        project_id: str,
        *,
        name: str | None = None,
        ui_state: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        self.init()
        current = self.get_project(project_id)
        new_name = name if name is not None else current["name"]
        new_state = ui_state if ui_state is not None else current["ui_state"]
        with self.connect() as conn:
            conn.execute(
                "update projects set name = ?, ui_state = ?, updated_at = ? where id = ?",
                (new_name, json.dumps(new_state, sort_keys=True), now(), project_id),
            )
        return self.get_project(project_id)

    def delete_project(self, project_id: str) -> None:
        self.init()
        with self.connect() as conn:
            project = conn.execute("select id from projects where id = ?", (project_id,)).fetchone()
            if project is None:
                raise KeyError(f"unknown project: {project_id}")
            conn.execute("delete from files where project_id = ?", (project_id,))
            cursor = conn.execute("delete from projects where id = ?", (project_id,))
            if cursor.rowcount == 0:
                raise KeyError(f"unknown project: {project_id}")

    def list_files(self, project_id: str) -> list[dict[str, Any]]:
        self.init()
        with self.connect() as conn:
            rows = conn.execute(
                "select id, project_id, name, content, layer_order, enabled, created_at, updated_at "
                "from files where project_id = ? order by layer_order, created_at",
                (project_id,),
            ).fetchall()
        return [self._file_row(row) for row in rows]

    def create_file(
        self,
        project_id: str,
        *,
        name: str,
        content: str,
        order: int | None = None,
        enabled: bool = True,
    ) -> dict[str, Any]:
        self.init()
        self.get_project(project_id)
        file_id = str(uuid.uuid4())
        timestamp = now()
        if order is None:
            order = len(self.list_files(project_id))
        with self.connect() as conn:
            conn.execute(
                "insert into files(id, project_id, name, content, layer_order, enabled, created_at, updated_at) "
                "values (?, ?, ?, ?, ?, ?, ?, ?)",
                (file_id, project_id, name, content, order, int(enabled), timestamp, timestamp),
            )
            conn.execute("update projects set updated_at = ? where id = ?", (timestamp, project_id))
        return self.get_file(project_id, file_id)

    def get_file(self, project_id: str, file_id: str) -> dict[str, Any]:
        self.init()
        with self.connect() as conn:
            row = conn.execute(
                "select id, project_id, name, content, layer_order, enabled, created_at, updated_at "
                "from files where project_id = ? and id = ?",
                (project_id, file_id),
            ).fetchone()
        if row is None:
            raise KeyError(f"unknown file: {file_id}")
        return self._file_row(row)

    def update_file(
        self,
        project_id: str,
        file_id: str,
        *,
        name: str,
        content: str,
        order: int,
        enabled: bool,
    ) -> dict[str, Any]:
        self.init()
        timestamp = now()
        with self.connect() as conn:
            cursor = conn.execute(
                "update files set name = ?, content = ?, layer_order = ?, enabled = ?, updated_at = ? "
                "where project_id = ? and id = ?",
                (name, content, order, int(enabled), timestamp, project_id, file_id),
            )
            if cursor.rowcount == 0:
                raise KeyError(f"unknown file: {file_id}")
            conn.execute("update projects set updated_at = ? where id = ?", (timestamp, project_id))
        return self.get_file(project_id, file_id)

    def delete_file(self, project_id: str, file_id: str) -> None:
        self.init()
        with self.connect() as conn:
            cursor = conn.execute(
                "delete from files where project_id = ? and id = ?",
                (project_id, file_id),
            )
            if cursor.rowcount == 0:
                raise KeyError(f"unknown file: {file_id}")
            conn.execute("update projects set updated_at = ? where id = ?", (now(), project_id))

    def _project_row(self, row: sqlite3.Row) -> dict[str, Any]:
        return {
            "id": row["id"],
            "name": row["name"],
            "ui_state": json.loads(row["ui_state"]),
            "created_at": row["created_at"],
            "updated_at": row["updated_at"],
        }

    def _file_row(self, row: sqlite3.Row) -> dict[str, Any]:
        return {
            "id": row["id"],
            "project_id": row["project_id"],
            "name": row["name"],
            "content": row["content"],
            "order": row["layer_order"],
            "enabled": bool(row["enabled"]),
            "created_at": row["created_at"],
            "updated_at": row["updated_at"],
        }
