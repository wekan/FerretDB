#!/usr/bin/env python3
"""Offline guard: everything here that runs go turns off the Go toolchain's own
telemetry first.

GOTELEMETRY=off in the environment does not do it: `go env GOTELEMETRY` still
reports "local", and every go command writes counters under the user's config
directory, because the mode is read only from the mode file that
`go telemetry off` writes. build.sh, the Dockerfiles and the release workflows
had only the environment variable.

The top-level Taskfile is checked per task: each task that runs go depends on
go-telemetry-off. integration/ and tools/ Taskfiles run through bin/task, which
init-tools builds after it.
"""
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
GO_COMMAND = re.compile(
    r"^[^#\n]*(?:(?:^|[ ;&(|])go (?:build|test|run|install|mod|vet|generate|get|list|env)\b"
    r"|actions/setup-go)", re.M)
SKIP = {'.git', '.goroot', 'tmp', 'bin', 'node_modules'}


def runs_go(text):
    return bool(GO_COMMAND.search(text))


def turns_off(text):
    return 'go telemetry off' in text


def tasks_without_it(taskfile_text):
    """Names of Taskfile tasks that run go but do not depend on go-telemetry-off."""
    missing = []
    for m in re.finditer(r'^  ([a-z0-9-]+):\n((?:    .*\n|\s*\n)*)', taskfile_text, re.M):
        name, body = m.groups()
        if name != 'go-telemetry-off' and runs_go(body) and 'go-telemetry-off' not in body:
            missing.append(name)
    return missing


def checked_files():
    for f in ROOT.rglob('*'):
        rel = f.relative_to(ROOT)
        if not f.is_file() or SKIP & set(rel.parts) or rel.parts[0] in ('integration', 'tools'):
            continue
        if f.suffix in ('.sh', '.yml') or f.name.endswith('Dockerfile'):
            yield f


class GoTelemetryOff(unittest.TestCase):
    def test_every_go_command_runs_with_go_telemetry_off(self):
        runs = [f for f in checked_files()
                if f.name != 'Taskfile.yml' and runs_go(f.read_text(errors='replace'))]
        for expected in ('build.sh', 'Dockerfile', 'build/ferretdb/production.Dockerfile',
                         '.github/workflows/release-all.yml'):
            self.assertIn(ROOT / expected, runs)
        self.assertEqual([str(f.relative_to(ROOT)) for f in runs if not turns_off(f.read_text())], [])

    def test_every_taskfile_task_that_runs_go_depends_on_it(self):
        text = (ROOT / 'Taskfile.yml').read_text()
        self.assertIn('  go-telemetry-off:\n    run: once\n    cmds:\n      - go telemetry off\n', text)
        self.assertEqual(tasks_without_it(text), [])

    def test_the_environment_variable_alone_is_refused(self):
        # Negative: what build.sh and the Dockerfile had.
        self.assertTrue(runs_go('export GOTELEMETRY=off\ngo build ./cmd/ferretdb\n'))
        self.assertFalse(turns_off('export GOTELEMETRY=off\ngo build ./cmd/ferretdb\n'))
        self.assertTrue(runs_go('ENV GOTELEMETRY=off\nRUN <<EOF\ngo mod download\nEOF\n'))
        self.assertEqual(tasks_without_it('  gen:\n    cmds:\n      - go generate -x ./...\n'), ['gen'])
        self.assertEqual(tasks_without_it('  gen:\n    deps: [go-telemetry-off]\n'
                                          '    cmds:\n      - go generate -x ./...\n'), [])
        self.assertFalse(runs_go('# `go build` is documented here\n'))


if __name__ == '__main__':
    unittest.main()
