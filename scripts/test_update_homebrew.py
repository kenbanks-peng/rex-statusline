import hashlib
import tempfile
import unittest
from pathlib import Path

from update_homebrew import update_formula


class UpdateFormulaTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.archives = Path(self.temp.name)
        self.formula = 'class RexStatusline < Formula\n  version "1.0.1"\n'
        for os in ("darwin", "linux"):
            for arch in ("arm64", "amd64"):
                name = f"rex-statusline-{os}-{arch}.tar.gz"
                (self.archives / name).write_bytes(name.encode())
                self.formula += (
                    '  url "https://github.com/kenbanks-peng/rex-statusline/releases/'
                    'download/v#{version}/' + name + '"\n'
                    '  sha256 "' + "0" * 64 + '"\n'
                )
        self.formula += "end\n"

    def test_updates_version_and_all_four_checksums(self):
        updated = update_formula(self.formula, "v1.1.0", self.archives)
        self.assertIn('version "1.1.0"', updated)
        for archive in self.archives.iterdir():
            self.assertIn(hashlib.sha256(archive.read_bytes()).hexdigest(), updated)
        self.assertEqual(update_formula(updated, "v1.1.0", self.archives), updated)

    def test_skips_older_release(self):
        formula = self.formula.replace('"1.0.1"', '"1.10.0"')
        self.assertEqual(update_formula(formula, "v1.2.0", self.archives), formula)

    def test_rejects_unstable_or_invalid_tags(self):
        for tag in ("v1.1.0-rc.1", "1.1.0", "v1.1", "v1.1.0\n"):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                update_formula(self.formula, tag, self.archives)

    def test_requires_all_archives(self):
        next(self.archives.iterdir()).unlink()
        with self.assertRaises(FileNotFoundError):
            update_formula(self.formula, "v1.1.0", self.archives)

    def test_rejects_missing_or_duplicate_checksum_entries(self):
        for formula in (
            self.formula.replace('sha256', 'missing', 1),
            self.formula + self.formula.splitlines(keepends=True)[2]
            + self.formula.splitlines(keepends=True)[3],
        ):
            with self.subTest(formula=formula), self.assertRaises(ValueError):
                update_formula(formula, "v1.1.0", self.archives)


if __name__ == "__main__":
    unittest.main()
