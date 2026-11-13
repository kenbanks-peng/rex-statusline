#!/usr/bin/env python3
"""Live Bash/zsh Rex publisher checks; only touch disposable sessions."""

import json
import os
import shlex
import shutil
import subprocess
import time
from pathlib import Path


def rex(*args):
    result = subprocess.run(
        ["rex", *args], check=False, capture_output=True, text=True, timeout=15
    )
    if result.returncode:
        raise RuntimeError(result.stderr)
    return json.loads(result.stdout) if result.stdout.strip() else None


def check(shell):
    binary = str(Path("build/rex-statusline").resolve())
    server = rex("whoami", "--json")["instance_id"]
    created = rex(
        "new",
        "rex-statusline-publisher-test",
        "--json",
        "--shell=none",
        "--",
        shutil.which(shell),
        *(["--noprofile", "--norc", "-i"] if shell == "bash" else ["-f", "-i"]),
    )
    session = created["session_id"]
    block = created["initial_windows"][0]["block_ids"][0]
    root = Path(os.environ.get("XDG_CACHE_HOME", str(Path.home() / ".cache"))) / "bsp"
    report = root / server / session / block / "report.json"

    def send(command):
        rex("send", "-s", session, "-b", block, command)
        rex("send-key", "-s", session, "-b", block, "enter")

    def wait(check):
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            try:
                value = json.loads(report.read_text())
                if check(value):
                    return value
            except (FileNotFoundError, json.JSONDecodeError):
                pass
            time.sleep(0.1)
        raise AssertionError("no expected report: " + str(report))

    try:
        time.sleep(0.3)
        send('eval "$(' + shlex.quote(binary) + " init " + shell + ')"')
        initial = wait(lambda r: r["report"]["kind"] == "context")
        send("sleep 0.15; false")
        completed = wait(lambda r: r["report"]["context"]["exit_code"] == 1)
        assert completed["report"]["context"]["duration_ms"] >= 100
        assert completed["owner"] == initial["owner"]
        assert completed["session"] == session and completed["block"] == block
        assert "PATH" in completed["report"]["context"]["env"]
        send("sleep 60 &")
        wait(lambda r: r["report"]["context"]["jobs"] == 1)
        send("kill %1")
        print(
            "PASS: "
            + shell
            + " ownership, context, duration, exit code, jobs, identity"
        )
    finally:
        rex("kill", session)
        shutil.rmtree(root / server / session, ignore_errors=True)


if __name__ == "__main__":
    for shell in ("bash", "zsh"):
        check(shell)
