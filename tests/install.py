#!/usr/bin/env python3
"""Exercise the installer offline with real archives and checksum verification."""

import hashlib
import io
import os
import shutil
import subprocess
import tarfile
import tempfile
import unittest
from pathlib import Path

INSTALLER = Path(__file__).resolve().parents[1] / "install.sh"
PAYLOAD = b"#!/bin/sh\necho installed\n"


class InstallerTests(unittest.TestCase):
    def run_installer(
        self,
        os_name="Linux",
        arch="x86_64",
        checksum="valid",
        on_path=False,
        download_failure=False,
        verifier=None,
    ):
        with tempfile.TemporaryDirectory(prefix="rex install ") as directory:
            root = Path(directory)
            home = root / "home"
            destination = home / ".local/bin"
            destination.mkdir(parents=True)
            target = destination / "rex-statusline"
            target.write_bytes(b"existing")
            commands = root / "commands"
            commands.mkdir()
            # Isolate PATH so either checksum implementation can be tested.
            for command in (
                "sh",
                "tar",
                "install",
                "mktemp",
                "awk",
                "mkdir",
                "mv",
                "rm",
                "gzip",
                "sha256sum",
                "shasum",
            ):
                if (
                    verifier
                    and command in ("sha256sum", "shasum")
                    and command != verifier
                ):
                    continue
                source = shutil.which(command)
                if source:
                    (commands / command).symlink_to(source)
            for name, content in {
                "uname": '#!/bin/sh\ncase "$1" in -s) echo "$TEST_OS";; -m) echo "$TEST_ARCH";; esac\n',
                "curl": '#!/bin/sh\n[ "$DOWNLOAD_FAILURE" = 0 ] || exit 22\n/bin/cp "$FIXTURE/${4##*/}" "$3"\n',
            }.items():
                path = commands / name
                path.write_text(content)
                path.chmod(0o755)
            platform = "darwin" if os_name == "Darwin" else "linux"
            machine = "amd64" if arch == "x86_64" else "arm64"
            archive = root / f"rex-statusline-{platform}-{machine}.tar.gz"
            with tarfile.open(archive, "w:gz") as output:
                entry = tarfile.TarInfo("rex-statusline")
                entry.size = len(PAYLOAD)
                entry.mode = 0o755
                output.addfile(entry, io.BytesIO(PAYLOAD))
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            record = f"{digest}  {archive.name}\n"
            checksums = {
                "valid": record,
                "corrupt": f"{'0' * 64}  {archive.name}\n",
                "missing": f"{digest}  other.tar.gz\n",
                "duplicate": record * 2,
            }
            (root / "checksums.txt").write_text(checksums[checksum])
            env = dict(
                os.environ,
                HOME=str(home),
                FIXTURE=str(root),
                TEST_OS=os_name,
                TEST_ARCH=arch,
                DOWNLOAD_FAILURE=str(int(download_failure)),
                TMPDIR=str(root),
                PATH=str(commands) + (os.pathsep + str(destination) if on_path else ""),
            )
            # Feed stdin just as curl | sh does.
            result = subprocess.run(
                ["/bin/sh"],
                check=False,
                input=INSTALLER.read_text(),
                env=env,
                capture_output=True,
                text=True,
                timeout=10,
            )
            self.assertFalse(list(destination.glob(".rex-statusline.*")))
            self.assertFalse(list(root.glob("tmp.*")))
            return result, target.read_bytes(), os.access(target, os.X_OK)

    def test_supported_platforms(self):
        for os_name in ("Darwin", "Linux"):
            for arch in ("x86_64", "arm64", "aarch64"):
                with self.subTest(os=os_name, arch=arch):
                    result, data, executable = self.run_installer(os_name, arch)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(data, PAYLOAD)
                    self.assertTrue(executable)
                    self.assertIn("to your PATH", result.stdout)

    def test_already_on_path(self):
        result, _, _ = self.run_installer(on_path=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("to your PATH", result.stdout)

    def test_checksum_failures_preserve_existing_binary(self):
        for checksum in ("corrupt", "missing", "duplicate"):
            with self.subTest(checksum=checksum):
                result, data, _ = self.run_installer(checksum=checksum)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(data, b"existing")

    def test_unsupported_platforms(self):
        for os_name, arch in (("FreeBSD", "x86_64"), ("Linux", "riscv64")):
            result, data, _ = self.run_installer(os_name, arch)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Unsupported", result.stderr)
            self.assertEqual(data, b"existing")

    def test_download_failure(self):
        result, data, _ = self.run_installer(download_failure=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(data, b"existing")

    def test_checksum_implementations(self):
        for verifier in ("sha256sum", "shasum"):
            if not shutil.which(verifier):
                continue
            with self.subTest(verifier=verifier):
                result, data, _ = self.run_installer(verifier=verifier)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(data, PAYLOAD)


if __name__ == "__main__":
    unittest.main()
