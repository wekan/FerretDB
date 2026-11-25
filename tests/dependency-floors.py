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
- github.com/klauspost/compress < 1.20.2 (GO-2026-6865) and golang.org/x/net
  < 0.62.0 (GO-2026-6860, GO-2026-6866): govulncheck finds both reachable, the
  first one in the released binary itself.
- go.opentelemetry.io/otel/... < 1.47.0: the version WeKan's Dependabot alert
  #189 was raised for (otel/trace), and the rest of the set, which moves
  together. tools/ had 1.45.0 through the gcp detectors.
- Go itself < 1.27.4 (GO-2026-6859, net/http): every place that picks the Go a
  release is built with - the go directives, the Dockerfiles, build.sh and the
  release workflows - so no binary is built with the affected stdlib.
"""
import importlib.util
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('sync', ROOT / 'tests/integration-module-sync.py')
sync = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sync)

FLOORS = {
    'github.com/go-jose/go-jose/v4': 'v4.1.6',
    'github.com/klauspost/compress': 'v1.20.2',
    'golang.org/x/net': 'v0.62.0',
    'go.opentelemetry.io/otel': 'v1.47.0',
    'go.opentelemetry.io/otel/trace': 'v1.47.0',
    'go.opentelemetry.io/otel/metric': 'v1.47.0',
    'go.opentelemetry.io/otel/log': 'v1.47.0',
    'go.opentelemetry.io/otel/sdk': 'v1.47.0',
    'go.opentelemetry.io/otel/sdk/metric': 'v1.47.0',
}

GO_FLOOR = (1, 27, 4)

# Where the Go that builds a release is chosen, and how to read it there. The
# tools module only builds developer tools, so its directive is not checked.
GO_PINS = [
    ('go.mod', r'^go (\d+\.\d+(?:\.\d+)?)$'),
    ('integration/go.mod', r'^go (\d+\.\d+(?:\.\d+)?)$'),
    ('Dockerfile', r'golang:(\d+\.\d+(?:\.\d+)?)\b'),
    ('build/ferretdb/*.Dockerfile', r'golang:(\d+\.\d+(?:\.\d+)?)\b'),
    ('build.sh', r'^GO_VERSION="(\d+\.\d+(?:\.\d+)?)"'),
    ('.github/workflows/release-all*.yml', r"GO_VERSION: '(\d+\.\d+(?:\.\d+)?)'"),
]


def go_key(version):
    return tuple(int(x) for x in (version.split('.') + ['0', '0'])[:3])


def go_below_floor(text, pattern, where):
    found = re.findall(pattern, text, re.M)
    return found, [f'{where}: Go {v} < {".".join(map(str, GO_FLOOR))}' for v in found if go_key(v) < GO_FLOOR]


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

        self.assertEqual(below_floor({'go.opentelemetry.io/otel/trace': 'v1.45.0',
                                      'github.com/klauspost/compress': 'v1.20.1',
                                      'golang.org/x/net': 'v0.61.0'}, 'tools/go.mod'),
                         ['tools/go.mod: github.com/klauspost/compress v1.20.1 < v1.20.2',
                          'tools/go.mod: go.opentelemetry.io/otel/trace v1.45.0 < v1.47.0',
                          'tools/go.mod: golang.org/x/net v0.61.0 < v0.62.0'])

    def test_releases_are_built_with_a_patched_go(self):
        problems = []
        for glob, pattern in GO_PINS:
            files = sorted(ROOT.glob(glob))
            self.assertTrue(files, f'{glob} exists')
            for f in files:
                found, below = go_below_floor(f.read_text(), pattern, str(f.relative_to(ROOT)))
                self.assertTrue(found, f'{f.relative_to(ROOT)} still names its Go version as {pattern}')
                problems += below
        self.assertEqual(problems, [], 'raise every Go pin together')
        # Dockerfile.release only repackages the release binaries: since the
        # mongosh launcher went, nothing in it compiles Go, so it has no pin.
        self.assertNotRegex((ROOT / 'Dockerfile.release').read_text(), r'(?m)^FROM\s+(--platform=\S+\s+)?golang:')

    def test_detects_affected_go(self):
        # Negative test: the toolchain govulncheck reported GO-2026-6859 for.
        self.assertEqual(go_below_floor('go 1.27.3\n', GO_PINS[0][1], 'go.mod')[1], ['go.mod: Go 1.27.3 < 1.27.4'])
        self.assertEqual(go_below_floor('FROM golang:1.26.11 AS x', GO_PINS[2][1], 'D')[1], ['D: Go 1.26.11 < 1.27.4'])
        self.assertEqual(go_below_floor('go 1.27\n', GO_PINS[0][1], 'go.mod')[1], ['go.mod: Go 1.27 < 1.27.4'])
        self.assertEqual(go_below_floor("GO_VERSION: '1.27.4'", GO_PINS[5][1], 'w')[1], [])
        self.assertEqual(go_below_floor('GO_VERSION="1.28.0"', GO_PINS[4][1], 'b')[1], [])


if __name__ == '__main__':
    unittest.main()
