#!/usr/bin/env python3
"""The published FerretDB image runs on distroless static, with no shell.

The ghcr.io/wekan/ferretdb image was debian:trixie-slim, and image scanners
listed 255 vulnerabilities in packages FerretDB never uses. It then became
distroless base-nossl-debian13 with libgcc, libstdc++ and libatomic for the
Node.js of a bundled mongosh, and every one of Artifact Hub's 42 findings was
in glibc and those C++ runtime libraries. The ferretdb binary is static and
uses neither, so mongosh is gone and Dockerfile.release ends in
gcr.io/distroless/static-debian13 - CA certificates, tzdata, netbase and media
types only - and assembles the same set into scratch for 386, which distroless
does not publish. This pins:

  * every runtime-<arch> stage is distroless static-debian13, except 386,
    which is scratch, and the final stage is FROM runtime-${TARGETARCH};
  * no C library, no C++ runtime, no Node.js, no mongosh and no OpenSSL in the
    image: none is added, and no base has them;
  * the final stage has no shell-form RUN, no package manager and no shell:
    the smoke test is an exec-form RUN;
  * the runtime platforms are exactly what release-platforms.sh selects, and
    the distroless ones are exactly what distroless publishes;
  * the image keeps uid/gid 1000 for /state, so existing volumes still work.

Nothing here builds an image or touches the network.
"""
import json
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
DOCKERFILE = ROOT / 'Dockerfile.release'
DISTROLESS = 'gcr.io/distroless/static-debian13:nonroot'

# The platforms gcr.io/distroless/static-debian13 publishes (its manifest list on
# 2026-11-25), as buildx TARGETARCH names. arm is arm/v7 only.
DISTROLESS_ARCHES = {'amd64', 'arm64', 'arm', 'ppc64le', 's390x', 'riscv64'}
# Assembled from debian:trixie-slim into scratch by Dockerfile.release.
ASSEMBLED_ARCHES = {'386'}

PLATFORM_ARCH = {
    'linux/amd64': 'amd64', 'linux/arm64': 'arm64', 'linux/arm/v7': 'arm',
    'linux/386': '386', 'linux/ppc64le': 'ppc64le', 'linux/s390x': 's390x',
    'linux/riscv64': 'riscv64',
}

SHELL_WORDS = re.compile(r'\b(apt-get|apt|dpkg|apk|sh|bash|busybox|curl|wget)\b')


def stages(text):
    """[(from-line, name, [instructions])] with continuation lines joined."""
    joined = re.sub(r'\\\n', ' ', text)
    result = []
    for line in joined.splitlines():
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        m = re.match(r'FROM\s+(?:--platform=\S+\s+)?(\S+)(?:\s+AS\s+(\S+))?', line, re.I)
        if m:
            result.append((m.group(1), m.group(2), []))
        elif result:
            result[-1][2].append(line)
    return result


def runtime_problems(text):
    """What makes the final stage something other than a shell-free distroless one."""
    problems = []
    all_stages = stages(text)
    if not all_stages:
        return ['no stages']
    base, name, body = all_stages[-1]
    if base != 'runtime-${TARGETARCH}':
        problems.append(f'final stage is FROM {base}, not runtime-${{TARGETARCH}}')
    runtimes = {n[len('runtime-'):]: b for b, n, _ in all_stages if n and n.startswith('runtime-')}
    for arch, image in sorted(runtimes.items()):
        want = 'scratch' if arch in ASSEMBLED_ARCHES else DISTROLESS
        if image != want:
            problems.append(f'runtime-{arch} is FROM {image}, not {want}')
    for line in body:
        if not line.upper().startswith('RUN'):
            continue
        command = line[3:].strip()
        try:
            argv = json.loads(command)
        except ValueError:
            problems.append(f'shell-form RUN in the final stage: {line}')
            continue
        if not isinstance(argv, list) or SHELL_WORDS.search(Path(str(argv[0])).name):
            problems.append(f'RUN in the final stage uses a shell or package manager: {line}')
    if not any(line.startswith('USER ') for line in body):
        problems.append('final stage has no USER')
    return problems


def runtime_arches(text):
    return {n[len('runtime-'):] for _, n, _ in stages(text)
            if n and n.startswith('runtime-')}


