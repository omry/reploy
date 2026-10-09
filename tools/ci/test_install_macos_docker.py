from __future__ import annotations

import hashlib
import io
import json
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import install_macos_docker as installer


class InstallMacOSDockerTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="reploy-ci-tools-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.prefix = self.root / "installed"
        self.manifest = self.root / "manifest.json"

    def archive(self, name, members):
        path = self.root / name
        with tarfile.open(path, "w:gz") as archive:
            for member, payload in members.items():
                info = tarfile.TarInfo(member)
                info.mode = 0o755
                info.size = len(payload)
                archive.addfile(info, io.BytesIO(payload))
        return path

    def run_install(self, entries):
        self.manifest.write_text(json.dumps({"x86_64": entries}))

        def download(command, check):
            self.assertTrue(check)
            shutil.copyfile(command[-3], command[-1])

        with patch.object(installer, "MANIFEST", self.manifest), patch.object(installer.subprocess, "run", side_effect=download):
            installer.install_tools(self.prefix, "x86_64")

    def entry(self, source, **options):
        return {"url": str(source), "sha256": hashlib.sha256(source.read_bytes()).hexdigest(), **options}

    def test_installs_clients_plugins_and_lima_support_files(self):
        lima = self.archive("lima.tgz", {"bin/limactl": b"lima", "share/lima/lima.yaml": b"config", "share/lima/lima-guestagent.Linux-x86_64.gz": b"guest agent"})
        docker = self.archive("docker.tgz", {"docker/docker": b"docker", "docker/unused": b"unused"})
        client = self.root / "buildx"
        client.write_bytes(b"buildx")
        self.run_install({
            "lima": self.entry(lima, archive="prefix"),
            "docker": self.entry(docker, member="docker/docker", target="bin/docker"),
            "buildx": self.entry(client, target="libexec/docker/cli-plugins/docker-buildx"),
        })
        self.assertEqual((self.prefix / "share/lima/lima-guestagent.Linux-x86_64.gz").read_bytes(), b"guest agent")
        self.assertEqual((self.prefix / "bin/docker").read_bytes(), b"docker")
        self.assertFalse((self.prefix / "docker/unused").exists())
        for binary in ("bin/limactl", "bin/docker", "libexec/docker/cli-plugins/docker-buildx"):
            self.assertEqual((self.prefix / binary).stat().st_mode & 0o777, 0o755)

    def test_rejects_checksum_mismatch_before_extraction(self):
        archive = self.archive("client.tgz", {"bin/client": b"untrusted"})
        entry = self.entry(archive, archive="prefix")
        entry["sha256"] = "0" * 64
        with self.assertRaisesRegex(ValueError, "SHA256 mismatch"):
            self.run_install({"client": entry})
        self.assertFalse((self.prefix / "bin/client").exists())

    def test_rejects_archive_that_escapes_prefix(self):
        archive = self.archive("escape.tgz", {"../escaped": b"outside"})
        with self.assertRaises(tarfile.FilterError):
            self.run_install({"lima": self.entry(archive, archive="prefix")})
        self.assertFalse((self.root / "escaped").exists())

    def test_missing_docker_binary_fails(self):
        archive = self.archive("empty.tgz", {"docker/other": b"wrong file"})
        with self.assertRaises(KeyError):
            self.run_install({"docker": self.entry(archive, member="docker/docker", target="bin/docker")})
        self.assertFalse((self.prefix / "bin/docker").exists())

    def test_download_failure_stops_installation(self):
        self.manifest.write_text(json.dumps({"x86_64": {"docker": {"url": "https://example.invalid", "sha256": "0" * 64}}}))
        with patch.object(installer, "MANIFEST", self.manifest), patch.object(installer.subprocess, "run", side_effect=subprocess.CalledProcessError(22, "curl")):
            with self.assertRaises(subprocess.CalledProcessError):
                installer.install_tools(self.prefix, "x86_64")
        self.assertEqual(list(self.prefix.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
