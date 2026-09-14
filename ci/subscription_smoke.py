"""Optional real-client assertions used by reconcile-smoke.py (all data temporary)."""
import datetime
import json
import re
from pathlib import Path
import secrets
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from zoneinfo import ZoneInfo


def exercise(mihomo, request, base, user, user_api, inbounds, state,
             work, port, launch, wait_for, payload_port, evidence):
    print('subscription smoke: importing real four-protocol provider', flush=True)
    # The legacy smoke leaves VLESS disabled; enable it for the complete export.
    def wait_applied(label, before, timeout=20):
        return wait_for(label, lambda: (lambda s: s['applied_revision'] > before and not s['pending'])(state()), timeout)

    before = state()['applied_revision']
    request('PUT', f'/api/inbounds/{inbounds[0]["id"]}', {'enabled': True})
    wait_applied('four protocol re-enable', before)
    proxy_port, control_port = port(), port()
    secret = secrets.token_hex(24)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def subscription(token, fmt='clash-provider', expected=200):
        try:
            with opener.open(base + '/sub/' + token + '?format=' + fmt, timeout=10) as response:
                assert response.status == expected, 'unexpected subscription response status'
                return response.read(), dict(response.headers)
        except urllib.error.HTTPError as exc:
            assert exc.code == expected, 'unexpected subscription response status'
            return exc.read(), dict(exc.headers)

    provider, headers = subscription(user['sub_token'])
    assert b'skip-cert-verify: false' in provider
    provider_file = work / 'provider.yaml'
    provider_file.write_bytes(provider)
    provider_file.chmod(0o600)
    config = {
        'mixed-port': proxy_port, 'bind-address': '127.0.0.1', 'allow-lan': False,
        'external-controller': f'127.0.0.1:{control_port}', 'secret': secret,
        'mode': 'rule', 'log-level': 'warning', 'ipv6': False,
        'proxy-providers': {'qa': {'type': 'file', 'path': str(provider_file)}},
        'proxy-groups': [{'name': 'QA', 'type': 'select', 'use': ['qa']}],
        'rules': ['MATCH,QA'],
    }
    config_file = work / 'mihomo.json'
    config_file.write_text(json.dumps(config))
    config_file.chmod(0o600)
    check = subprocess.run([mihomo, '-t', '-d', str(work), '-f', str(config_file)], capture_output=True, timeout=20)
    assert check.returncode == 0, 'Mihomo rejected exported provider'
    client = launch([mihomo, '-d', str(work), '-f', str(config_file)], 'mihomo.log')

    def control(path, body=None, controller=control_port):
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(f'http://127.0.0.1:{controller}' + path, data=data,
                                     headers={'Authorization': 'Bearer ' + secret, 'Content-Type': 'application/json'},
                                     method='PUT' if body is not None else 'GET')
        try:
            with opener.open(req, timeout=5) as response:
                content = response.read()
                return json.loads(content) if content else True
        except urllib.error.URLError:
            return None

    available = wait_for('Mihomo provider', lambda: control('/providers/proxies/qa'), 20)['proxies']
    assert len(available) == 4, 'provider missing protocol'

    def select(name):
        assert control('/proxies/QA', {'name': name}), 'Mihomo selection failed'

    def transfer(path='/small', timeout=20, listener=proxy_port):
        return subprocess.run(['curl', '--noproxy', '', '-fsS', '--max-time', str(timeout),
                               '-x', f'http://127.0.0.1:{listener}',
                               f'http://127.0.0.1:{payload_port}' + path, '-o', '/dev/null'],
                              capture_output=True, timeout=timeout+5).returncode

    for proxy in available:
        select(proxy['name'])
        result = transfer()
        assert result == 0, 'real Mihomo protocol connection failed: ' + proxy['type']
        evidence['checks']['mihomo_' + proxy['type'].lower() + '_transfer'] = True
    evidence['mihomo_version'] = subprocess.check_output([mihomo, '-v'], text=True, timeout=5).strip()

    # A separate client has no cached QUIC sessions; wrong certificate pins must
    # fail even though these loopback servers use self-signed certificates.
    bad_provider, replacements = re.subn(rb'(?m)^(\s+fingerprint: )"[a-fA-F0-9:]+"', lambda m: m[1] + b'"' + b'0'*64 + b'"', provider)
    assert replacements == 2, 'expected HY2 and TUIC certificate pins'
    bad_dir = work / 'wrong-pin'
    bad_dir.mkdir(mode=0o700)
    bad_provider_file = bad_dir / 'provider.yaml'
    bad_provider_file.write_bytes(bad_provider)
    bad_provider_file.chmod(0o600)
    bad_proxy, bad_control = port(), port()
    bad_config = json.loads(json.dumps(config))
    bad_config['mixed-port'] = bad_proxy
    bad_config['external-controller'] = f'127.0.0.1:{bad_control}'
    bad_config['proxy-providers']['qa']['path'] = str(bad_provider_file)
    bad_config_file = bad_dir / 'config.json'
    bad_config_file.write_text(json.dumps(bad_config))
    bad_config_file.chmod(0o600)
    bad_client = launch([mihomo, '-d', str(bad_dir), '-f', str(bad_config_file)], 'wrong-pin.log')
    wait_for('wrong pin client', lambda: control('/providers/proxies/qa', controller=bad_control), 15)
    for proxy in available:
        if proxy['type'].lower() not in ['hysteria2', 'tuic']:
            continue
        assert control('/proxies/QA', {'name': proxy['name']}, controller=bad_control)
        assert transfer(timeout=5, listener=bad_proxy) != 0, 'wrong certificate pin accepted: ' + proxy['type']
        evidence['checks']['mihomo_' + proxy['type'].lower() + '_wrong_pin_rejected'] = True
    bad_client.terminate()
    bad_client.wait(timeout=10)

    ss = next(p for p in available if p['type'].lower() == 'shadowsocks')
    select(ss['name'])
    request('POST', user_api + '/reset-usage')
    request('PUT', user_api, {'traffic_limit': 100*1024*1024})
    started = time.monotonic()
    before = state()['applied_revision']
    for _ in range(3):
        assert transfer('/payload', 30) == 0, '150 MiB quota setup transfer failed'
    wait_for('quota enforcement', lambda: request('GET', user_api)['subscriber']['status'] == 'quota', 110)
    wait_applied('quota removal applied', before)
    assert transfer(timeout=5) != 0, 'quota-disabled user still connects'
    body, headers = subscription(user['sub_token'])
    assert b'proxies: []' in body and 'subscription-userinfo' in {k.lower() for k in headers}
    evidence['quota_seconds'] = round(time.monotonic()-started, 3)
    assert evidence['quota_seconds'] <= 120, 'quota SLA exceeded'
    evidence['checks']['mihomo_150mib_quota_denied_empty_subscription'] = True
    started = time.monotonic()
    before = state()['applied_revision']
    request('PUT', user_api, {'traffic_limit': 1024*1024*1024})
    wait_applied('raised quota applied', before, 15)
    assert transfer() == 0, 'raised quota did not restore connection'
    evidence['quota_restore_seconds'] = round(time.monotonic()-started, 3)
    assert evidence['quota_restore_seconds'] <= 10, 'quota restore SLA exceeded'
    evidence['checks']['mihomo_quota_raise_restores'] = True
    print('subscription smoke: quota stop/restore passed', flush=True)

    today = datetime.datetime.now(ZoneInfo('Asia/Shanghai')).date()
    before = state()['applied_revision']
    request('PUT', user_api, {'expire_at': str(today-datetime.timedelta(days=1))})
    wait_applied('expiry removal applied', before)
    assert transfer(timeout=5) != 0, 'expired user still connects'
    before = state()['applied_revision']
    request('PUT', user_api, {'expire_at': str(today+datetime.timedelta(days=1))})
    wait_applied('expiry extension applied', before)
    assert transfer() == 0, 'expiry extension did not restore connection'
    evidence['checks']['mihomo_expiry_stop_extend_restore'] = True
    old_token = user['sub_token']
    new_user = request('POST', user_api + '/reset-token')['subscriber']
    subscription(old_token, expected=404)
    body, _ = subscription(new_user['sub_token'])
    assert b'proxies:' in body and transfer() == 0
    evidence['checks']['subscription_token_rotation_keeps_proxy_credentials'] = True
    # A single client config can also be imported directly; only add local listening.
    full, _ = subscription(new_user['sub_token'], 'clash')
    full_file = work / 'full-subscription.yaml'
    full_file.write_bytes(full)
    full_file.chmod(0o600)
    check = subprocess.run([mihomo, '-t', '-d', str(work), '-f', str(full_file)], capture_output=True, timeout=20)
    assert check.returncode == 0, 'Mihomo rejected full Clash subscription'
    evidence['checks']['mihomo_full_subscription_check'] = True
