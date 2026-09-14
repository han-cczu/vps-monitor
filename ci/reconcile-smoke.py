#!/usr/bin/env python3
"""Real panel -> Agent -> sing-box lifecycle on an unused Linux/systemd host.

Uses temporary panel data and loopback traffic. Refuses existing core paths;
removes only the services/files created by this run. Run as root.
"""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser()
    for name in ['server', 'agent', 'core', 'core-arm64', 'output']:
        parser.add_argument('--' + name, required=True)
    parser.add_argument('--mihomo', help='also exercise Mihomo/sing-box subscriptions and quota with real clients')
    parser.add_argument('--relay', action='store_true', help='also exercise managed A -> isolated B -> local payload and relay removal')
    parser.add_argument('--agent-next', help='optional newer version test build for real self-update')
    args = parser.parse_args()
    assert os.geteuid() == 0, 'root required'
    owned = [Path(p) for p in ['/usr/local/bin/sing-box', '/etc/sing-box',
             '/var/log/sing-box', '/var/lib/vps-agent', '/etc/systemd/system/sing-box.service', '/etc/logrotate.d/sing-box']]
    for path in owned:
        assert not path.exists(), f'refusing to replace existing {path}'
    assert subprocess.check_output(['systemctl', 'show', 'sing-box', '-p', 'LoadState', '--value'],
                                   text=True, timeout=10).strip() == 'not-found'
    for port in [10085]:
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', port))
    server, agent, core = (str(Path(p).resolve()) for p in [args.server, args.agent, args.core])
    work = Path(tempfile.mkdtemp(prefix='vps-reconcile-smoke-'))
    # Self-update may replace the running file. Never mutate supplied build artifacts.
    agent_copy = work / 'vps-agent'
    shutil.copy2(agent, agent_copy)
    agent_copy.chmod(0o755)
    agent = str(agent_copy)
    processes, handles = [], []
    evidence = {'platform': 'Linux amd64 / systemd', 'checks': {}, 'payload_bytes': 50 * 1024 * 1024}
    payload_size = evidence['payload_bytes']
    token = ''
    httpd = None

    def port():
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            return sock.getsockname()[1]

    api_port = port()
    base = f'http://127.0.0.1:{api_port}'
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def request(method, path, body=None, expected=200, raw=None, content_type='application/json'):
        data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
        headers = {'Content-Type': content_type}
        if token:
            headers['Authorization'] = 'Bearer ' + token
        req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
        try:
            with opener.open(req, timeout=30) as response:
                status, content = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, content = error.code, error.read()
        result = json.loads(content) if content else {}
        assert status == expected, f'{method} {path}: HTTP {status}: {result.get("message", "unexpected status")}'
        return result

    def wait_for(label, check, timeout=30):
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            result = check()
            if result:
                return result
            time.sleep(0.2)
        raise AssertionError('timeout: ' + label)

    def launch(command, log, env=None):
        handle = (work / log).open('a')
        handles.append(handle)
        process = subprocess.Popen(command, env=env, stdout=handle, stderr=handle)
        processes.append(process)
        return process

    def stop(process):
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=40)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)

    class Payload(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            size = 64 * 1024 if self.path == '/small' else payload_size
            self.send_response(200)
            self.send_header('Content-Length', str(size))
            self.end_headers()
            block = b'x' * (1024 * 1024)
            for offset in range(0, size, len(block)):
                self.wfile.write(block[:min(len(block), size-offset)])

        def log_message(self, *_):
            pass

    try:
        env = os.environ.copy()
        env.update(VM_LISTEN=f'127.0.0.1:{api_port}', VM_DATA_DIR=str(work / 'data'),
                   VM_JWT_SECRET=secrets.token_hex(32), VM_TZ='Asia/Shanghai', VM_LOG_LEVEL='info')
        panel = launch([server], 'server.log', env)

        def initial_password():
            for line in (work / 'server.log').read_text().splitlines():
                try:
                    item = json.loads(line)
                except ValueError:
                    continue
                if item.get('msg') == 'initial admin password (printed only once)':
                    return item['password']
            assert panel.poll() is None, 'panel failed to start'

        password = wait_for('panel bootstrap', initial_password)
        token = request('POST', '/api/auth/sign-in', {'username': 'admin', 'password': password})['accessToken']
        version_text = subprocess.check_output([core, 'version'], text=True, timeout=10)
        version = 'v' + version_text.splitlines()[0].split()[2].removeprefix('v')
        for arch, binary in [('amd64', args.core), ('arm64', args.core_arm64)]:
            path = Path(binary)
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            boundary = 'smoke-' + secrets.token_hex(16)
            parts = []
            for key, value in {'version': version, 'arch': arch, 'sha256': digest}.items():
                parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n'.encode())
            parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="core"\r\nContent-Type: application/octet-stream\r\n\r\n'.encode())
            parts.append(path.read_bytes())
            parts.append(f'\r\n--{boundary}--\r\n'.encode())
            request('POST', '/api/corefiles', expected=201, raw=b''.join(parts),
                    content_type='multipart/form-data; boundary=' + boundary)
        request('PUT', '/api/corefiles/current', {'version': version})
        node = request('POST', '/api/servers', {'name': 'isolated-core-smoke', 'currency': 'USD',
                       'billing_cycle': 'month', 'traffic_mode': 'max', 'public_host': '127.0.0.1'}, expected=201)
        node_id = node['server']['id']
        core_api = f'/api/servers/{node_id}/core'
        inbound_api = f'/api/servers/{node_id}/inbounds'
        inbounds = []
        used_ports = set()
        for protocol in ['vless', 'shadowsocks', 'hysteria2', 'tuic']:
            listen = port()
            while listen in used_ports:
                listen = port()
            used_ports.add(listen)
            inbounds.append(request('POST', inbound_api, {'protocol': protocol, 'listen_port': listen}, expected=201)['inbound'])
        user = request('POST', '/api/subscribers', {'name': 'smoke-client'}, expected=201)['subscriber']
        user_api = f'/api/subscribers/{user["id"]}'
        request('PUT', user_api + '/assignments', {'inbound_ids': [i['id'] for i in inbounds]})
        cfg = work / 'agent.yaml'
        cfg.write_text(f'server: ws://127.0.0.1:{api_port}/api/agent/ws\ntoken: {node["token"]}\n')
        cfg.chmod(0o600)
        daemon = launch([agent, '--config', str(cfg)], 'agent.log')
        state = lambda: request('GET', core_api)['core']
        wait_for('agent connection', lambda: state()['online'], 30)
        request('POST', core_api + '/install', expected=202)
        wait_for('installed version', lambda: state()['installed_version'] == version)
        wait_for('first four-protocol apply', lambda: state()['running'] and not state()['pending'] and state()['applied_revision'] > 0)
        first = state()
        assert first['applied_revision'] == 1, 'rapid initial writes should produce one revision'
        expected_ports = {str(i['listen_port']) + '/' + tr for i in inbounds
                          for tr in (['tcp'] if i['protocol'] == 'vless' else
                                     ['tcp', 'udp'] if i['protocol'] == 'shadowsocks' else ['udp'])}
        assert expected_ports <= set(first['listening'])
        evidence['checks']['install_four_protocols_apply_state'] = True
        evidence['first_revision'] = first['applied_revision']
        evidence['listening'] = sorted(expected_ports)
        evidence['firewall'] = first['firewall']

        # Three quick writes result in one changed rendered configuration.
        vless = inbounds[0]
        for patch in [{'remark': 'first'}, {'remark': 'second'}, {'enabled': False}]:
            request('PUT', f'/api/inbounds/{vless["id"]}', patch)
        wait_for('debounced revision', lambda: state()['applied_revision'] == 2 and not state()['pending'])
        assert len(request('GET', core_api + '/revisions')['revisions']) == 2
        evidence['checks']['rapid_writes_one_revision'] = True

        # Transfer real traffic through the SS2022 listener, with Agent polling/reset.
        httpd = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Payload)
        threading.Thread(target=httpd.serve_forever, daemon=True).start()
        ss, proxy_port = inbounds[1], port()
        client_config = {'inbounds': [{'type': 'mixed', 'listen': '127.0.0.1', 'listen_port': proxy_port}],
                         'outbounds': [{'type': 'shadowsocks', 'server': '127.0.0.1',
                                        'server_port': ss['listen_port'], 'method': '2022-blake3-aes-128-gcm',
                                        'password': ss['settings']['server_psk'] + ':' + user['ss_user_key']}]}
        client_file = work / 'client.json'
        client_file.write_text(json.dumps(client_config))
        client_file.chmod(0o600)
        launch([core, 'run', '-c', str(client_file)], 'client.log')

        def listening():
            try:
                with socket.create_connection(('127.0.0.1', proxy_port), timeout=0.1):
                    return True
            except OSError:
                return False

        wait_for('SS test client', listening)
        started = time.monotonic()
        result = subprocess.run(['curl', '--noproxy', '', '-fsS', '--max-time', '30', '-x',
                                 f'http://127.0.0.1:{proxy_port}', f'http://127.0.0.1:{httpd.server_port}/payload',
                                 '-o', '/dev/null'], capture_output=True, timeout=35)
        assert result.returncode == 0, 'SS2022 transfer failed'
        wait_for('50 MiB accounted in SQLite', lambda: request('GET', user_api)['subscriber']['traffic_used'] >= payload_size, 75)
        used = request('GET', user_api)['subscriber']['traffic_used']
        assert payload_size <= used < payload_size + 1024 * 1024
        evidence['traffic_used'] = used
        evidence['accounting_seconds'] = round(time.monotonic() - started, 3)
        evidence['checks']['real_50mib_user_accounting'] = True

        # An external process occupies a port: Agent must roll back, panel shows it.
        with socket.socket() as occupied:
            occupied.bind(('127.0.0.1', 0))
            occupied.listen()
            request('PUT', f'/api/inbounds/{vless["id"]}', {'enabled': True, 'listen_port': occupied.getsockname()[1]})
            wait_for('failed apply reported', lambda: state()['last_error'] and state()['pending'], 25)
            failed = state()
            assert failed['running'] and failed['applied_revision'] == 2
            request('POST', core_api + '/rollback/1')
            wait_for('rollback revision applied', lambda: not state()['pending'] and state()['config_sha256'] == first['config_sha256'])
        evidence['checks']['occupied_port_preserves_old_config_and_rollback'] = True
        logs = request('GET', core_api + '/logs?lines=20')
        assert logs['text'], 'expected failed-start log lines'
        evidence['checks']['real_log_request_response'] = True

        # Reconnect must apply offline edits; no forced core upgrade is involved.
        stop(daemon)
        wait_for('agent disconnected', lambda: not state()['online'])
        request('GET', core_api + '/logs', expected=409)
        applied_before = state()['applied_revision']
        request('PUT', f'/api/inbounds/{vless["id"]}', {'enabled': False})
        wait_for('offline desired revision', lambda: state()['desired_revision'] > applied_before)
        assert state()['applied_revision'] == applied_before
        daemon = launch([agent, '--config', str(cfg)], 'agent.log')
        wait_for('reconnect reconciliation', lambda: state()['online'] and not state()['pending'], 35)
        evidence['checks']['offline_edit_reconnect_apply'] = True
        before_disable = state()['applied_revision']
        request('PUT', user_api, {'enabled': False})
        wait_for('empty user ACL applied', lambda: state()['applied_revision'] > before_disable and not state()['pending'])
        denied = subprocess.run(['curl', '--noproxy', '', '-fsS', '--max-time', '5', '-x',
                                 f'http://127.0.0.1:{proxy_port}', f'http://127.0.0.1:{httpd.server_port}/payload',
                                 '-o', '/dev/null'], capture_output=True, timeout=10)
        assert denied.returncode != 0, 'empty SS user ACL accepted former credentials'
        before_enable = state()['applied_revision']
        request('PUT', user_api, {'enabled': True})
        wait_for('user restored', lambda: state()['applied_revision'] > before_enable and not state()['pending'])
        evidence['checks']['empty_ss_users_reject_former_credentials'] = True
        request('POST', core_api + '/restart', expected=202)
        wait_for('restart complete', lambda: state()['running'] and not state()['last_error'])
        if args.mihomo:
            from subscription_smoke import exercise
            exercise(args.mihomo, core, request, base, user, user_api, inbounds, state,
                     work, port, launch, stop, wait_for, httpd.server_port, evidence)
        if args.relay:
            from relay_smoke import exercise
            exercise(core, request, node_id, user, state, work, port, launch,
                     stop, wait_for, proxy_port, httpd.server_port, evidence)
        if args.agent_next:
            next_version = subprocess.check_output([args.agent_next, '--version'], text=True, timeout=5).strip()
            release_dir = work / 'data' / 'agent'
            release_dir.mkdir(exist_ok=True)
            shutil.copy2(args.agent_next, release_dir / 'vps-agent-linux-amd64')
            (release_dir / 'VERSION').write_text(next_version)
            before_hash = hashlib.sha256(agent_copy.read_bytes()).hexdigest()
            started = time.monotonic()
            request('POST', f'/api/servers/{node_id}/agent/update', expected=202)
            wait_for('agent self-update and reconnect', lambda: any(s['id'] == node_id and s['online'] and s['version'] == next_version for s in request('GET', '/api/agent-version')['servers']), 20)
            assert daemon.poll() is None, 'exec changed supervisor process lifecycle'
            assert hashlib.sha256(Path(agent + '.bak').read_bytes()).hexdigest() == before_hash
            assert hashlib.sha256(agent_copy.read_bytes()).hexdigest() == hashlib.sha256(Path(args.agent_next).read_bytes()).hexdigest()
            evidence['agent_update_seconds'] = round(time.monotonic()-started, 3)
            assert evidence['agent_update_seconds'] <= 10, 'agent update reconnect SLA exceeded'
            evidence['checks']['real_agent_update_hash_backup_same_pid_reconnect'] = True
            print('agent update smoke: exec/reconnect passed', flush=True)
        final_revision = state()['applied_revision']
        stop(panel)
        panel = launch([server], 'server.log', env)
        time.sleep(1)
        wait_for('panel restart reconnect', lambda: state()['online'] and not state()['pending'], 35)
        assert state()['applied_revision'] == final_revision
        evidence['checks']['panel_restart_retains_revision'] = True
        evidence['passed'] = True
    finally:
        for process in reversed(processes):
            stop(process)
        subprocess.run(['systemctl', 'stop', 'sing-box.service'], capture_output=True, timeout=30)
        subprocess.run(['systemctl', 'disable', 'sing-box.service'], capture_output=True, timeout=30)
        for path in reversed(owned):
            if path.is_dir():
                shutil.rmtree(path)
            else:
                path.unlink(missing_ok=True)
        subprocess.run(['systemctl', 'daemon-reload'], check=True, timeout=30)
        subprocess.run(['systemctl', 'reset-failed', 'sing-box.service'], capture_output=True, timeout=30)
        if httpd:
            httpd.shutdown()
        for handle in handles:
            handle.close()
        shutil.rmtree(work)
    Path(args.output).write_text(json.dumps(evidence, indent=2) + '\n')
    print(json.dumps(evidence, indent=2))


if __name__ == '__main__':
    main()
