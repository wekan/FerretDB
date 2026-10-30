#!/usr/bin/env python3
"""Offline guard: FerretDB never opens an MPTCP listener by default.

CVE-2026-98432 is a Linux kernel use-after-free in IPv4 TCP connection
handling, shown to give remote code execution through an MPTCP listener
(golang-announce, 2026-10). Since Go 1.24 every Linux listen socket of a Go
program is MPTCP unless the program says otherwise, so a FerretDB built with
Go's default was such a listener. go.mod sets `godebug multipathtcp=0`, which
every build of the main module takes as its default; GODEBUG=multipathtcp=1
still turns it on for an operator who needs it. The real fix is a patched
kernel - this keeps FerretDB's own listener out of the way until then.
"""
import re
import shutil
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SKIP = {'.git', '.goroot', 'tmp', 'bin', 'node_modules', 'vendor'}


def files(pattern):
    for path in ROOT.rglob(pattern):
        if not SKIP.intersection(path.relative_to(ROOT).parts):
            yield path


class MptcpOff(unittest.TestCase):
    def test_go_mod_turns_it_off(self):
        go_mod = (ROOT / 'go.mod').read_text(encoding='utf-8')
        self.assertRegex(go_mod, r'(?m)^godebug multipathtcp=0$')

    def test_the_built_command_has_it_as_its_default(self):
        go = shutil.which('go')
        if not go:
            self.skipTest('no go toolchain on PATH')
        out = subprocess.run([go, 'list', '-f', '{{.DefaultGODEBUG}}', './cmd/ferretdb'], cwd=ROOT,
                             capture_output=True, text=True, check=False)
        if out.returncode:
            self.skipTest('go list could not load the module: ' + out.stderr.strip()[:200])
        self.assertIn('multipathtcp=0', out.stdout.strip().split(','))

    def test_negative_nothing_turns_it_back_on(self):
        found = []
        for path in files('*.go'):
            if path.name.endswith('_test.go'):
                continue
            for n, line in enumerate(path.read_text(encoding='utf-8', errors='replace').splitlines(), 1):
                if re.search(r'SetMultipathTCP\(\s*true\s*\)', line):
                    found.append(f'{path.relative_to(ROOT)}:{n}')
        for pattern in ('Dockerfile*', '*.yml', '*.yaml', '*.sh', 'go.mod'):
            for path in files(pattern):
                # Settings, not the comments that explain them.
                for n, line in enumerate(path.read_text(encoding='utf-8', errors='replace').splitlines(), 1):
                    if 'multipathtcp=1' in line and not re.match(r'\s*(?://|#)', line):
                        found.append(f'{path.relative_to(ROOT)}:{n}')
        self.assertEqual(found, [])

    def test_the_release_runs_this_guard(self):
        for name in ('release-all.yml', 'release-all-missing.yml'):
            workflow = (ROOT / '.github/workflows' / name).read_text(encoding='utf-8')
            self.assertIn('python3 -B tests/mptcp-off.py', workflow, name)


if __name__ == '__main__':
    unittest.main()
