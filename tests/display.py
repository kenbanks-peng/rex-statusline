#!/usr/bin/env python3
"""PTY checks for display rendering, resize, input, and terminal restoration."""

import json
from datetime import datetime, timezone
import fcntl
import os
import pty
import select
import signal
import struct
import subprocess
import tempfile
import termios
import time
from pathlib import Path

BINARY = str(Path("build/rex-statusline").resolve())


def write_frame(path, line, columns=12):
    temporary = path.with_name(path.name + ".write")
    temporary.write_text(json.dumps({"line": line, "columns": columns, "trace": {
        "id": str(time.time_ns()), "started": datetime.now(timezone.utc).isoformat(),
        "trigger": "event", "window": "window:test",
    }}))
    temporary.replace(path)


def check(stop):
    master, slave = pty.openpty()
    original = termios.tcgetattr(slave)
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 2, 12, 0, 0))
    with tempfile.TemporaryDirectory() as directory:
        frame = Path(directory) / "frame"
        timing_log = Path(directory) / "timing.jsonl"
        write_frame(frame, "\x1b]52;c;EVIL\x07\x1b[2J\x1b[31m界e\u0301👩‍💻123456789\n")
        process = subprocess.Popen(
            [BINARY, "display", "--frame", str(frame), "--timing-log", str(timing_log)],
            stdin=slave,
            stdout=slave,
            stderr=slave,
        )
        output = b""
        deadline = time.monotonic() + 5
        try:
            while b"\x1b[2K" not in output and time.monotonic() < deadline:
                if select.select([master], [], [], 0.1)[0]:
                    output += os.read(master, 65536)
            assert b"\x1b[2;1H\x1b[2K" in output, output
            assert not termios.tcgetattr(slave)[3] & termios.ECHO
            for mode in (1002, 1006, 1016, 2004):
                assert f"\x1b[?{mode}h".encode() in output, mode
            assert (
                b"EVIL" not in output
                and b"\x1b]52" not in output
                and output.count(b"\x1b[2J") == 1
            )
            def wait_for(marker):
                nonlocal output
                deadline = time.monotonic() + 5
                while marker not in output and time.monotonic() < deadline:
                    if select.select([master], [], [], 0.1)[0]:
                        output += os.read(master, 65536)
                assert marker in output, output

            write_frame(frame, "DIRECT")
            wait_for(b"\x1b[2KDIRECT")
            # The manager publishes by atomic rename; repeated replacements must work.
            for text in ("REPLACED", "AGAIN"):
                temporary = frame.with_suffix(".tmp")
                write_frame(temporary, text)
                temporary.replace(frame)
                wait_for(b"\x1b[2K" + text.encode())
            # A longer frame can arrive before the PTY applies its resize.
            write_frame(frame, "GROWING-FRAME-ENDCAP", 20)
            assert not select.select([master], [], [], 0.1)[0], "clipped growth frame"
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 2, 24, 0, 0))
            process.send_signal(signal.SIGWINCH)
            wait_for(b"\x1b[2KGROWING-FRAME-ENDCAP")
            assert b"\x1b[2KGROWING-FRA\x1b" not in output, output
            output = b""
            write_frame(frame, "SHRUNK", 6)
            assert not select.select([master], [], [], 0.1)[0], "short frame at old width"
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 1, 6, 0, 0))
            process.send_signal(signal.SIGWINCH)
            wait_for(b"\x1b[1;1H\x1b[2KSHRUNK")
            assert b"\x1b[2;1H\x1b[2KSHRUNK" not in output, output
            # The resize may also arrive before the manager publishes its frame.
            output = b""
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 2, 24, 0, 0))
            process.send_signal(signal.SIGWINCH)
            assert not select.select([master], [], [], 0.1)[0], "old frame redrawn during resize"
            write_frame(frame, "FINAL-GROWTH-ENDCAP", 24)
            wait_for(b"\x1b[2;1H\x1b[2KFINAL-GROWTH-ENDCAP")
            assert b"\x1b[2;1H\x1b[2KSHRUNK" not in output, output
            unrelated = frame.with_name("other")
            unrelated.write_text("UNRELATED")
            assert not select.select([master], [], [], 0.2)[0], "unexpected redraw"
            assert b"\x1b[?2026h" in output and b"\x1b[?2026l" in output
            if stop == "input":
                os.write(master, b"\x03")
            elif stop == "interrupt":
                process.send_signal(signal.SIGINT)
            else:
                process.terminate()
            process.wait(timeout=5)
            assert process.returncode == 0, process.returncode
            restored = termios.tcgetattr(slave)
            # macOS sets the kernel-managed PENDIN bit when cooked input resumes.
            restored[3] &= ~getattr(termios, "PENDIN", 0)
            original[3] &= ~getattr(termios, "PENDIN", 0)
            assert restored == original, "terminal attributes not restored"
            while select.select([master], [], [], 0.1)[0]:
                output += os.read(master, 65536)
            assert b"\x1b[?25h" in output and b"\x1b[?1049l" in output
            assert b"elapsed_ms" not in output and b"draw_submitted" not in output
            records = [json.loads(row) for row in timing_log.read_text().splitlines()]
            drawn = [row for row in records if row["msg"] == "draw_submitted"]
            assert drawn, records
            for row in drawn:
                update = row["update"]
                stages = {entry["msg"] for entry in records if entry["update"] == update}
                assert {"frame_received", "dimensions_ready", "draw_submitted"} <= stages, stages
                assert row["window"] == "window:test"
                assert row["total_ms"] >= row["duration_ms"] >= 0, row
            waits = [row for row in records if row["msg"] == "dimensions_ready"]
            assert waits and all("timed_out" in row for row in waits), records
            for mode in (1002, 1006, 1016, 2004):
                assert f"\x1b[?{mode}l".encode() in output, mode
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            os.close(master)
            os.close(slave)


if __name__ == "__main__":
    for method in ["terminate", "interrupt", "input"]:
        check(method)
    print(
        "PASS: frame replacements, control sanitization, Unicode, buffered growth/shrink, resize, "
        "SIGTERM, SIGINT, input, restoration"
    )
