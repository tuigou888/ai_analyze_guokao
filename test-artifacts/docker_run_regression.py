"""Exercise the real deployment scripts without Docker, sudo or production data.
Docker is a command-recording double; its preflight shell runs against real temp files.
Run: python3 test-artifacts/docker_run_regression.py
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parents[1]

class DockerRunRegression(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='gk docker regression ')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root/'deploy').mkdir()
        for name in ['docker-run.sh', 'prepare-data-permissions.sh']:
            source = SOURCE/'deploy'/name
            if source.exists(): shutil.copy2(source, self.root/'deploy'/name)
        (self.root/'var/db').mkdir(parents=True)
        self.db = self.root/'var/db/gk.sqlite'
        self.key = self.root/'var/secret.key'
        self.db.write_bytes(b'synthetic database bytes, never a production database')
        self.key.write_text('a'*64)
        self.before = self.hashes()
        self.bin = self.root/'bin'; self.bin.mkdir()
        self.log = self.root/'commands.jsonl'
        self.env = os.environ.copy()
        self.env.pop('GK_SECRET_KEY', None)
        self.env.update(PATH=str(self.bin)+os.pathsep+self.env['PATH'], TEST_ROOT=str(self.root), TEST_LOG=str(self.log))
        self.stub('id', '''import os,sys
container=os.environ.get('TEST_CONTAINER')=='1'
print(os.environ.get('TEST_CONTAINER_UID','10001') if container else os.environ.get('TEST_HOST_UID','0'))
''')
        self.stub('sudo', '''import os,sys
args=sys.argv[1:]
if args and args[0]=='--':args=args[1:]
os.execvp(args[0],args)
''')
        self.stub('install', '''import os,sys,json,subprocess
args=sys.argv[1:]
with open(os.environ['TEST_LOG'],'a') as f:f.write(json.dumps(['install']+args)+'\\n')
filtered=[];i=0
while i<len(args):
 if args[i] in ['-o','-g']:i+=2
 else:filtered.append(args[i]);i+=1
sys.exit(subprocess.call(['/usr/bin/install']+filtered))
''')
        self.stub('chown', '''import os,sys,json
with open(os.environ['TEST_LOG'],'a') as f:f.write(json.dumps(['chown']+sys.argv[1:])+'\\n')
sys.exit(int(os.environ.get('TEST_CHOWN_FAIL','0')))
''')
        self.stub('find', '''import os,sys
if os.environ.get('TEST_FIND_FAIL'):sys.exit(1)
os.execv('/usr/bin/find',['find']+sys.argv[1:])
''')
        self.stub('docker', '''import os,sys,json,subprocess,shlex
args=sys.argv[1:]
with open(os.environ['TEST_LOG'],'a') as f:f.write(json.dumps(['docker']+args)+'\\n')
if args[:3]==['compose','config','--quiet']:sys.exit(int(os.environ.get('TEST_CONFIG_FAIL','0')))
if args[:2]==['compose','build']:sys.exit(int(os.environ.get('TEST_BUILD_FAIL','0')))
if args[:2]==['compose','run']:
 if os.environ.get('TEST_PREFLIGHT_FAIL'):sys.exit(1)
 body=args[-1].replace('/opt/gk/var',shlex.quote(os.environ['TEST_ROOT']+'/var'))
 env=os.environ.copy();env['TEST_CONTAINER']='1'
 sys.exit(subprocess.call(['/bin/sh','-ec',body],env=env))
sys.exit(0)
''')

    def stub(self, name, body):
        path=self.bin/name
        path.write_text('#!/usr/bin/env python3\n'+body)
        path.chmod(0o755)

    def hashes(self):
        return {p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in [self.db,self.key] if p.exists()}

    def run_action(self, action, **env):
        return subprocess.run(['/bin/sh',str(self.root/'deploy/docker-run.sh'),action],cwd='/tmp',env={**self.env,**env},capture_output=True,text=True)

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def no_service_change(self):
        self.assertFalse(any(c[:3] in [['docker','compose','stop'],['docker','compose','down'],['docker','compose','up']] for c in self.calls()),self.calls())

    def test_start_repairs_modes_preserves_contents_and_checks_actual_runtime_user(self):
        self.db.chmod(0o644);self.key.chmod(0o644)
        result=self.run_action('start')
        self.assertEqual(result.returncode,0,result.stderr)
        calls=self.calls()
        probe=next(i for i,c in enumerate(calls) if c[:3]==['docker','compose','run'])
        up=next(i for i,c in enumerate(calls) if c[:3]==['docker','compose','up'])
        self.assertLess(probe,up)
        self.assertTrue(any(c[0]=='chown' and '10001:10001' in c for c in calls))
        self.assertEqual(self.db.stat().st_mode&0o777,0o600)
        self.assertEqual(self.key.stat().st_mode&0o777,0o600)
        self.assertEqual((self.root/'var/db').stat().st_mode&0o777,0o700)
        self.assertEqual(self.hashes(),self.before)

    def test_restart_and_rebuild_check_before_stopping_and_touch_only_gk(self):
        for action in ['restart','rebuild']:
            with self.subTest(action=action):
                self.log.unlink(missing_ok=True)
                result=self.run_action(action)
                self.assertEqual(result.returncode,0,result.stderr)
                calls=self.calls()
                probe=next(i for i,c in enumerate(calls) if c[:3]==['docker','compose','run'])
                stop=next(i for i,c in enumerate(calls) if c[:3]==['docker','compose','stop'])
                up=next(i for i,c in enumerate(calls) if c[:3]==['docker','compose','up'])
                self.assertLess(probe,stop);self.assertLess(stop,up)
                self.assertEqual(calls[stop][-1],'gk');self.assertEqual(calls[up][-1],'gk')
                self.assertFalse(any(c[:3]==['docker','compose','down'] for c in calls))
                if action=='rebuild':self.assertEqual(calls[1][:3],['docker','compose','build'])

    def test_build_failure_keeps_existing_service(self):
        result=self.run_action('rebuild',TEST_BUILD_FAIL='1')
        self.assertNotEqual(result.returncode,0);self.no_service_change()
        self.assertFalse(any(c[0]=='chown' for c in self.calls()))

    def test_preflight_failure_keeps_existing_service(self):
        result=self.run_action('restart',TEST_PREFLIGHT_FAIL='1')
        self.assertNotEqual(result.returncode,0);self.no_service_change()

    def test_missing_database_does_not_start_an_empty_replacement(self):
        self.db.unlink()
        result=self.run_action('start')
        self.assertNotEqual(result.returncode,0)
        self.assertFalse(self.db.exists());self.no_service_change()
        self.assertEqual(self.hashes()['secret.key'],self.before['secret.key'])

    def test_missing_key_is_never_generated_during_deployment(self):
        self.key.unlink()
        result=self.run_action('start')
        self.assertNotEqual(result.returncode,0)
        self.assertFalse(self.key.exists());self.no_service_change()

    def test_environment_key_mode_remains_supported_without_exposing_its_value(self):
        self.key.unlink();secret='synthetic-environment-key-not-for-output'
        result=self.run_action('start',GK_SECRET_KEY=secret)
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertFalse(self.key.exists())
        self.assertNotIn(secret,result.stdout+result.stderr+self.log.read_text())

    def test_whitespace_only_environment_is_not_a_key(self):
        self.key.unlink()
        result=self.run_action('start',GK_SECRET_KEY='  \t  ')
        self.assertNotEqual(result.returncode,0);self.no_service_change()

    def test_symlinked_secret_is_rejected_without_changing_the_target(self):
        external=self.root/'external-secret';external.write_text('outside fixture');external.chmod(0o640)
        self.key.unlink();self.key.symlink_to(external)
        result=self.run_action('start')
        self.assertNotEqual(result.returncode,0);self.no_service_change()
        self.assertEqual(external.stat().st_mode&0o777,0o640)
        self.assertFalse(any(c[0]=='chown' for c in self.calls()))

    def test_symlink_inside_database_directory_is_rejected(self):
        (self.root/'var/db/linked').symlink_to(self.root/'external')
        result=self.run_action('start')
        self.assertNotEqual(result.returncode,0);self.no_service_change()

    def test_permission_fix_failure_does_not_stop_or_start_service(self):
        result=self.run_action('restart',TEST_CHOWN_FAIL='1')
        self.assertNotEqual(result.returncode,0);self.no_service_change()

    def test_runtime_uid_mismatch_is_rejected(self):
        result=self.run_action('restart',TEST_CONTAINER_UID='10002')
        self.assertNotEqual(result.returncode,0);self.no_service_change()

    def test_invalid_compose_configuration_changes_no_permissions_or_service(self):
        result=self.run_action('restart',TEST_CONFIG_FAIL='1')
        self.assertNotEqual(result.returncode,0);self.no_service_change()
        self.assertFalse(any(c[0] in ['install','chown'] for c in self.calls()))

    def test_check_does_not_change_permissions_or_service(self):
        self.db.chmod(0o640)
        result=self.run_action('check')
        self.assertEqual(result.returncode,0,result.stderr);self.no_service_change()
        self.assertFalse(any(c[0] in ['install','chown'] for c in self.calls()))
        self.assertEqual(self.db.stat().st_mode&0o777,0o640)

    def test_prepare_creates_only_directories_and_never_database_or_key(self):
        shutil.rmtree(self.root/'var')
        result=self.run_action('prepare')
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertTrue((self.root/'var/db').is_dir())
        self.assertFalse(self.db.exists());self.assertFalse(self.key.exists())
        self.no_service_change()

    def test_database_directory_cannot_pass_as_database_file(self):
        self.db.unlink();self.db.mkdir()
        result=self.run_action('start')
        self.assertNotEqual(result.returncode,0);self.no_service_change()

    def test_check_missing_var_does_not_create_mount_source(self):
        shutil.rmtree(self.root/'var')
        result=self.run_action('check')
        self.assertNotEqual(result.returncode,0)
        self.assertFalse((self.root/'var').exists())
        self.assertFalse(any(c[:3]==['docker','compose','run'] for c in self.calls()))

    def test_failed_link_scan_aborts_before_changing_permissions(self):
        result=self.run_action('start',TEST_FIND_FAIL='1')
        self.assertNotEqual(result.returncode,0);self.no_service_change()
        self.assertFalse(any(c[0] in ['install','chown'] for c in self.calls()))

    def test_unicode_whitespace_environment_is_not_a_key(self):
        self.key.unlink()
        for value in ['\u0085','\u00a0','\u1680','\u2000','\u200a','\u2028','\u2029','\u202f','\u205f','\u3000']:
            with self.subTest(value=repr(value)):
                self.log.unlink(missing_ok=True)
                result=self.run_action('restart',GK_SECRET_KEY=value)
                self.assertNotEqual(result.returncode,0);self.no_service_change()

    def test_check_rejects_secret_symlinks_without_mutation(self):
        external=self.root/'external-secret';external.write_text('a'*64);external.chmod(0o640)
        self.key.unlink();self.key.symlink_to(external)
        result=self.run_action('check')
        self.assertNotEqual(result.returncode,0);self.no_service_change()
        self.assertEqual(external.stat().st_mode&0o777,0o640)

    def test_non_root_operator_uses_sudo_for_permission_changes(self):
        result=self.run_action('start',TEST_HOST_UID='1000')
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(self.hashes(),self.before)

if __name__=='__main__':unittest.main(verbosity=2)
