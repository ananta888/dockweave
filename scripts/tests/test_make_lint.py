"""`make lint` must fail when staticcheck reports findings or is missing (RF-037)."""
import os
import shutil
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


@unittest.skipUnless(shutil.which("make"), "make not installed")
class MakeLintTest(unittest.TestCase):
    def run_lint(self, staticcheck_exit):
        with tempfile.TemporaryDirectory() as tmp:
            if staticcheck_exit is not None:
                fake = Path(tmp) / "staticcheck"
                fake.write_text("#!/bin/sh\necho 'x.go:1:1: injected finding (SA0000)'\nexit %d\n" % staticcheck_exit)
                fake.chmod(fake.stat().st_mode | stat.S_IXUSR)
            env = dict(os.environ, PATH=tmp + os.pathsep + "/usr/bin:/bin")
            # GO=true turns the `vet` prerequisite into a no-op.
            return subprocess.run(["make", "-s", "-C", str(ROOT), "lint", "GO=true"],
                                  capture_output=True, text=True, env=env, timeout=60)

    def test_injected_finding_fails(self):
        p = self.run_lint(1)
        self.assertNotEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertIn("injected finding", p.stdout)

    def test_missing_staticcheck_fails(self):
        p = self.run_lint(None)
        self.assertNotEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertIn("make install-tools", p.stderr)

    def test_clean_run_passes(self):
        p = self.run_lint(0)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)


if __name__ == "__main__":
    unittest.main()
