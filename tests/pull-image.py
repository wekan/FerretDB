#!/usr/bin/env python3
"""Pull container images through Docker Hub's mirrors, with a fake Docker CLI.

The v1.89.0 Docker image run died on Docker Hub's anonymous pull limit before it
built anything. build/ferretdb/pull-image.sh retries Docker Hub and then takes
the same image from mirror.gcr.io or Amazon ECR Public; docker.yml logs in to
Docker Hub for pulls, pulls through the helper and gives BuildKit a mirror.
Nothing here touches the network.
"""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[1]
HELPER = ROOT / 'build/ferretdb/pull-image.sh'

FAKE_DOCKER = '''#!/usr/bin/env bash
# Every call is logged; a pull succeeds only for a reference listed in $UP, and a
# Docker Hub pull answers "toomanyrequests" when $LIMIT is set.
echo "$*" >> "$CALLS"
case "$1" in
  pull)
    ref="${@: -1}"
    for up in $UP; do [ "$ref" = "$up" ] && exit 0; done
    case "$ref" in mirror.gcr.io/*|public.ecr.aws/*) ;; *)
      [ -n "${LIMIT:-}" ] && { echo "toomanyrequests: You have reached your unauthenticated pull rate limit."; exit 1; } ;;
    esac
    echo "Error response from daemon: context deadline exceeded"; exit 1 ;;
  tag) exit 0 ;;
esac
exit 1
'''


class PullImage(unittest.TestCase):
    def pull(self, *args, up='', limit=''):
        with tempfile.TemporaryDirectory(dir=os.environ.get('TMPDIR')) as tmp:
            directory = Path(tmp)
            docker = directory / 'docker'
            docker.write_text(FAKE_DOCKER)
            docker.chmod(0o755)
            calls = directory / 'calls'
            calls.write_text('')
            env = dict(os.environ, PATH=f'{directory}:{os.environ["PATH"]}', CALLS=str(calls),
                       PULL_IMAGE_SLEEP='0', UP=up, LIMIT=limit)
            result = subprocess.run(['bash', str(HELPER), *args], env=env, capture_output=True, text=True)
            return result.returncode, calls.read_text().splitlines(), result.stdout + result.stderr

    def test_docker_hub_answering_is_used_as_it_is(self):
        code, calls, _ = self.pull('debian:trixie-slim', 'linux/ppc64le', up='debian:trixie-slim')
        self.assertEqual(code, 0)
        self.assertEqual(calls, ['pull --platform linux/ppc64le debian:trixie-slim'])

    def test_the_pull_limit_is_not_asked_twice_and_the_mirror_serves(self):
        code, calls, out = self.pull('debian:trixie-slim', 'linux/ppc64le',
                                     up='mirror.gcr.io/library/debian:trixie-slim', limit='1')
        self.assertEqual(code, 0)
        self.assertEqual([c for c in calls if c.endswith(' debian:trixie-slim') and c.startswith('pull')],
                         ['pull --platform linux/ppc64le debian:trixie-slim'])
        self.assertIn('tag mirror.gcr.io/library/debian:trixie-slim debian:trixie-slim', calls)
        self.assertIn('came from mirror.gcr.io', out)

    def test_a_timeout_is_retried_before_the_mirrors(self):
        code, calls, _ = self.pull('moby/buildkit:buildx-stable-1', up='mirror.gcr.io/moby/buildkit:buildx-stable-1')
        self.assertEqual(code, 0)
        self.assertEqual(calls.count('pull moby/buildkit:buildx-stable-1'), 2)
        self.assertIn('tag mirror.gcr.io/moby/buildkit:buildx-stable-1 moby/buildkit:buildx-stable-1', calls)

    def test_an_official_image_comes_from_ecr_when_the_mirror_fails(self):
        code, calls, _ = self.pull('debian:trixie-slim', 'linux/ppc64le',
                                   up='public.ecr.aws/docker/library/debian:trixie-slim')
        self.assertEqual(code, 0)
        self.assertIn('tag public.ecr.aws/docker/library/debian:trixie-slim debian:trixie-slim', calls)

    def test_negative_every_source_down_fails_after_every_attempt(self):
        code, calls, out = self.pull('debian:trixie-slim', 'linux/ppc64le')
        self.assertNotEqual(code, 0)
        self.assertEqual(len([c for c in calls if c.startswith('pull')]), 2 + 3 + 3)
        self.assertIn('::error::Could not pull debian:trixie-slim', out)

    def test_negative_a_non_official_image_is_not_looked_for_on_ecr(self):
        code, calls, _ = self.pull('tonistiigi/binfmt:latest')
        self.assertNotEqual(code, 0)
        self.assertFalse([c for c in calls if 'public.ecr.aws' in c])


class Workflow(unittest.TestCase):
    def setUp(self):
        self.steps = yaml.safe_load((ROOT / '.github/workflows/docker.yml').read_text())['jobs']['docker']['steps']
        self.names = [step.get('name', '') for step in self.steps]

    def index(self, name):
        return self.names.index(name)

    def test_docker_hub_login_and_pulls_come_before_anything_pulls(self):
        login = self.index('Log in to Docker Hub for pulls (when the secret is set)')
        pull = self.index('Pull the images the build starts from')
        qemu = self.index('Set up target runtime emulation')
        buildx = self.index('Set up Docker Buildx')
        ppc = self.index('Prepare same-version official PowerPC Node runtime')
        self.assertLess(login, pull)
        self.assertLess(pull, qemu)
        self.assertLess(qemu, buildx)
        self.assertLess(buildx, ppc)
        run = self.steps[pull]['run']
        self.assertIn('pull-image.sh tonistiigi/binfmt:latest', run)
        self.assertIn('pull-image.sh moby/buildkit:buildx-stable-1', run)

    def test_negative_no_step_pulls_from_docker_hub_on_its_own(self):
        # setup-qemu-action pulls tonistiigi/binfmt from Docker Hub with no
        # retry and no mirror; the job registers qemu from the pulled image.
        uses = [step.get('uses', '') for step in self.steps]
        self.assertFalse([u for u in uses if u.startswith('docker/setup-qemu-action')])
        buildx = self.steps[self.index('Set up Docker Buildx')]
        self.assertEqual(buildx['with']['driver-opts'], 'image=moby/buildkit:buildx-stable-1')
        self.assertIn('mirrors = ["mirror.gcr.io"]', buildx['with']['buildkitd-config-inline'])
        self.assertIn('[registry."docker.io"]', buildx['with']['buildkitd-config-inline'])

    def test_the_login_never_fails_the_job_or_prints_the_secret(self):
        run = self.steps[self.index('Log in to Docker Hub for pulls (when the secret is set)')]['run']
        self.assertTrue(run.startswith('set +x'))
        self.assertIn('--password-stdin', run)
        self.assertIn('::warning::', run)
        self.assertNotIn('set -e', run)

    def test_the_ppc64le_check_pulls_through_the_helper(self):
        script = (ROOT / 'build/ferretdb/official-ppc64le-node.sh').read_text()
        self.assertLess(script.index('pull-image.sh" debian:trixie-slim linux/ppc64le'),
                        script.index('docker run --rm --platform linux/ppc64le'))


if __name__ == '__main__':
    unittest.main()
