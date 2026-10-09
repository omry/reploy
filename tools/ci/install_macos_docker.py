#!/usr/bin/env python3
"""Install checksum-pinned CI Docker clients and Lima/Colima without Homebrew."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import platform
import shutil
import subprocess
import tarfile
import tempfile


MANIFEST = Path(__file__).with_name("macos-docker-tools.json")


def install_tools(prefix: Path, architecture: str) -> None:
    tools = json.loads(MANIFEST.read_text())[architecture]
    prefix.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="reploy-macos-tools-") as temporary:
        for name, tool in tools.items():
            print(f"Installing {name}: {tool['url']}", flush=True)
            download = Path(temporary) / name
            subprocess.run(
                ["curl", "--fail", "--location", "--silent", "--show-error",
                 "--retry", "3", "--connect-timeout", "30", "--max-time", "300",
                 tool["url"], "--output", str(download)],
                check=True,
            )
            with download.open("rb") as source:
                digest = hashlib.file_digest(source, "sha256").hexdigest()
            if digest != tool["sha256"]:
                raise ValueError(f"{name}: SHA256 mismatch; refusing to install")
            if tool.get("archive") == "prefix":
                with tarfile.open(download) as archive:
                    # Lima's bin, libexec and share directories must retain
                    # their relative layout, including its guest agent.
                    archive.extractall(prefix, filter="data")
                continue
            target = prefix / tool["target"]
            target.parent.mkdir(parents=True, exist_ok=True)
            if "member" in tool:
                with tarfile.open(download) as archive:
                    source = archive.extractfile(tool["member"])
                    if source is None:
                        raise ValueError(f"{name}: missing archive member")
                    with source, target.open("wb") as output:
                        shutil.copyfileobj(source, output)
            else:
                shutil.copyfile(download, target)
            target.chmod(0o755)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--prefix", type=Path, required=True)
    args = parser.parse_args()
    if platform.system() != "Darwin" or platform.machine() not in ("x86_64", "arm64"):
        parser.error("requires native Intel or ARM64 macOS")
    install_tools(args.prefix.resolve(), platform.machine())


if __name__ == "__main__":
    main()
