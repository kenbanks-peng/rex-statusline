#!/usr/bin/env python3
"""Offline interactive-shell checks for binary-emitted prompt integration."""

import json
import os
import pty
import re
import select
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path

BINARY = Path("build/rex-statusline").resolve()


def check(shell, array=False, coarse=False):
    print("Checking", shell, "array=" + str(array), flush=True)
    executable = shutil.which(shell)
    assert executable, shell + " is required"
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        binary = root / "publisher's binary"
        shutil.copy2(BINARY, binary)
        script = subprocess.check_output([str(binary), "init", shell], text=True)
        hook = root / "hook.sh"
        hook.write_text(script)
        # Guarded eval must not return out of the caller\'s startup file.
        startup = root / "startup.sh"
        startup.write_text(
            'eval "$(cat ' + shlex.quote(str(hook)) + ')"; printf caller-continued'
        )
        for extra_env in ({}, {"SSH_CONNECTION": "remote"}, {"REX_SESSION": ""}):
            env = dict(os.environ, REX_SESSION="test", REX_BLOCK="test")
            env.update(extra_env)
            result = subprocess.check_output(
                [
                    executable,
                    "-c",
                    ". " + shlex.quote(str(startup)),
                ],
                env=env,
                text=True,
            )
            assert result == "caller-continued", result
        log = root / "reports"
        previous = root / "previous"
        binary.write_text(
            "#!"
            + sys.executable
            + "\nimport json, sys\n"
            + "with open("
            + repr(str(log))
            + ", 'a') as f:\n"
            + "    f.write(json.dumps(sys.argv[1:]) + '\\n')\n"
        )
        env = dict(os.environ, REX_SESSION="test", REX_BLOCK="test", TERM="dumb")
        for name in (
            "SSH_CONNECTION",
            "SSH_TTY",
            "SSH_CLIENT",
            "PROMPT_COMMAND",
            "PS0",
        ):
            env.pop(name, None)
        pid, master = pty.fork()
        if pid == 0:
            args = (
                [executable, "--noprofile", "--norc", "-i"]
                if shell == "bash"
                else [executable, "-f", "-i"]
            )
            os.execve(executable, args, env)
        output = b""

        sequence = 0

        def wait_prompt(marker):
            nonlocal output
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                if select.select([master], [], [], 0.1)[0]:
                    output += os.read(master, 65536)
                    if re.sub(rb"\x1b\[[0-9;?]*[A-Za-z]", b"", output).endswith(marker):
                        result, output = output, b""
                        return result
            raise AssertionError(output.decode(errors="replace"))

        def send(command):
            nonlocal sequence
            sequence += 1
            marker = "BSP-READY-" + str(sequence) + "> "
            os.write(
                master,
                (
                    "PS1=$(printf 'BSP-READY-%s> ' "
                    + str(sequence)
                    + "); "
                    + command
                    + "\n"
                ).encode(),
            )
            return wait_prompt(marker.encode())

        def reports():
            return [
                dict(zip(args[1::2], args[2::2]))
                for args in map(json.loads, log.read_text().splitlines())
            ]

        try:
            send("unset RPROMPT" + ("; unset EPOCHREALTIME" if coarse else ""))
            if shell == "bash":
                old = "printf '%s\\n' \"$?\" >> " + shlex.quote(str(previous))
                prompt = (
                    "PROMPT_COMMAND=(" + shlex.quote(old) + " ':')"
                    if array
                    else "PROMPT_COMMAND=" + shlex.quote(old)
                )
                send(prompt + "; PS0='OLD-PS0'; trap ':' DEBUG")
            else:
                send(
                    "precmd() { local code=$?; printf '%s\\n' \"$code\" >> "
                    + shlex.quote(str(previous))
                    + "; return 0; }"
                )
            send("source " + shlex.quote(str(hook)))
            send("source " + shlex.quote(str(hook)))
            count = len(reports())
            result = send("sleep 1.1; false")
            frames = reports()
            assert len(frames) == count + 1, frames
            report = frames[-1]
            assert report["--exit-code"] == "1", (frames, result, previous.read_text())
            assert int(report["--duration-ms"]) >= 1000, report
            assert report["--owner"] == str(pid), report
            assert previous.read_text().splitlines()[-1] == "1"
            if shell == "bash":
                assert b"OLD-PS0" in result, result
                assert b"trap -- ':' DEBUG" in send("trap -p DEBUG")
            send("sleep 60 &")
            assert reports()[-1]["--jobs"] == "1", reports()[-1]
            send("kill %1; wait %1 2>/dev/null; :")
            send("cd /tmp")
            assert reports()[-1]["--cwd"] == "/tmp"
            count = len(reports())
            send("export SSH_CONNECTION=remote")
            assert len(reports()) == count
        finally:
            os.kill(pid, signal.SIGKILL)
            os.waitpid(pid, 0)
            os.close(master)


def main():
    for shell in ("bash", "zsh"):
        check(shell)
    check("bash", array=True)
    check("bash", coarse=True)
    for args in (["init"], ["init", "fish"], ["init", "bash", "extra"]):
        result = subprocess.run([str(BINARY), *args], check=False, capture_output=True)
        assert result.returncode != 0 and not result.stdout, args
    print(
        "PASS: Bash/zsh publication, timing, jobs, exit status, quoting, existing hooks, idempotence"
    )


if __name__ == "__main__":
    main()
