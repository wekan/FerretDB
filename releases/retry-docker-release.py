#!/usr/bin/env python3
"""Complete Docker publication without rerunning successful matching runs."""
import json
import os
import re
import subprocess
import sys


def recover(version, repo):
    if not re.fullmatch(r'v1\.\d+\.\d+', version):
        raise ValueError('Expected a FerretDB v1 release tag')
    pages = json.loads(subprocess.check_output(['gh', 'api', '--paginate', '--slurp',
        f'repos/{repo}/actions/workflows/docker.yml/runs?per_page=100'], text=True))
    matches = [run for page in pages for run in page['workflow_runs']
               if run['display_title'] == 'Docker ' + version]
    if matches:
        run = matches[0]
        if run['status'] != 'completed' or run['conclusion'] == 'success':
            print('Docker publication is running or already succeeded for ' + version)
            return
        subprocess.run(['gh', 'run', 'rerun', str(run['id']), '--failed', '--repo', repo], check=True)
    else:
        subprocess.run(['gh', 'workflow', 'run', 'docker.yml', '--repo', repo,
                        '--ref', 'main-v1', '-f', 'version=' + version], check=True)


if __name__ == '__main__':
    try:
        recover(sys.argv[1], os.environ.get('GITHUB_REPOSITORY', 'wekan/FerretDB'))
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        print('::error::Docker recovery failed: ' + str(error), file=sys.stderr)
        sys.exit(1)
