#!/usr/bin/env python3
"""Each release binary is attached as soon as it is built, not after all of them.

Runs the real build.sh matrix with an offline compiler fixture and a recording
publisher, runs the real publish-release-asset.sh against a fake `gh`, and
reads both release workflows to pin the order of their steps.
"""
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[1]
PUBLISH = ROOT / 'build/ferretdb/publish-release-asset.sh'
WORKFLOWS = ('release-all.yml', 'release-all-missing.yml')
ALWAYS = "if: ${{ always() && steps.create.outcome == 'success' }}"
spec = importlib.util.spec_from_file_location('audit', ROOT / 'build/ferretdb/check-telemetry.py')
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


def targets():
    text = (ROOT / 'build.sh').read_text()
    block = text.split('FERRETDB_DIST_TARGETS=(', 1)[1].split('\n)', 1)[0]
    return re.findall(r'^  "([^ "]+) ', block, re.M)


def fixture(root: Path, events: Path):
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
    go.write_text(f"""#!/bin/sh
out=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then shift; out="$1"; fi
  shift
done
[ -n "$out" ] || exit 0
[ "$out" != /dev/null ] || exit 0
mkdir -p "$(dirname "$out")"
printf 'clean binary' > "$out"
echo "build $(basename "$out")" >> '{events}'
""")
    go.chmod(0o755)
    publisher = root / 'publish'
    publisher.write_text(f"""#!/bin/sh
echo "publish $(basename "$1")" >> '{events}'
[ "$(basename "$1")" != "$FAIL_PUBLISH" ]
""")
    publisher.chmod(0o755)
    return publisher


class BuildAttachesEachBinary(unittest.TestCase):
    def run_dist(self, mode, fail=''):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / 'repo'
            events = Path(tmp) / 'events'
            publisher = fixture(root, events)
            result = subprocess.run(
                ['bash', str(root / 'build.sh'), mode], cwd=root,
                env={**os.environ, 'FERRETDB_DIST_PUBLISH': str(publisher),
                     'FAIL_PUBLISH': fail},
                capture_output=True, text=True, timeout=120)
            lines = events.read_text().splitlines() if events.exists() else []
            return result, lines

    def test_sequential_build_publishes_each_binary_before_the_next_build(self):
        result, lines = self.run_dist('dist-seq')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        expected = []
        for name in targets():
            exe = '.exe' if name.startswith('win') else ''
            expected += [f'build ferretdb-{name}{exe}', f'publish ferretdb-{name}{exe}']
        self.assertEqual(len(expected), 50)
        self.assertEqual(lines, expected)

    def test_parallel_build_publishes_every_binary_once(self):
        result, lines = self.run_dist('dist-par')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        published = [l for l in lines if l.startswith('publish ')]
        self.assertEqual(len(published), 25)
        self.assertEqual(len(set(published)), 25)
        for line in published:
            self.assertLess(lines.index(line.replace('publish', 'build', 1)), lines.index(line))

    def test_failed_attach_keeps_building_and_fails_the_run(self):
        result, lines = self.run_dist('dist-seq', fail='ferretdb-arm64')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('could not attach to the release: arm64', result.stdout + result.stderr)
        self.assertEqual(len([l for l in lines if l.startswith('build ')]), 25)

    def test_without_publisher_build_sh_only_builds(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / 'repo'
            events = Path(tmp) / 'events'
            fixture(root, events)
            env = {k: v for k, v in os.environ.items() if k != 'FERRETDB_DIST_PUBLISH'}
            result = subprocess.run(['bash', str(root / 'build.sh'), 'dist-seq'], cwd=root,
                                    env=env, capture_output=True, text=True, timeout=120)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertFalse([l for l in events.read_text().splitlines() if l.startswith('publish')])


class PublishScript(unittest.TestCase):
    def run_publish(self, files, gh_failures, attempts='3'):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        base = Path(tmp.name)
        (base / 'bin').mkdir()
        (base / 'dist').mkdir()
        calls = base / 'calls'
        counter = base / 'count'
        counter.write_text('0')
        gh = base / 'bin/gh'
        gh.write_text(f"""#!/bin/sh
echo "$*" >> '{calls}'
n=$(cat '{counter}'); n=$((n + 1)); echo "$n" > '{counter}'
[ "$n" -gt {gh_failures} ]
""")
        gh.chmod(0o755)
        paths = []
        for name, content in files:
            p = base / 'dist' / name
            p.write_text(content)
            paths.append(str(p))
        env = {**os.environ, 'PATH': f"{base / 'bin'}{os.pathsep}{os.environ['PATH']}",
               'FERRETDB_RELEASE_TAG': 'v1.99.0', 'FERRETDB_UPLOAD_ATTEMPTS': attempts,
               'FERRETDB_UPLOAD_DELAY': '0'}
        result = subprocess.run(['bash', str(PUBLISH), *paths], env=env,
                                capture_output=True, text=True, timeout=60)
        made = calls.read_text().splitlines() if calls.exists() else []
        return result, made, base / 'dist'

    def test_binary_is_attached_with_its_checksum(self):
        result, calls, dist = self.run_publish([('ferretdb-amd64', 'bytes')], 0)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(calls), 1)
        self.assertRegex(calls[0], r'^release upload v1\.99\.0 \S*/ferretdb-amd64 '
                                   r'\S*/ferretdb-amd64\.sha256sum --clobber$')
        check = subprocess.run(['sha256sum', '-c', 'ferretdb-amd64.sha256sum'], cwd=dist,
                               capture_output=True, text=True)
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        self.assertTrue((dist / 'ferretdb-amd64.sha256sum').read_text()
                        .endswith('  ferretdb-amd64\n'))

    def test_readme_is_attached_without_a_checksum(self):
        result, calls, dist = self.run_publish([('README.md', '# x')], 0)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertRegex(calls[0], r'^release upload v1\.99\.0 \S*/README\.md --clobber$')
        self.assertFalse((dist / 'README.md.sha256sum').exists())

    def test_transient_failures_are_retried(self):
        result, calls, _ = self.run_publish([('ferretdb-win64.exe', 'x')], 2)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(calls), 3)

    def test_gives_up_after_the_last_attempt(self):
        result, calls, _ = self.run_publish([('ferretdb-amd64', 'x')], 99, attempts='2')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(calls), 2)
        self.assertIn('after 2 attempts', result.stderr)

    def test_requires_a_tag(self):
        env = {k: v for k, v in os.environ.items() if k != 'FERRETDB_RELEASE_TAG'}
        result = subprocess.run(['bash', str(PUBLISH), 'x'], env=env,
                                capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)


