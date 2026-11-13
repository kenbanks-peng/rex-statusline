"""Exercise the actual display loop, including unsignaled PTY size changes."""

import fcntl
import json
import os
import pty
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time
from pathlib import Path


def check():
    master, slave = pty.openpty()
    original = termios.tcgetattr(slave)

    def resize(columns, rows=2):
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))

    resize(12)
    with tempfile.TemporaryDirectory() as directory:
        frame = Path(directory) / "frame"
        log = Path(directory) / "timing"
        update = 0

        def publish(line, columns):
            nonlocal update
            update += 1
            temporary = frame.with_suffix(".tmp")
            temporary.write_text(json.dumps({
                "line": line, "columns": columns,
                "trace": {"id": str(update), "started": "2026-01-01T00:00:00Z"},
            }))
            temporary.replace(frame)
            return str(update)

        def records():
            return [json.loads(row) for row in log.read_text().splitlines()]

        def received(identity):
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                if any(row["msg"] == "frame_received" and row["update"] == identity
                       for row in records()):
                    return
                time.sleep(0.001)
            raise AssertionError("frame not received")

        def draw(marker):
            output = b""
            deadline = time.monotonic() + 5
            while marker not in output and time.monotonic() < deadline:
                if select.select([master], [], [], 0.05)[0]:
                    output += os.read(master, 65536)
            assert marker in output, output
            return output

        def quiet(seconds=0.025):
            assert not select.select([master], [], [], seconds)[0], "premature redraw"

        publish("INITIAL", 12)
        environment = dict(os.environ, REX_DISPLAY_TEST_CHILD="1",
                           REX_DISPLAY_TEST_FRAME=str(frame), REX_DISPLAY_TEST_LOG=str(log),
                           REX_SESSION="", REX_BLOCK="")
        process = subprocess.Popen(
            [sys.argv[1], "-test.run=^TestDisplayReadinessProcess$"],
            stdin=slave, stdout=slave, stderr=slave, env=environment,
        )
        try:
            output = draw(b"\x1b[2KINITIAL")
            assert b"\x1b[?2048h" in output
            delays = []
            # No controlling terminal/foreground process group is assigned to the
            # child, so TIOCSWINSZ alone does not deliver SIGWINCH to it.
            for mode in ("poll", "winch", "in-band"):
                identity = publish("GROW-" + mode, 24)
                received(identity)
                quiet()
                # A report cannot authorize drawing while the actual PTY is old.
                if mode == "in-band":
                    os.write(master, b"\x1b[48;2;24;0;0t")
                    quiet()
                began = time.monotonic()
                resize(24)
                if mode == "winch":
                    process.send_signal(signal.SIGWINCH)
                elif mode == "in-band":
                    os.write(master, b"\x1b[48;2;")
                    os.write(master, b"24;0;0t")
                draw(b"\x1b[2KGROW-" + mode.encode())
                delays.append((mode, (time.monotonic() - began) * 1000))
                assert not next(row for row in records()
                                if row["msg"] == "dimensions_ready"
                                and row["update"] == identity)["timed_out"]
                # Resize-first must hold the old frame until a matching one arrives.
                resize(12)
                process.send_signal(signal.SIGWINCH)
                quiet()
                publish("S-" + mode, 12)
                draw(b"\x1b[2KS-" + mode.encode())

            # No staged frame means no polling timer: direct notifications must
            # independently wake the loop for a height-only resize.
            for mode in ("winch", "in-band"):
                resize(12, 1)
                if mode == "winch":
                    process.send_signal(signal.SIGWINCH)
                else:
                    os.write(master, b"\x1b[48;1;12;0;0t")
                draw(b"\x1b[1;1H\x1b[2KS-in-band")
                resize(12, 2)
                if mode == "winch":
                    process.send_signal(signal.SIGWINCH)
                else:
                    os.write(master, b"\x1b[48;2;12;0;0t")
                draw(b"\x1b[2;1H\x1b[2KS-in-band")

            # Missing resize must still finish within the existing safety bound.
            identity = publish("TIMEOUT", 24)
            received(identity)
            quiet()
            draw(b"\x1b[2KTIMEOUT")
            ready = next(row for row in records()
                         if row["msg"] == "dimensions_ready" and row["update"] == identity)
            assert ready["timed_out"], ready
            assert 290 <= ready["duration_ms"] < 1500, ready

            os.write(master, b"\x03")
            process.wait(timeout=5)
            assert process.returncode == 0
            output = draw(b"\x1b[?1049l")
            assert b"\x1b[?2048l" in output
            restored = termios.tcgetattr(slave)
            restored[3] &= ~getattr(termios, "PENDIN", 0)
            original[3] &= ~getattr(termios, "PENDIN", 0)
            assert restored == original, "terminal state not restored"
            print("PTY resize-to-draw ms:", ", ".join(
                f"{mode}={elapsed:.3f}" for mode, elapsed in delays))
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            os.close(master)
            os.close(slave)


if __name__ == "__main__":
    check()
