from __future__ import annotations

import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest
from unittest import mock

import hatch_build


class ReployBuildHookTests(unittest.TestCase):
    def test_source_build_replaces_existing_binary_in_all_build_modes(self) -> None:
        for version in ("editable", "standard"):
            with self.subTest(version=version), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                package_root = root / "packaging" / "python"
                package_root.mkdir(parents=True)
                (root / "go.mod").write_text("module example.invalid/reploy\n")
                command = root / "cmd" / "reploy" / "main.go"
                command.parent.mkdir(parents=True)
                command.write_text("package main\n")

                binary = root / "dist" / "linux-amd64" / "reploy"
                binary.parent.mkdir(parents=True)
                binary.write_text("stale", encoding="utf-8")
                build_dir = root / "build"

                def build(
                    *, repo_root: Path, target: str, binary_name: str
                ) -> Path:
                    self.assertEqual(repo_root, root)
                    self.assertEqual(target, "linux-amd64")
                    self.assertEqual(binary_name, "reploy")
                    binary.write_text("current", encoding="utf-8")
                    return binary

                hook = hatch_build.ReployBuildHook(
                    str(package_root), {}, None, None, str(build_dir), "wheel"
                )
                build_data: dict[str, object] = {}
                with (
                    mock.patch.dict(
                        os.environ,
                        {"REPLOY_TARGET": "linux-amd64", "REPLOY_BINARY": ""},
                    ),
                    mock.patch.object(
                        hatch_build, "_build_reploy_binary", side_effect=build
                    ) as build_binary,
                ):
                    hook.initialize(version, build_data)

                build_binary.assert_called_once_with(
                    repo_root=root, target="linux-amd64", binary_name="reploy"
                )
                self.assertEqual(binary.read_text(encoding="utf-8"), "current")

                scripts = build_data["shared_scripts"]
                self.assertIsInstance(scripts, dict)
                script_source = Path(next(iter(scripts)))
                if version == "editable":
                    self.assertNotEqual(script_source, binary)
                    self.assertIn(
                        str(binary), script_source.read_text(encoding="utf-8")
                    )
                else:
                    self.assertEqual(script_source, binary)

    def test_explicit_binary_override_does_not_build_from_source(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            binary = root / "provided-reploy"
            binary.write_text("provided", encoding="utf-8")
            hook = hatch_build.ReployBuildHook(
                str(root), {}, None, None, str(root / "build"), "wheel"
            )
            build_data: dict[str, object] = {}

            with (
                mock.patch.dict(
                    os.environ,
                    {
                        "REPLOY_TARGET": "linux-amd64",
                        "REPLOY_BINARY": str(binary),
                    },
                ),
                mock.patch.object(hatch_build, "_build_reploy_binary") as build_binary,
            ):
                hook.initialize("standard", build_data)

            build_binary.assert_not_called()
            self.assertEqual(build_data["shared_scripts"], {str(binary): "reploy"})

    def test_missing_staged_output_preserves_existing_binary(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            binary = root / "dist" / "linux-amd64" / "reploy"
            binary.parent.mkdir(parents=True)
            binary.write_text("stale", encoding="utf-8")

            with mock.patch.object(subprocess, "run") as run:
                with self.assertRaisesRegex(
                    RuntimeError, "did not create the expected binary"
                ):
                    hatch_build._build_reploy_binary(
                        repo_root=root,
                        target="linux-amd64",
                        binary_name="reploy",
                    )

            run.assert_called_once()
            self.assertEqual(binary.read_text(encoding="utf-8"), "stale")


class ReploySourceInstallTests(unittest.TestCase):
    def test_editable_and_ordinary_installs_replace_stale_dist_binary(self) -> None:
        for mode in ("editable", "ordinary"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                package_root = root / "packaging" / "python"
                package_root.mkdir(parents=True)
                source_package_root = Path(hatch_build.__file__).parent
                for name in ("hatch_build.py", "pyproject.toml", "README.md"):
                    shutil.copy2(source_package_root / name, package_root / name)

                (root / "VERSION").write_text("0.7.0.dev1\n", encoding="utf-8")
                (root / "go.mod").write_text(
                    "module example.invalid/reploy\n", encoding="utf-8"
                )
                command = root / "cmd" / "reploy" / "main.go"
                command.parent.mkdir(parents=True)
                command.write_text("package main\n", encoding="utf-8")

                build_tool = root / "tools" / "build_reploy"
                build_tool.parent.mkdir()
                build_tool.write_text(
                    textwrap.dedent(
                        """\
                        import argparse
                        from pathlib import Path

                        parser = argparse.ArgumentParser()
                        parser.add_argument("--root", required=True)
                        parser.add_argument("--outdir", required=True)
                        parser.add_argument("--target", required=True)
                        args = parser.parse_args()
                        binary = Path(args.outdir) / args.target / "reploy"
                        binary.parent.mkdir(parents=True, exist_ok=True)
                        binary.write_text(
                            "#!/usr/bin/env sh\\nprintf current\\n", encoding="utf-8"
                        )
                        binary.chmod(0o755)
                        """
                    ),
                    encoding="utf-8",
                )

                binary = root / "dist" / "linux-amd64" / "reploy"
                binary.parent.mkdir(parents=True)
                binary.write_text(
                    "#!/usr/bin/env sh\nprintf stale\n", encoding="utf-8"
                )
                binary.chmod(0o755)

                env = os.environ.copy()
                env.pop("REPLOY_BINARY", None)
                env["REPLOY_TARGET"] = "linux-amd64"
                install_dir = root / f"{mode}-install"
                invocation = [
                    sys.executable,
                    "-m",
                    "pip",
                    "install",
                    "--no-build-isolation",
                    "--no-deps",
                    "--target",
                    str(install_dir),
                ]
                if mode == "editable":
                    invocation.append("--editable")
                invocation.append(str(package_root))

                result = subprocess.run(
                    invocation,
                    cwd=root,
                    env=env,
                    check=False,
                    text=True,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT,
                )
                self.assertEqual(result.returncode, 0, result.stdout)
                self.assertEqual(
                    binary.read_text(encoding="utf-8"),
                    "#!/usr/bin/env sh\nprintf current\n",
                )

                launcher = install_dir / "bin" / "reploy"
                self.assertTrue(launcher.is_file(), result.stdout)
                completed = subprocess.run(
                    [str(launcher)],
                    check=False,
                    text=True,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT,
                )
                self.assertEqual(completed.returncode, 0, completed.stdout)
                self.assertEqual(completed.stdout, "current")


if __name__ == "__main__":
    unittest.main()
