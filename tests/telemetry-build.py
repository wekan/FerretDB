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


class Matrix(unittest.TestCase):
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
                                        env={**os.environ, 'TEST_BINARY_CONTENT': marker},
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
                        text.index('docker buildx build'))
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
