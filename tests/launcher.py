#!/usr/bin/env python3
"""Real linux launcher + native bridge + guest publisher, with a fake engine.

Runs no containers. Optional: LINUX_LAUNCHER and GUEST_PUBLISHER override paths.
"""

import json
import os
import subprocess
import tempfile
import time
from pathlib import Path


def rex(*args):
    p = subprocess.run(
        ["rex", *args], check=False, capture_output=True, text=True, timeout=15
    )
    if p.returncode:
        raise RuntimeError(p.stderr)
    return json.loads(p.stdout) if p.stdout.strip() else None


def main():
    devbox = Path.home() / "Software/DevBox/tools/ai"
    launcher = Path(
        os.environ.get("LINUX_LAUNCHER", devbox / "linux/target/debug/linux")
    )
    publisher = Path(
        os.environ.get(
            "GUEST_PUBLISHER",
            devbox / "dev-container/resources/scripts/terminal-status.sh",
        )
    )
    if not launcher.is_file() or not publisher.is_file():
        raise SystemExit("Build the sibling linux launcher and provide GUEST_PUBLISHER")
    binary = Path("build/rex-statusline").resolve()
    theme = Path.home() / ".config/rex/status/rex.toml"
    server = rex("whoami", "--json")["instance_id"]
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        engine = root / "container"
        engine.write_text("""#!/bin/bash
set -eu
inbox=
while (( $# )); do
    if [[ $1 == --mount && $# -gt 1 && $2 == *target=/run/terminal-status* ]]; then
        inbox=${2#*source=}
        inbox=${inbox%%,target=*}
    fi
    shift
done
[[ -n $inbox ]]
export BSP_REPORT_DIR="$inbox"
source "$GUEST_PUBLISHER"
_omp_status=7
_omp_execution_time=1500
terminal_status_publish
printf 'GUEST_READY\\n'
exec sleep 3600
""")
        engine.chmod(0o700)
        config = root / "linux.toml"
        config.write_text("[container]\nimage='fake:test'\ncommand=['bash','-i']\n")
        cache = root / "cache"
        created = rex(
            "new",
            "rex-statusline-launcher-test",
            "--json",
            "--shell=none",
            "--",
            "/usr/bin/env",
            "PATH=" + str(root) + ":" + os.environ["PATH"],
            "XDG_CACHE_HOME=" + str(cache),
            "REX_STATUSLINE_EXECUTABLE=" + str(binary),
            "GUEST_PUBLISHER=" + str(publisher),
            "TERMINAL_STATUS_CONFIG=" + str(theme),
            str(launcher),
            "--config",
            str(config),
        )
        session = created["session_id"]
        block = created["initial_windows"][0]["block_ids"][0]
        report = cache / "bsp" / server / session / block / "report.json"
        try:
            deadline = time.monotonic() + 15
            record = None
            while time.monotonic() < deadline:
                try:
                    candidate = json.loads(report.read_text())
                    if candidate["report"]["line"]:
                        record = candidate
                        break
                except FileNotFoundError:
                    pass
                time.sleep(0.1)
            if record is None:
                output = subprocess.run(
                    ["rex", "capture", "-s", session, "-b", block, "--trim"],
                    check=False,
                    capture_output=True,
                    text=True,
                    timeout=15,
                )
                raise AssertionError(
                    "no bridged guest report: " + output.stdout + output.stderr
                )
            assert record["report"]["kind"] == "line"
            assert record["report"]["source"] == "container"
            assert record["block"] == block and record["session"] == session
            assert "1.5" in record["report"]["line"], record
            assert "\x1b[" in record["report"]["line"], record
            print(
                "PASS: real Rust launcher -> guest publisher -> native BSP bridge -> host record"
            )
        finally:
            rex("kill", session)


if __name__ == "__main__":
    main()
