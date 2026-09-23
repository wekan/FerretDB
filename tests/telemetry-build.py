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
                                       ('dist-par', 'beacon.ferretdb.com', 1)]:
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
                    self.assertIn('::error::Telemetry audit failed', result.stdout + result.stderr)
                    self.assertEqual(list((root / 'dist').glob('ferretdb-*')), [])


if __name__ == '__main__':
    unittest.main()