class Workflows(unittest.TestCase):
    def steps(self, name):
        text = (ROOT / '.github/workflows' / name).read_text()
        return text, re.split(r'\n      - (?:name|uses): ', text)

    def test_release_exists_before_the_build_and_the_build_attaches(self):
        for name in WORKFLOWS:
            with self.subTest(workflow=name):
                text, steps = self.steps(name)
                create = next(i for i, s in enumerate(steps) if 'gh release create' in s)
                build = next(i for i, s in enumerate(steps) if './build.sh dist-seq' in s)
                self.assertLess(create, build)
                self.assertIn('FERRETDB_DIST_PUBLISH: ${{ github.workspace }}/build/ferretdb/'
                              'publish-release-asset.sh', steps[build])
                self.assertIn('FERRETDB_RELEASE_TAG: ${{ steps.ver.outputs.version }}', steps[build])
                self.assertIn('GH_TOKEN:', steps[build])

    def test_no_step_uploads_the_binaries_after_the_build(self):
        # Binaries reach the release only through publish-release-asset.sh,
        # from inside the build. A bulk upload after it is the old behaviour.
        for name in WORKFLOWS:
            with self.subTest(workflow=name):
                text, _ = self.steps(name)
                self.assertNotIn('gh release upload', text)
                self.assertNotRegex(text, r'gh release create[^\n]*dist/')

    def test_final_steps_still_check_every_built_file_is_attached(self):
        for name in WORKFLOWS:
            with self.subTest(workflow=name):
                text, steps = self.steps(name)
                self.assertIn('sha256sum -c', text)
                check = [s for s in steps if 'attach-finished.sh --check' in s]
                self.assertEqual(len(check), 1)
                self.assertIn(ALWAYS, check[0])

    def test_cancelled_run_still_attaches_finished_binaries(self):
        # A cancel lands in the build step. The catch-up attach and the
        # missing-asset check must still run, gated only on the release
        # existing - never on the build step having succeeded.
        for name in WORKFLOWS:
            with self.subTest(workflow=name):
                text, steps = self.steps(name)
                create = next(i for i, s in enumerate(steps) if 'gh release create' in s)
                self.assertIn('id: create', steps[create])
                build = next(i for i, s in enumerate(steps) if './build.sh dist-seq' in s)
                self.assertIn('id: build', steps[build])
                attach = [i for i, s in enumerate(steps)
                          if re.search(r'attach-finished\.sh\s*$', s, re.M)]
                self.assertEqual(len(attach), 1)
                self.assertEqual(attach[0], build + 1)
                self.assertIn(ALWAYS, steps[attach[0]])

    def test_negative_no_cancel_unsafe_conditions(self):
        for name in WORKFLOWS:
            with self.subTest(workflow=name):
                text, steps = self.steps(name)
                # !cancelled() is exactly what skips these on a cancel.
                self.assertNotIn('cancelled()', text)
                for s in steps:
                    if 'attach-finished.sh' in s:
                        self.assertNotIn("steps.build.outcome == 'success'", s)
                    # Nothing that needs the whole build may run after a cancel.
                    if 'gh workflow run docker.yml' in s or 'retry-docker-release.py' in s \
                            or 'gh release edit' in s:
                        self.assertNotIn('always()', s)


