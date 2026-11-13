#!/usr/bin/env python3
"""Update the Homebrew formula from downloaded release archives."""

import hashlib
import re
import sys
from pathlib import Path


def update_formula(formula: str, tag: str, archives: Path) -> str:
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", tag):
        raise ValueError("Expected a stable vMAJOR.MINOR.PATCH release tag")
    version = tag[1:]
    match = re.search(r'^  version "([0-9]+\.[0-9]+\.[0-9]+)"$', formula, re.MULTILINE)
    if not match:
        raise ValueError("Expected one formula version")
    if tuple(map(int, version.split("."))) < tuple(map(int, match[1].split("."))):
        return formula

    updated, count = re.subn(
        r'^  version "[^"]+"$', f'  version "{version}"', formula, flags=re.MULTILINE
    )
    if count != 1:
        raise ValueError("Expected one formula version")
    for os in ("darwin", "linux"):
        for arch in ("arm64", "amd64"):
            archive = f"rex-statusline-{os}-{arch}.tar.gz"
            checksum = hashlib.sha256((archives / archive).read_bytes()).hexdigest()
            pattern = (
                r'(url "https://github\.com/kenbanks-peng/rex-statusline/releases/'
                r'download/v#\{version\}/'
                + re.escape(archive)
                + r'"\n\s+sha256 ")[0-9a-f]{64}(")'
            )
            updated, count = re.subn(
                pattern, lambda m: m[1] + checksum + m[2], updated
            )
            if count != 1:
                raise ValueError(f"Expected one checksum for {archive}")
    return updated


if __name__ == "__main__":
    formula_path = Path(sys.argv[1])
    formula_path.write_text(
        update_formula(formula_path.read_text(), sys.argv[2], Path(sys.argv[3]))
    )
