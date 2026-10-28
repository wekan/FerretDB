#!/usr/bin/env python3
"""Offline guard: no Go module of this repository requires a version that a
security advisory names as affected.

A fixed advisory is pinned here as the first patched version, and checked in
EVERY go.mod (main, integration/, tools/), not only the one the alert was
raised for: a transitive requirement moves between modules as tools come and
go, and a downgrade by `go mod tidy` or a Dependabot group bump would bring it
back silently.

- github.com/go-jose/go-jose/v4 < 4.1.6: a crafted RSA key in a JWK takes
  cubic CPU time to validate (Dependabot alert #55). tools/ had 4.1.4 through
  golang.org/x/pkgsite, golang.org/x/perf and go-task.
"""
import importlib.util
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('sync', ROOT / 'tests/integration-module-sync.py')
sync = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sync)

FLOORS = {
    'github.com/go-jose/go-jose/v4': 'v4.1.6',
}


def below_floor(requirements, where):
    return sorted(
        f'{where}: {m} {v} < {FLOORS[m]}'
        for m, v in requirements.items()
        if m in FLOORS and sync.key(v) < sync.key(FLOORS[m])
    )


def go_mods():
    return sorted(p for p in ROOT.glob('**/go.mod')
                  if not any(part in ('.goroot', 'tmp', 'bin', '.git') for part in p.relative_to(ROOT).parts))


class DependencyFloors(unittest.TestCase):
    def test_no_module_requires_an_affected_version(self):
        mods = go_mods()
        self.assertIn(ROOT / 'tools/go.mod', mods, 'the module the alert was raised for is checked')
        problems = []
        for mod in mods:
            problems += below_floor(sync.requirements(mod), str(mod.relative_to(ROOT)))
        self.assertEqual(problems, [], 'upgrade with `go get <module>@<floor>` and `go mod tidy`')

    def test_detects_affected_version(self):
        # Negative test: the version Dependabot alert #55 was raised for.
        self.assertEqual(below_floor({'github.com/go-jose/go-jose/v4': 'v4.1.4'}, 'tools/go.mod'),
                         ['tools/go.mod: github.com/go-jose/go-jose/v4 v4.1.4 < v4.1.6'])
        self.assertEqual(below_floor({'github.com/go-jose/go-jose/v4': 'v4.1.6-0.20261001000000-abcdef123456'}, 'x'),
                         ['x: github.com/go-jose/go-jose/v4 v4.1.6-0.20261001000000-abcdef123456 < v4.1.6'],
                         'a pseudo-version before the release is not the release')
        self.assertEqual(below_floor({'github.com/go-jose/go-jose/v4': 'v4.1.6',
                                      'github.com/go-jose/go-jose/v3': 'v3.0.0'}, 'x'), [])


if __name__ == '__main__':
    unittest.main()
