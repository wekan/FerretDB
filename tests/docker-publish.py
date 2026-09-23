#!/usr/bin/env python3
"""Exercise registry isolation using a fake Docker CLI; never publish images."""
import base64
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class PublishTests(unittest.TestCase):
    def run_publish(self, *, offline='', broken='', transient='', credentials=None, version='v1.82.0'):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            docker = directory / 'docker'
            docker.write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
log = Path(os.environ['MOCK_DOCKER_LOG'])
previous = [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []
args = sys.argv[1:]
with log.open('a') as stream:
    stream.write(json.dumps(args) + '\\n')
if args[0] == 'login':
    assert sys.stdin.read() == 'dummy-token'
    host = args[1]
    transient = host == os.environ['MOCK_TRANSIENT'] and not any(row[:2] == args[:2] for row in previous)
    sys.exit(1 if host in os.environ['MOCK_OFFLINE'].split(',') or transient else 0)
assert args[:2] == ['buildx', 'build']
repository = args[args.index('-t') + 1].split(':')[0]
sys.exit(1 if repository in os.environ['MOCK_BROKEN'].split(',') else 0)
''')
            docker.chmod(0o755)
            for name, body in [('timeout', 'shift\nexec "$@"'), ('sleep', 'exit 0')]:
                file = directory / name
                file.write_text('#!/usr/bin/env bash\n' + body + '\n')
                file.chmod(0o755)
            auth = base64.b64encode(b'dummy-user:dummy-token').decode()
            env = dict(os.environ, PATH=str(directory) + os.pathsep + os.environ['PATH'],
                       VERSION=version, PLATFORMS='linux/amd64,linux/ppc64le',
                       DOCKERHUB_AUTH=auth, QUAY_AUTH=auth, GHCR_AUTH=auth,
                       MOCK_DOCKER_LOG=str(directory / 'calls'), MOCK_OFFLINE=offline,
                       MOCK_BROKEN=broken, MOCK_TRANSIENT=transient)
            env.update(credentials or {})
            result = subprocess.run(['bash', str(ROOT / 'build/ferretdb/publish-docker.sh')],
                                    env=env, text=True, capture_output=True, timeout=15)
            calls = [json.loads(line) for line in (directory / 'calls').read_text().splitlines()] if (directory / 'calls').exists() else []
            self.assertNotIn('dummy-token', result.stdout + result.stderr)
            self.assertNotIn(auth, result.stdout + result.stderr)
            return result, calls

    def repositories(self, calls):
        return [row[row.index('-t') + 1].split(':')[0] for row in calls if row[:2] == ['buildx', 'build']]

    def test_all_registries_publish_both_tags(self):
        result, calls = self.run_publish()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.repositories(calls), ['wekanteam/ferretdb', 'quay.io/wekan/ferretdb', 'ghcr.io/wekan/ferretdb'])
        for row in calls:
            if row[0] != 'buildx':
                continue
            repo = row[row.index('-t') + 1].split(':')[0]
            self.assertIn(repo + ':v1.82.0', row)
            self.assertIn(repo + ':latest', row)
            self.assertIn('--push', row)
            self.assertIn('linux/amd64,linux/ppc64le', row)

    def test_each_offline_registry_does_not_block_others(self):
        hosts = ['docker.io', 'quay.io', 'ghcr.io']
        repos = ['wekanteam/ferretdb', 'quay.io/wekan/ferretdb', 'ghcr.io/wekan/ferretdb']
        for host, repo in zip(hosts, repos):
            with self.subTest(host=host):
                result, calls = self.run_publish(offline=host)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.repositories(calls), [r for r in repos if r != repo])
                self.assertEqual(sum(row[:2] == ['login', host] for row in calls), 2)
                self.assertIn('Published to 2 of 3', result.stdout)

    def test_build_or_push_failure_does_not_block_others(self):
        for repo in ['wekanteam/ferretdb', 'quay.io/wekan/ferretdb', 'ghcr.io/wekan/ferretdb']:
            with self.subTest(repo=repo):
                result, calls = self.run_publish(broken=repo)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(len(self.repositories(calls)), 3)
                self.assertIn('Published to 2 of 3', result.stdout)

    def test_all_offline_fails_without_push_attempts(self):
        result, calls = self.run_publish(offline='docker.io,quay.io,ghcr.io')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.repositories(calls), [])
        self.assertIn('Published to 0 of 3', result.stdout)

    def test_transient_login_recovers(self):
        result, calls = self.run_publish(transient='docker.io')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.repositories(calls)), 3)

    def test_missing_and_malformed_credentials_are_isolated(self):
        for credential in ['', '!!!', base64.b64encode(b'no-colon').decode(), base64.b64encode(b'user:').decode()]:
            with self.subTest(credential=credential):
                result, calls = self.run_publish(credentials={'DOCKERHUB_AUTH': credential})
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.repositories(calls), ['quay.io/wekan/ferretdb', 'ghcr.io/wekan/ferretdb'])

    def test_invalid_version_never_invokes_docker(self):
        result, calls = self.run_publish(version='bad tag')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, [])


if __name__ == '__main__':
    unittest.main()
