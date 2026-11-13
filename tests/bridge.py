#!/usr/bin/env python3
"""Live BSP bridge lifecycle without requiring SSH or a container engine."""

import json
import subprocess
import tempfile
import time
from datetime import UTC, datetime
from pathlib import Path


def rex(*args):
    p = subprocess.run(
        ["rex", *args], check=False, capture_output=True, text=True, timeout=15
    )
    if p.returncode:
        raise RuntimeError(p.stderr)
    return json.loads(p.stdout) if p.stdout.strip() else None


def main():
    binary = str(Path("build/rex-statusline").resolve())
    created = rex(
        "new",
        "rex-statusline-bridge-test",
        "--json",
        "--shell=none",
        "--",
        "/bin/sleep",
        "3600",
    )
    session = created["session_id"]
    block = created["initial_windows"][0]["block_ids"][0]
    try:
        process = rex("block", "call", "-s", session, "-b", block, "process")
        owner = process["foreground"]["pid"]
        with tempfile.TemporaryDirectory() as root:
            common = [
                "--session",
                session,
                "--block",
                block,
                "--owner",
                str(owner),
                "--source",
                "remote",
                "--bsp-root",
                root,
            ]

            def command(op, *args, data=None):
                return subprocess.run(
                    [binary, op, *common, *args],
                    check=False,
                    input=data,
                    capture_output=True,
                    text=True,
                    timeout=15,
                )

            first = command("claim")
            assert first.returncode == 0, first.stderr
            token = first.stdout.strip()
            assert len(token) == 32 and "\x1b" not in token
            result = command(
                "publish",
                "--kind",
                "line",
                "--epoch",
                token,
                data="\x1b[31mGUEST 界\x1b[0m",
            )
            assert result.returncode == 0, result.stderr
            report_path = next(Path(root).glob("*/*/*/report.json"))
            record = json.loads(report_path.read_text())
            assert record["report"]["kind"] == "line"
            assert record["report"]["line"] == "\x1b[31mGUEST 界\x1b[0m"
            second = command("claim")
            assert second.returncode == 0, second.stderr
            replacement = second.stdout.strip()
            assert replacement != token
            assert (
                command(
                    "publish", "--kind", "line", "--epoch", token, data="STALE"
                ).returncode
                != 0
            )
            assert command("release", "--epoch", token).returncode != 0
            assert command("release", "--epoch", replacement).returncode == 0
            assert not report_path.exists()
            inbox = Path(root) / "inbox"
            inbox.mkdir(mode=0o700)
            forwarder = subprocess.Popen(
                [binary, "bridge", *common, "--inbox", str(inbox)],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )

            def wait_line(want):
                deadline = time.monotonic() + 5
                while time.monotonic() < deadline:
                    if forwarder.poll() is not None:
                        raise AssertionError(forwarder.communicate())
                    try:
                        if (
                            json.loads(report_path.read_text())["report"]["line"]
                            == want
                        ):
                            return
                    except FileNotFoundError:
                        pass
                    time.sleep(0.05)
                raise AssertionError("bridge did not forward " + want)

            try:
                wait_line("")
                payload = {
                    "version": 1,
                    "kind": "line",
                    "line": "INBOX GUEST",
                    "updated": datetime.now(UTC).isoformat(),
                }
                (inbox / "report.json").write_text(json.dumps(payload))
                wait_line("INBOX GUEST")
                previous = report_path.read_bytes()
                time.sleep(0.3)
                assert previous == report_path.read_bytes(), (
                    "cached line was heartbeated"
                )
            finally:
                forwarder.terminate()
                forwarder.wait(timeout=10)
                assert forwarder.returncode == 0, forwarder.communicate()
            assert not report_path.exists(), "bridge failed to release epoch"
        print(
            "PASS: bridge claim, explicit line, epoch handoff, stale writer/release rejection"
        )
    finally:
        rex("kill", session)


if __name__ == "__main__":
    main()
