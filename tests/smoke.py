#!/usr/bin/env python3
"""Exercise the compiled manager against an isolated local Rex session."""

import json
import os
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


def main():
    binary = str(Path("build/rex-statusline").resolve())
    config = str(Path("internal/defaults/config.toml").resolve())
    previous = rex("whoami", "--json")["session_id"]
    created = rex(
        "new",
        "rex-statusline-go-smoke",
        "--json",
        "--shell=none",
        "--",
        "/bin/sleep",
        "3600",
    )
    session = created["session_id"]
    window = created["initial_windows"][0]["window_id"]
    a = created["initial_windows"][0]["block_ids"][0]
    b = rex(
        "split",
        "-s",
        session,
        "-b",
        a,
        "--focus=false",
        "--json",
        "--shell=none",
        "--",
        "/bin/sleep",
        "3600",
    )["block_ids"][0]
    command = [binary, "manage", "--foreground", "--session", session, "--config", config]
    manager = subprocess.Popen(command)

    def view():
        return rex("api", "call", "-s", session, "session.view", "{}")

    def sources_and_floats():
        sources, floats = {}, {}
        for w in view()["windows"]:
            for l in w["layers"]:
                for block in l["blocks"]:
                    label = block.get("label", "")
                    if label.startswith("rex-statusline:"):
                        key = label[len("rex-statusline:") :]
                        assert key not in floats, "duplicate float"
                        floats[key] = (w, l, block)
                    elif l["kind"] == "tiled":
                        sources[block["block_id"]] = (w, l, block)
        return sources, floats

    def wait(check):
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            if manager.poll() is not None:
                raise RuntimeError(f"manager exited with code {manager.returncode}")
            values = sources_and_floats()
            if check(*values):
                return values
            time.sleep(0.2)
        raise AssertionError("timed out: " + json.dumps(view()))

    def capture(name):
        if os.environ.get("SMOKE_SHOW"):
            rex(
                "-C",
                os.environ["SMOKE_SHOW"],
                "do",
                "session.select",
                "session_id=" + session,
            )
            time.sleep(1)
            if os.environ.get("SMOKE_WINDOW"):
                Path("build/smoke").mkdir(parents=True, exist_ok=True)
                subprocess.run(
                    [
                        "screencapture",
                        "-x",
                        "-l",
                        os.environ["SMOKE_WINDOW"],
                        str(Path("build/smoke") / (name + ".png")),
                    ],
                    check=True,
                )

    try:
        _, floats = wait(lambda s, f: set(s) == {a, b} and set(f) == {window})
        shared_id = floats[window][2]["block_id"]
        capture("shared")
        duplicate = subprocess.run(
            command, check=False, capture_output=True, text=True, timeout=10
        )
        assert (
            duplicate.returncode != 0
            and "another rex-statusline manager" in duplicate.stderr
        )
        rex("focus", "-s", session, b)
        wait(lambda s, f: set(s) == {a, b} and set(f) == {window}
             and f[window][2]["block_id"] == shared_id)
        rex(
            "api",
            "call",
            "-s",
            session,
            "session.resize_pane",
            json.dumps(
                {
                    "direction": "horizontal",
                    "before_block_id": a,
                    "after_block_id": b,
                    "ratio": 0.65,
                }
            ),
        )
        wait(
            lambda s, f: (
                set(f) == {window}
                and 0 <= f[window][1]["bounds"][0]
                and 0 < f[window][1]["bounds"][2] <= 1
                and f[window][1]["bounds"][0] + f[window][1]["bounds"][2] <= 1.00001
            )
        )
        rex("zoom", "-s", session, a, "--on")
        wait(
            lambda s, f: (
                set(f) == {window} and f[window][1].get("state") != "minimized"
            )
        )
        capture("zoom")
        rex("zoom", "-s", session, a, "--off")
        wait(lambda s, f: f[window][1].get("state") != "minimized")
        target = rex(
            "window",
            "new",
            "-s",
            session,
            "Move target",
            "--focus=false",
            "--json",
            "--shell=none",
            "--",
            "/bin/sleep",
            "3600",
        )
        c = target["block_ids"][0]
        rex("move", "-s", session, b, "right", c)
        # Inactive tabs defer their first render until selected.
        rex("focus", "-s", session, target["window_id"])
        wait(
            lambda s, f: (
                set(s) == {a, b, c}
                and set(f) == {window, target["window_id"]}
                and s[b][0]["window_id"] == target["window_id"]
            )
        )
        rex("block", "close", "-s", session, b)
        wait(lambda s, f: set(s) == {a, c}
             and set(f) == {window, target["window_id"]})
        _, floats = sources_and_floats()
        layer = floats[window][1]
        dragged = list(layer["bounds"])
        dragged[0], dragged[1] = 0.1, 0.3
        rex(
            "api", "call", "-s", session, "session.resize_layer",
            json.dumps({"layer_id": layer["layer_id"], "bounds": dragged}),
        )
        # Let several reconciliation ticks observe the same manual move as Rex's UI.
        time.sleep(4.5)
        _, floats = sources_and_floats()
        assert all(
            abs(actual - expected) < 0.00001
            for actual, expected in zip(floats[window][1]["bounds"], dragged)
        ), "manager reset the dragged position: " + json.dumps({
            "expected": dragged, "actual": floats[window][1]["bounds"]
        })
        manager.terminate()
        manager.wait(timeout=15)
        _, floats = sources_and_floats()
        assert not floats, "shutdown left floats"
        assert manager.returncode == 0
        print(
            "PASS: startup, duplicate lock, shared tab floats, resize, zoom, move, close, dragged placement, cleanup"
        )
    finally:
        if manager.poll() is None:
            manager.terminate()
            manager.wait(timeout=15)
        if os.environ.get("SMOKE_SHOW"):
            rex(
                "-C",
                os.environ["SMOKE_SHOW"],
                "do",
                "session.select",
                "session_id=" + previous,
            )
        rex("kill", session)


if __name__ == "__main__":
    main()