class DistrolessRuntime(unittest.TestCase):
    def setUp(self):
        self.text = DOCKERFILE.read_text(encoding='utf-8')
        self.final = stages(self.text)[-1][2]

    def test_final_stage_is_distroless_without_a_shell(self):
        self.assertEqual(runtime_problems(self.text), [])
        self.assertNotIn('debian:trixie-slim AS final', self.text)
        self.assertNotIn('nonroot@sha256', self.text, 'tags, as the other base images: newest distroless rebuild')

    def test_runtime_arches_are_what_the_bases_publish(self):
        self.assertEqual(runtime_arches(self.text), DISTROLESS_ARCHES | ASSEMBLED_ARCHES)

    def test_runtime_arches_match_release_platforms(self):
        with tempfile.TemporaryDirectory(dir=None) as tmp:
            for arch in ('amd64', 'arm64', 'armhf', 'i386', 'ppc64le', 's390x', 'riscv64',
                         'armv6', 'armel', 'loong64'):
                (Path(tmp) / f'ferretdb-{arch}').write_text('binary')
            out = subprocess.run(['bash', str(ROOT / 'build/ferretdb/release-platforms.sh'), tmp],
                                 capture_output=True, text=True, check=True).stdout.strip()
        platforms = out.split(',')
        self.assertEqual({PLATFORM_ARCH[p] for p in platforms}, runtime_arches(self.text))
        self.assertEqual(len(platforms), len(set(platforms)))

    def test_every_emulated_platform_is_registered(self):
        workflow = (ROOT / '.github/workflows/docker.yml').read_text(encoding='utf-8')
        installed = re.search(r'binfmt:latest --install (\S+)', workflow).group(1).split(',')
        # 386 runs natively on the amd64 runner; every other foreign CPU needs QEMU.
        self.assertEqual(set(installed), DISTROLESS_ARCHES - {'amd64'})
        self.assertIn('python3 -B tests/docker-runtime.py', workflow)

    def test_the_same_user_owns_state(self):
        self.assertEqual((ROOT / 'build/ferretdb/passwd').read_text().split(':')[2:4], ['1000', '1000'])
        self.assertEqual((ROOT / 'build/ferretdb/group').read_text().split(':')[2], '1000')
        self.assertIn('chown 1000:1000 /state', self.text)
        self.assertIn('COPY --from=pick --chown=1000:1000 /state /state', self.final)
        self.assertIn('USER ferretdb:ferretdb', self.final)
        for line in ('ENTRYPOINT ["/ferretdb"]', 'VOLUME /state', 'EXPOSE 27017 8088', 'WORKDIR /',
                     'ENV FERRETDB_STATE_DIR=/state', 'ENV FERRETDB_SQLITE_URL=file:/state/'):
            self.assertIn(line, self.final)
        self.assertNotIn('HEALTHCHECK', self.text, 'a HEALTHCHECK would need a shell or curl')

    def test_no_mongosh_node_or_c_runtime_in_the_image(self):
        # The instructions, not the comments that say why these are gone.
        code = '\n'.join(l for l in self.text.splitlines() if not l.lstrip().startswith('#'))
        for word in ('mongosh', '/opt/', 'node', 'launcher', 'libc6', 'libstdc++', 'libgcc', 'libatomic',
                     'ld-linux', 'gcc-14-base'):
            self.assertNotIn(word, code, word)
        self.assertEqual([l for l in self.final if l.upper().startswith('RUN')], ['RUN ["/ferretdb", "--version"]'])
        self.assertIn('ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin', self.final)
        self.assertFalse((ROOT / 'build/ferretdb/launcher').exists())
        self.assertFalse((ROOT / 'build/ferretdb/official-ppc64le-node.sh').exists())

    def test_386_root_has_what_distroless_static_has(self):
        for package in ('base-files', 'netbase', 'tzdata', 'tzdata-legacy', 'media-types', 'ca-certificates'):
            self.assertRegex(self.text, rf'for p in [^;]*\b{re.escape(package)}\b')
        for needed in ('/etc/ssl/certs/ca-certificates.crt', 'mkdir -p -m 1777 /add/tmp',
                       '/add/var/lib/dpkg/status.d', 'COPY --from=static-root /add/ /'):
            self.assertIn(needed, self.text)

    def test_no_openssl_in_the_image(self):
        self.assertNotRegex(self.text, r'^FROM\s+\S*distroless/(cc|base)-debian', 'a distroless base with libssl3')
        self.assertNotRegex(self.text, r'add_package[^;\n]*\blibssl|for p in [^;]*\blibssl|install[^\n]*\blibssl')
        self.assertNotRegex(self.text, r'(?m)^FROM\s+\S*distroless/(?!static-debian13:nonroot)')

    def test_negative_debian_or_shell_runtimes_are_refused(self):
        good = self.text
        cases = {
            'debian final': good.replace('FROM runtime-${TARGETARCH} AS final', 'FROM debian:trixie-slim AS final'),
            'shell RUN': good.replace('RUN ["/ferretdb", "--version"]', 'RUN /ferretdb --version'),
            'apt in final': good.replace('RUN ["/ferretdb", "--version"]', 'RUN ["apt-get", "update"]'),
            'sh in exec form': good.replace('RUN ["/ferretdb", "--version"]', 'RUN ["/bin/sh", "-c", "true"]'),
            'other base': good.replace('FROM gcr.io/distroless/static-debian13:nonroot AS runtime-s390x',
                                       'FROM debian:trixie-slim AS runtime-s390x'),
            'a base with a C library': good.replace('FROM gcr.io/distroless/static-debian13:nonroot AS runtime-arm64',
                                                    'FROM gcr.io/distroless/base-nossl-debian13:nonroot AS runtime-arm64'),
            '386 not scratch': good.replace('FROM scratch AS runtime-386', 'FROM debian:trixie-slim AS runtime-386'),
            'no user': good.replace('USER ferretdb:ferretdb', ''),
        }
        for name, text in cases.items():
            with self.subTest(name):
                self.assertNotEqual(text, good, 'the case changes the Dockerfile')
                self.assertNotEqual(runtime_problems(text), [])
        dropped = good.replace('FROM gcr.io/distroless/static-debian13:nonroot AS runtime-riscv64\n', '')
        self.assertNotEqual(runtime_arches(dropped), DISTROLESS_ARCHES | ASSEMBLED_ARCHES)


if __name__ == '__main__':
    unittest.main()
