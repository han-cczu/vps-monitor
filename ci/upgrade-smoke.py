#!/usr/bin/env python3
"""Verify a new server against an online-backup copy of an existing local DB."""
import argparse
from contextlib import closing
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source-db', required=True)
    parser.add_argument('--server', required=True)
    parser.add_argument('--password-file', required=True)
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    source = Path(args.source_db).resolve(strict=True)
    output = Path(args.output).resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix='upgrade-', dir=output.parent))
    data = work / 'data'
    data.mkdir()
    process = None
    log = None
    try:
        with closing(sqlite3.connect(source.as_uri() + '?mode=ro', uri=True)) as original:
            with closing(sqlite3.connect(data / 'vm.db')) as clone:
                original.backup(clone)
                assert clone.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
                before_version = clone.execute('SELECT MAX(version_id) FROM goose_db_version WHERE is_applied=1').fetchone()[0]
                before_users = clone.execute('SELECT id,username,password_hash FROM users ORDER BY id').fetchall()
                before_nodes = clone.execute('SELECT id,name,token_hash FROM servers ORDER BY id').fetchall()
                history = {name: clone.execute(f'SELECT COUNT(*) FROM {name}').fetchone()[0]
                           for name in ('metrics_minute', 'metrics_hour')}
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            port = listener.getsockname()[1]
        env = os.environ.copy()
        env.update(VM_LISTEN=f'127.0.0.1:{port}', VM_DATA_DIR=str(data),
                   VM_JWT_SECRET=secrets.token_hex(32), VM_TZ='Asia/Shanghai')
        log = (work / 'server.log').open('w')
        process = subprocess.Popen([str(Path(args.server).resolve(strict=True))], env=env,
                                   stdout=log, stderr=log,
                                   creationflags=0x08000000 if os.name == 'nt' else 0)
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        base = f'http://127.0.0.1:{port}'
        for _ in range(150):
            assert process.poll() is None, 'upgraded server did not start'
            try:
                with opener.open(base + '/api/health', timeout=1) as response:
                    if response.status == 200:
                        break
            except OSError:
                time.sleep(.1)
        else:
            raise AssertionError('upgraded server health timed out')
        token = ''

        def api(path, body=None):
            request = urllib.request.Request(base + path,
                      data=json.dumps(body).encode() if body is not None else None,
                      headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token})
            with opener.open(request, timeout=10) as response:
                return json.load(response)

        password = Path(args.password_file).read_text(encoding='utf-8').strip()
        token = api('/api/auth/sign-in', {'username': 'admin', 'password': password})['accessToken']
        assert len(api('/api/servers')['servers']) == len(before_nodes)
        assert 'site.title' in api('/api/settings')
        assert 'subscribers' in api('/api/subscribers')
        with closing(sqlite3.connect(data / 'vm.db')) as clone:
            assert clone.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
            after_version = clone.execute('SELECT MAX(version_id) FROM goose_db_version WHERE is_applied=1').fetchone()[0]
            assert after_version == 12
            assert before_users == clone.execute('SELECT id,username,password_hash FROM users ORDER BY id').fetchall()
            assert before_nodes == clone.execute('SELECT id,name,token_hash FROM servers ORDER BY id').fetchall()
            after_history = {name: clone.execute(f'SELECT COUNT(*) FROM {name}').fetchone()[0] for name in history}
        evidence = {'passed': True, 'source_modified': False,
                    'migration_before': before_version, 'migration_after': after_version,
                    'users_preserved': len(before_users), 'nodes_preserved': len(before_nodes),
                    'history_before': history, 'history_after': after_history,
                    'existing_password_login': True, 'new_settings_subscribers_api': True,
                    'integrity_check': 'ok'}
        output.write_text(json.dumps(evidence, indent=2) + '\n', encoding='utf-8')
        print(json.dumps(evidence, indent=2))
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        if log:
            log.close()
        shutil.rmtree(work)


if __name__ == '__main__':
    main()
