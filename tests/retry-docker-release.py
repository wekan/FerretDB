import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch
import json
spec=importlib.util.spec_from_file_location('docker',Path(__file__).resolve().parent.parent/'releases/retry-docker-release.py')
r=importlib.util.module_from_spec(spec);spec.loader.exec_module(r)
class DockerRecovery(unittest.TestCase):
    def test_dispatch_or_retry_failed_without_touching_success(self):
        for state in ['absent','success','failure','running']:
            rows=[] if state=='absent' else [{'id':12,'display_title':'Docker v1.100.0','status':'in_progress' if state=='running' else 'completed','conclusion':state}]
            with patch.object(r.subprocess,'check_output',return_value=json.dumps([{'workflow_runs':rows}])),patch.object(r.subprocess,'run') as run:
                r.recover('v1.100.0','wekan/FerretDB')
                if state in ['success','running']:run.assert_not_called()
                elif state=='failure':self.assertIn('--failed',run.call_args.args[0])
                else:self.assertIn('version=v1.100.0',run.call_args.args[0])
    def test_invalid_version_never_calls_github(self):
        with patch.object(r.subprocess,'check_output') as read:
            with self.assertRaises(ValueError):r.recover('v2.0.0','wekan/FerretDB')
            read.assert_not_called()
if __name__=='__main__':unittest.main()
