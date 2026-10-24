#!/usr/bin/env python3
"""Run the real serial/parallel matrix with an offline compiler fixture."""
import importlib.util
import json
from pathlib import Path
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('audit', ROOT / 'build/ferretdb/check-telemetry.py')
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


def fixture_env(marker):
    """The environment of a fixture build: this process's, minus FERRETDB_*.

    The release workflows run this test in a job whose environment steers the
    real build - FERRETDB_DIST_SKIP_LIST names the assets already on the release,
    FERRETDB_DIST_PUBLISH uploads each binary, FERRETDB_RELEASE_TAG and
    FERRETDB_RELEASE_VERSION set the version. Inherited, they made the fixture
    build skip every platform as "already on the release": it built nothing, so it
    found no telemetry, and "telemetry is fatal" failed - which stopped
    release-all-missing.yml before it built or published anything.
    """
    env = {k: v for k, v in os.environ.items() if not k.startswith('FERRETDB_')}
    env['TEST_BINARY_CONTENT'] = marker
    return env


class Matrix(unittest.TestCase):
    def test_fixture_builds_ignore_the_release_jobs_environment(self):
        saved = dict(os.environ)
        try:
            os.environ.update(FERRETDB_DIST_SKIP_LIST='/release/existing.txt', FERRETDB_DIST_PUBLISH='/bin/false',
                              FERRETDB_RELEASE_TAG='v1.99.0', FERRETDB_RELEASE_VERSION='v1.99.0')
            env = fixture_env('clean binary')
        finally:
            os.environ.clear()
            os.environ.update(saved)
        self.assertEqual([k for k in env if k.startswith('FERRETDB_')], [])
        self.assertEqual(env['TEST_BINARY_CONTENT'], 'clean binary')
        self.assertIn('PATH', env, 'everything else is kept')


    def test_telemetry_is_fatal_in_both_matrix_modes(self):
        for mode, marker, expected in [('dist-seq', 'clean binary', 0),
                                       ('dist-seq', 'beacon.ferretdb.com', 1),
                                       ('dist-par', 'beacon.ferretdb.com', 1),
                                       ('build', 'clean binary', 0),
                                       ('build', 'beacon.ferretdb.com', 1),
                                       ('build', 'behavior-test-failure', 1),
                                       ('dist-seq', 'behavior-test-failure', 1),
                                       ('dist-par', 'behavior-test-failure', 1)]:
            with self.subTest(mode=mode, marker=marker), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                for name in ('cmd', 'build/ferretdb', 'build/version', '.goroot/bin'):
                    (root / name).mkdir(parents=True)
                shutil.copy(ROOT / 'build.sh', root / 'build.sh')
                shutil.copy(ROOT / 'build/ferretdb/check-telemetry.py', root / 'build/ferretdb')
                (root / 'go.mod').write_text('module fixture\n')
                (root / 'build/version/version.txt').write_text('fixture')
                policy = dict(roots=['cmd'], files=['go.mod'])
                policy['reviewed'] = audit.snapshot(root, policy)
                (root / 'build/ferretdb/telemetry-source.json').write_text(json.dumps(policy))
                go = root / '.goroot/bin/go'
                go.write_text("""#!/bin/sh
# Simulate the no-network suite finding a reporter that binary strings miss.
if [ "$1" = test ] && [ "$TEST_BINARY_CONTENT" = behavior-test-failure ]; then exit 1; fi
out=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then shift; out="$1"; fi
  shift
done
[ -n "$out" ] || exit 0
[ "$out" != /dev/null ] || exit 0
mkdir -p "$(dirname "$out")"
printf '%s' "$TEST_BINARY_CONTENT" > "$out"
""")
                go.chmod(0o755)
                result = subprocess.run(['bash', str(root / 'build.sh'), mode], cwd=root,
                                        env=fixture_env(marker),
                                        capture_output=True, text=True, timeout=60)
                if expected == 0:
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                else:
                    self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertIn('::error::Telemetry', result.stdout + result.stderr)
                    self.assertEqual(list((root / 'dist').glob('ferretdb-*')), [])
                    self.assertFalse((root / 'bin/ferretdb').exists())

    def test_workflows_use_build_gates_and_docker_rechecks_downloads(self):
        for name in ('release-all.yml', 'release-all-missing.yml'):
            text = (ROOT / '.github/workflows' / name).read_text()
            self.assertIn('./build.sh dist-seq', text)
            self.assertIn('check-telemetry.py --source .', text)
            self.assertIn('tests/telemetry-build.py', text)
            self.assertNotIn('go test ./internal/util/telemetry', text)
        text = (ROOT / '.github/workflows/docker.yml').read_text()
        self.assertLess(text.index('check-telemetry.py --kind ferretdb'),
                        text.index('run: bash build/ferretdb/publish-docker.sh'))
        publish = (ROOT / 'build/ferretdb/publish-docker.sh').read_text()
        self.assertIn('docker buildx build', publish)
        docker = (ROOT / 'Dockerfile').read_text()
        self.assertLess(docker.index('check-telemetry.py --source'), docker.index('go build -mod'))
        self.assertLess(docker.index('go test -mod=readonly'), docker.index('export GOOS='))
        self.assertIn('check-telemetry.py --kind ferretdb /bin/ferretdb', docker)
        script = (ROOT / 'build.sh').read_text()
        preflight = script.split('act_telemetry_check() {', 1)[1].split('\n}', 1)[0]
        self.assertLess(preflight.index('go run -mod=readonly generate.go'), preflight.index('go test'))
        self.assertIn('-mod=readonly -count=1', preflight)


if __name__ == '__main__':
    unittest.main()
