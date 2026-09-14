#!/usr/bin/env python3
"""Run real corectl/systemd checks on an otherwise core-free Linux test machine.

Requires root, systemd, and built Linux amd64/arm64 agent and sing-box binaries.
Downloads use a local authenticated HTTP fixture; no public node is contacted.
All test services and files are removed in finally, including on assertion failure.
"""
import argparse
import base64
import hashlib
import http.server
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import socket
import subprocess
import tempfile
import threading
import time
import urllib.request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--agent', required=True)
    parser.add_argument('--core', required=True)
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    assert os.geteuid() == 0, 'root required'
    owned = [Path(p) for p in ['/usr/local/bin/sing-box', '/etc/sing-box',
             '/var/log/sing-box', '/var/lib/vps-agent', '/etc/systemd/system/sing-box.service']]
    for path in owned:
        assert not path.exists(), f'refusing to replace existing {path}'
    assert subprocess.run(['systemctl', 'show', 'sing-box.service', '-p', 'LoadState', '--value'],
                          capture_output=True, text=True, timeout=10).stdout.strip() == 'not-found'
    agent, core = str(Path(args.agent).resolve()), Path(args.core).resolve()
    work = Path(tempfile.mkdtemp(prefix='vps-corectl-smoke-'))
    token = secrets.token_hex(32)
    payload_size = 1_000_000
    downloads = []
    processes = []
    evidence = {'platform': 'Linux systemd', 'download_fixture': 'authenticated loopback HTTP', 'checks': {}}

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path.startswith('/api/agent/corefiles/'):
                if self.headers.get('Authorization') != 'Bearer ' + token:
                    self.send_error(401)
                    return
                downloads.append(self.path)
                self.send_response(200)
                self.send_header('Content-Length', str(core.stat().st_size))
                self.end_headers()
                with core.open('rb') as source:
                    shutil.copyfileobj(source, self.wfile)
            elif self.path == '/payload':
                self.send_response(200)
                self.send_header('Content-Length', str(payload_size))
                self.end_headers()
                self.wfile.write(b'x' * payload_size)
            else:
                self.send_error(404)

        def log_message(self, *_):
            pass

    httpd = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    http_port = httpd.server_port
    cfg = work / 'agent.yaml'
    cfg.write_text(f'server: ws://127.0.0.1:{http_port}/api/agent/ws\ntoken: {token}\n')
    cfg.chmod(0o600)

    def command(*values, ok=True):
        result = subprocess.run([agent, 'core', *values, '--config', str(cfg)],
                                capture_output=True, text=True, timeout=120)
        if ok:
            assert result.returncode == 0, result.stderr
        else:
            assert result.returncode != 0, 'expected failure'
        return json.loads(result.stdout)

    def write_config(name, content):
        path = work / name
        path.write_text(json.dumps(content))
        path.chmod(0o600)
        return str(path)

    def free_port():
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            return sock.getsockname()[1]

    try:
        version_output = subprocess.check_output([str(core), 'version'], text=True, timeout=10)
        version = 'v' + version_output.splitlines()[0].split()[2].removeprefix('v')
        digest = hashlib.file_digest(core.open('rb'), 'sha256').hexdigest()
        installed = command('install', '--version', version, '--sha256', digest)
        assert installed['installed_version'] == version and downloads
        assert hashlib.file_digest(owned[0].open('rb'), 'sha256').hexdigest() == digest
        evidence['checks']['install_sha256_version_unit'] = True

        ss_port, proxy_port = free_port(), free_port()
        server_key, user_key = [base64.b64encode(secrets.token_bytes(16)).decode() for _ in range(2)]
        config = {
            'log': {'level': 'info', 'timestamp': True},
            'inbounds': [{'type': 'shadowsocks', 'tag': 'ss-test', 'listen': '127.0.0.1',
                          'listen_port': ss_port, 'method': '2022-blake3-aes-128-gcm',
                          'password': server_key, 'users': [{'name': 'sub-1', 'password': user_key}]}],
            'outbounds': [{'type': 'direct'}],
            'experimental': {'v2ray_api': {'listen': '127.0.0.1:10085',
                             'stats': {'enabled': True, 'users': ['sub-1'], 'inbounds': ['ss-test']}}}}
        good = write_config('good.json', config)
        ports = f'{ss_port}/tcp,{ss_port}/udp'
        applied = command('apply', '--file', good, '--version', version, '--ports', ports)
        assert applied['running'] and applied['applied_revision'] == 1
        assert set(ports.split(',')) <= set(applied['listening'])
        expected_sha = applied['config_sha256']
        assert Path('/etc/sing-box/config.json').stat().st_mode & 0o777 == 0o600
        evidence['checks']['real_apply_listening_permissions'] = True

        invalid = write_config('invalid.json', {'inbounds': [{'type': 'not-a-protocol'}]})
        rejected = command('apply', '--file', invalid, '--version', version, ok=False)
        assert rejected['running'] and rejected['applied_revision'] == 1
        assert rejected['config_sha256'] == expected_sha and 'check failed' in rejected['error']
        evidence['checks']['invalid_config_preserves_service'] = True

        with socket.socket() as occupied:
            occupied.bind(('127.0.0.1', 0))
            occupied.listen()
            conflict_port = occupied.getsockname()[1]
            conflict = json.loads(json.dumps(config))
            conflict['inbounds'][0]['listen_port'] = conflict_port
            bad = write_config('conflict.json', conflict)
            rolled = command('apply', '--file', bad, '--version', version,
                             '--ports', f'{conflict_port}/tcp,{conflict_port}/udp', ok=False)
            assert rolled['running'] and rolled['applied_revision'] == 1
            assert rolled['config_sha256'] == expected_sha and 'rolled back' in rolled['error']
        assert hashlib.sha256(Path('/etc/sing-box/config.json').read_bytes()).hexdigest() == expected_sha
        evidence['checks']['occupied_port_rollback_restores_service'] = True

        client_config = {
            'inbounds': [{'type': 'mixed', 'listen': '127.0.0.1', 'listen_port': proxy_port}],
            'outbounds': [{'type': 'shadowsocks', 'server': '127.0.0.1', 'server_port': ss_port,
                           'method': '2022-blake3-aes-128-gcm', 'password': server_key + ':' + user_key}]}
        client_file = write_config('client.json', client_config)
        client_log = (work / 'client.log').open('w')
        processes.append(subprocess.Popen([str(core), 'run', '-c', client_file], stdout=client_log, stderr=client_log))
        for _ in range(50):
            try:
                with socket.create_connection(('127.0.0.1', proxy_port), timeout=0.1):
                    break
            except OSError:
                time.sleep(0.1)
        result = subprocess.run(['curl', '--noproxy', '', '-fsS', '--max-time', '15',
                                 '-x', f'http://127.0.0.1:{proxy_port}', f'http://127.0.0.1:{http_port}/payload',
                                 '-o', '/dev/null'], capture_output=True, timeout=20)
        assert result.returncode == 0, result.stderr.decode()
        time.sleep(0.2)
        first, second = command('stats'), command('stats')
        assert first['users'][0]['down'] >= payload_size
        assert second['users'][0]['down'] == 0 and second['users'][0]['up'] == 0
        evidence['stats_first'], evidence['stats_after_reset'] = first, second
        evidence['checks']['real_user_stats_and_reset'] = True
        assert command('logs', '--lines', '10')['text']
        evidence['checks']['real_log_tail'] = True

        stopped = command('stop')
        assert not stopped['running']
        assert command('start')['running'] and command('restart')['running']
        evidence['checks']['start_stop_restart'] = True

        # Prevent systemd's normal 3s restart from hiding a crash between 10s polls.
        dropin = Path('/etc/systemd/system/sing-box.service.d')
        assert not dropin.exists(), 'refusing to change an existing drop-in'
        dropin.mkdir()
        owned.append(dropin)
        (dropin / 'smoke.conf').write_text('[Service]\nRestart=no\n')
        subprocess.run(['systemctl', 'daemon-reload'], check=True, timeout=30)
        daemon_log = (work / 'agent.log').open('w')
        daemon = subprocess.Popen([agent, '--config', str(cfg)], stdout=daemon_log, stderr=daemon_log)
        processes.append(daemon)
        # Host prewarming can spend a few seconds checking IPv6 before core.Run.
        for _ in range(150):
            if 'agent 启动' in (work / 'agent.log').read_text():
                break
            time.sleep(0.1)
        else:
            raise AssertionError('agent did not start')
        time.sleep(0.5)
        pid = int(subprocess.check_output(['systemctl', 'show', 'sing-box', '-p', 'MainPID', '--value'], text=True))
        started = time.monotonic()
        os.kill(pid, signal.SIGKILL)
        for _ in range(115):
            text = (work / 'agent.log').read_text()
            if 'sing-box 状态变化' in text and '"running":false' in text:
                break
            time.sleep(0.1)
        else:
            raise AssertionError('agent did not observe crash')
        evidence['crash_detection_seconds'] = round(time.monotonic() - started, 3)
        evidence['checks']['crash_observed_with_restart_disabled'] = True
        evidence['firewall'] = applied['firewall']
        evidence['passed'] = True
    finally:
        for process in reversed(processes):
            process.terminate()
            try:
                process.wait(timeout=40)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        subprocess.run(['systemctl', 'stop', 'sing-box.service'], capture_output=True, timeout=30)
        subprocess.run(['systemctl', 'disable', 'sing-box.service'], capture_output=True, timeout=30)
        for path in reversed(owned):
            if path.is_dir():
                shutil.rmtree(path)
            else:
                path.unlink(missing_ok=True)
        subprocess.run(['systemctl', 'daemon-reload'], check=True, timeout=30)
        subprocess.run(['systemctl', 'reset-failed', 'sing-box.service'], capture_output=True, timeout=30)
        httpd.shutdown()
        shutil.rmtree(work)
    Path(args.output).write_text(json.dumps(evidence, indent=2) + '\n')
    print(json.dumps(evidence, indent=2))


if __name__ == '__main__':
    main()
