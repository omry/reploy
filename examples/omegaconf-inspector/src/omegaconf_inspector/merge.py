from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any

from omegaconf import OmegaConf


@dataclass(frozen=True)
class MergeLayer:
    name: str
    content: str
    enabled: bool = True


def merge_layers(
    layers: list[MergeLayer],
    *,
    path: str | None = None,
) -> dict[str, Any]:
    enabled = [layer for layer in layers if layer.enabled]
    errors: list[str] = []
    configs = []

    for layer in enabled:
        try:
            configs.append(OmegaConf.create(layer.content or "{}"))
        except Exception as exc:  # OmegaConf exposes several parse exception types.
            errors.append(f"parse {layer.name}: {exc}")

    if errors:
        return {
            "ok": False,
            "merged_yaml": "",
            "resolved_yaml": "",
            "error": "\n".join(errors),
        }

    try:
        merged = OmegaConf.merge(*configs) if configs else OmegaConf.create({})
        output_config = OmegaConf.select(merged, path, throw_on_missing=False) if path else merged
        merged_yaml = _to_output_yaml(output_config, resolve=False)
    except Exception as exc:
        return _error("merge", str(exc))

    resolved_yaml = ""
    try:
        resolved_yaml = _to_output_yaml(output_config, resolve=True)
    except Exception as exc:
        errors.append(f"resolve: {exc}")

    return {
        "ok": not errors,
        "merged_yaml": merged_yaml,
        "resolved_yaml": resolved_yaml,
        "error": "\n".join(errors) if errors else None,
    }


def _error(phase: str, message: str) -> dict[str, Any]:
    return {
        "ok": False,
        "merged_yaml": "",
        "resolved_yaml": "",
        "error": f"{phase}: {message}",
    }


def _to_output_yaml(value: Any, *, resolve: bool) -> str:
    if OmegaConf.is_config(value):
        return OmegaConf.to_yaml(value, resolve=resolve)
    return json.dumps(value, indent=2) + "\n"
