#!/usr/bin/env python3
"""Offline guard: the integration module must not lag the main module.

integration/go.mod replaces github.com/FerretDB/FerretDB with ../, so Go's
minimal version selection raises every shared requirement to at least the main
module's version. When a Dependabot bump touches only the main go.mod, the
integration go.mod still records the older version, and every build in
integration/ rewrites go.mod and go.sum (read-only module mode refuses to
build at all). That happened after the go-hdb 1.18.11 bump and again after the
OpenTelemetry 1.47.0 / go-hdb 1.19.0 / sqlite 1.60.1 bump.
"""
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
REQ = re.compile(r'^\s*(?:require\s+)?(\S+)\s+(v\S+)', re.M)


def requirements(path):
    text = Path(path).read_text()
    found = {}
    for block in re.findall(r'require\s*\((.*?)\)', text, re.S):
        for module, version in REQ.findall(block):
            found[module] = version
    for module, version in re.findall(r'^require\s+(\S+)\s+(v\S+)', text, re.M):
        found[module] = version
    return found


def key(version):
    # vMAJOR.MINOR.PATCH[-pre][+meta]; a pseudo-version's timestamp sorts as text.
    core, _, pre = version.lstrip('v').split('+')[0].partition('-')
    nums = tuple(int(n) for n in core.split('.'))
    # A release sorts above any prerelease/pseudo-version of the same core.
    return nums, (1, '') if not pre else (0, pre)


def lagging(main, integration):
    return sorted(
        f'{m}: integration {integration[m]} < main {v}'
        for m, v in main.items()
        if m in integration and key(integration[m]) < key(v)
    )


class IntegrationModuleSync(unittest.TestCase):
    def test_integration_requirements_not_below_main(self):
        problems = lagging(requirements(ROOT / 'go.mod'),
                           requirements(ROOT / 'integration/go.mod'))
        self.assertEqual(problems, [], 'run `go mod tidy` in integration/')

    def test_detects_lagging_version(self):
        # Negative test: the exact drift the OpenTelemetry bump left behind.
        self.assertEqual(
            lagging({'go.opentelemetry.io/otel/sdk': 'v1.47.0',
                     'google.golang.org/genproto/googleapis/rpc': 'v0.0.0-20260928230214-8a89bd6388cc'},
                    {'go.opentelemetry.io/otel/sdk': 'v1.46.0',
                     'google.golang.org/genproto/googleapis/rpc': 'v0.0.0-20260819154853-08b0e4226688'}),
            ['go.opentelemetry.io/otel/sdk: integration v1.46.0 < main v1.47.0',
             'google.golang.org/genproto/googleapis/rpc: integration '
             'v0.0.0-20260819154853-08b0e4226688 < main v0.0.0-20260928230214-8a89bd6388cc'])

    def test_higher_or_equal_is_fine(self):
        self.assertEqual(lagging({'a': 'v1.2.0', 'b': 'v1.0.0-rc.1'},
                                 {'a': 'v1.10.0', 'b': 'v1.0.0'}), [])

    def test_parses_both_require_forms(self):
        tmp = ROOT / '.tools/tmp'
        tmp.mkdir(parents=True, exist_ok=True)
        f = tmp / 'go.mod.sync-test'
        f.write_text('module x\n\nrequire one v1.0.0\n\nrequire (\n\ttwo v2.0.0 // indirect\n)\n')
        try:
            self.assertEqual(requirements(f), {'one': 'v1.0.0', 'two': 'v2.0.0'})
        finally:
            f.unlink()


if __name__ == '__main__':
    unittest.main()
