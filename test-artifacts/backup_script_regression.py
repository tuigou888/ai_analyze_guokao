"""Exercise the actual deployment shell script with synthetic local databases.
Usage: python3 test-artifacts/backup_script_regression.py PATH_TO_BUILT_GK
"""
import hashlib, json, os, shutil, subprocess, sys, tempfile
from pathlib import Path

binary = Path(sys.argv[1]).resolve()
script = Path('deploy/backup.sh').read_text()
report = {'checks': []}
for mode in ['file', 'environment_only', 'environment_overrides_stale_file']:
    with tempfile.TemporaryDirectory(prefix='gk-backup-script-') as temp:
        root = Path(temp)
        (root / 'var/db').mkdir(parents=True)
        shutil.copy2(binary, root / 'gk')
        # Only replace the installation root. Run the deployed script body.
        backup_script = root / 'backup.sh'
        backup_script.write_text(script.replace('/opt/gk', str(root)))
        backup_script.chmod(0o644)  # /bin/sh must work even without executable bit.
        env = os.environ.copy()
        env.pop('GK_SECRET_KEY', None)
        if mode != 'file':
            env['GK_SECRET_KEY'] = 'synthetic-script-environment-key'
        if mode == 'environment_overrides_stale_file':
            (root / 'var/secret.key').write_text('0' * 64)
        subprocess.run([str(root / 'gk'), 'config', 'set', 'llm.api_key', 'synthetic-script-api-key'],
                       cwd=root, env=env, check=True, capture_output=True)
        source = root / 'var/db/gk.sqlite'
        source_hash = hashlib.sha256(source.read_bytes()).hexdigest()
        if mode != 'file':
            missing_env = env.copy()
            missing_env.pop('GK_SECRET_KEY')
            failed = subprocess.run(['/bin/sh', str(backup_script)], cwd=root, env=missing_env, capture_output=True)
            assert failed.returncode != 0 and b'Backup complete:' not in failed.stdout
            assert not list((root / 'var/backups').iterdir()), 'failed backup left complete-looking directory'
        succeeded = subprocess.run(['/bin/sh', str(backup_script)], cwd=root, env=env, capture_output=True)
        assert succeeded.returncode == 0, succeeded.stderr.decode()
        assert b'Backup complete:' in succeeded.stdout
        bundles = list((root / 'var/backups').iterdir())
        assert len(bundles) == 1
        clean_env = env.copy()
        clean_env.pop('GK_SECRET_KEY', None)
        subprocess.run([str(root / 'gk'), 'backup', '--verify-bundle', str(bundles[0])],
                       cwd=root, env=clean_env, check=True, capture_output=True)
        assert source_hash == hashlib.sha256(source.read_bytes()).hexdigest(), 'backup changed source database'
        report['checks'].append(f'{mode}: /bin/sh backup succeeds, validates without source environment, preserves source')
        if mode != 'file':
            report['checks'].append(f'{mode}: missing effective environment fails without success or partial bundle')

output = Path('test-artifacts/round2-fixes')
output.mkdir(exist_ok=True)
(output / 'backup-script-results.json').write_text(json.dumps(report, ensure_ascii=False, indent=2))
print(json.dumps(report, ensure_ascii=False, indent=2))