class AttachFinished(unittest.TestCase):
    """The catch-up that runs with always(): attach what FINISHED, nothing else."""

    def run_attach(self, built, files, on_release, *args):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        base = Path(tmp.name)
        root = base / 'repo'
        (root / 'build/ferretdb').mkdir(parents=True)
        for script in ('attach-finished.sh', 'publish-release-asset.sh'):
            shutil.copy(ROOT / 'build/ferretdb' / script, root / 'build/ferretdb')
        (root / 'dist').mkdir()
        for name in files:
            (root / 'dist' / name).write_text(name)
        if built is not None:
            (root / 'tmp/ferretdb-dist').mkdir(parents=True)
            (root / 'tmp/ferretdb-dist/built.list').write_text(''.join(f'{n}\n' for n in built))
        (base / 'bin').mkdir()
        (base / 'assets').write_text(''.join(f'{n}\n' for n in on_release))
        calls = base / 'calls'
        gh = base / 'bin/gh'
        gh.write_text(f"""#!/bin/sh
if [ "$1 $2" = 'release view' ]; then cat '{base / 'assets'}'; exit 0; fi
echo "$*" >> '{calls}'
""")
        gh.chmod(0o755)
        env = {**os.environ, 'PATH': f"{base / 'bin'}{os.pathsep}{os.environ['PATH']}",
               'FERRETDB_RELEASE_TAG': 'v1.99.0', 'FERRETDB_UPLOAD_DELAY': '0'}
        result = subprocess.run(['bash', str(root / 'build/ferretdb/attach-finished.sh'), *args],
                                env=env, capture_output=True, text=True, timeout=60)
        uploaded = []
        if calls.exists():
            for line in calls.read_text().splitlines():
                uploaded += [Path(w).name for w in line.split() if '/' in w]
        return result, uploaded

    def test_attaches_finished_binaries_the_cancel_cut_off(self):
        result, uploaded = self.run_attach(
            ['amd64', 'arm64', 'win64'],
            ['ferretdb-amd64', 'ferretdb-arm64', 'ferretdb-win64.exe', 'ferretdb-armhf'],
            ['ferretdb-amd64', 'ferretdb-amd64.sha256sum', 'ferretdb-arm64'])
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        # arm64 lost its checksum upload, win64 never got attached.
        self.assertEqual(sorted(uploaded), ['ferretdb-arm64', 'ferretdb-arm64.sha256sum',
                                            'ferretdb-win64.exe', 'ferretdb-win64.exe.sha256sum'])

    def test_negative_never_attaches_an_unfinished_file(self):
        # armhf is in dist/ but not in built.list: interrupted mid-compile or
        # mid-audit. It must not reach the release.
        result, uploaded = self.run_attach(['amd64'], ['ferretdb-amd64', 'ferretdb-armhf'], [])
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn('ferretdb-armhf', uploaded)
        self.assertIn('ferretdb-amd64', uploaded)

    def test_nothing_finished_is_not_an_error(self):
        result, uploaded = self.run_attach(None, ['ferretdb-amd64'], [])
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(uploaded, [])

    def test_check_reports_missing_and_attaches_nothing(self):
        result, uploaded = self.run_attach(['amd64'], ['ferretdb-amd64'], ['ferretdb-amd64'],
                                           '--check')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('ferretdb-amd64', result.stderr)
        self.assertEqual(uploaded, [])

    def test_check_passes_when_everything_finished_is_attached(self):
        result, uploaded = self.run_attach(['amd64'], ['ferretdb-amd64'],
                                           ['ferretdb-amd64', 'ferretdb-amd64.sha256sum'], '--check')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(uploaded, [])


if __name__ == '__main__':
    unittest.main()
