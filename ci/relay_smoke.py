"""Optional two-hop relay smoke; only reconcile-smoke.py owns managed services.

A is the existing Agent-managed core and client. B is an offline panel node
whose generated revision runs as an extra child process, without an Agent or
systemd unit. No supplied binaries or production configuration are modified.
"""
import hashlib
import json
import socket
import subprocess
import sys


def isolated_config(revision, inbound_id, relay_user):
    """Retain the generated protocol and ACL; isolate only bind/stats settings."""
    config = json.loads(json.dumps(revision['config_json']))
    assert config['experimental']['v2ray_api']['listen'] == '127.0.0.1:10085'
    del config['experimental']  # A owns this fixed statistics port on the host.
    inbounds = config['inbounds']
    assert len(inbounds) == 1 and inbounds[0]['type'] == 'shadowsocks'
    inbound = inbounds[0]
    assert [a['inbound_id'] for a in relay_user['assigned_inbounds']] == [inbound_id]
    assert inbound['users'] == [{'name': f'sub-{relay_user["id"]}',
                                 'password': relay_user['ss_user_key']}]
    inbound['listen'] = '127.0.0.1'
    assert config['route']['final'] == 'direct'
    return config


def exercise(core, request, source_id, user, state, work, port, launch,
             stop, wait_for, proxy_port, payload_port, evidence):
    print('relay smoke: preparing API-generated A -> B credentials/config', flush=True)
    source_api = f'/api/servers/{source_id}'
    relay_api = source_api + '/advanced/relay'
    relay_active, target_process = False, None
    target_file = work / 'relay-target.json'
    expected_payload = b'x' * (64 * 1024)

    def wait_applied(label, before):
        def applied():
            current = state()
            return current if (current['running'] and not current['pending']
                               and current['applied_revision'] > before) else None
        return wait_for(label, applied, 35)

    def transfer():
        # A fresh curl process opens a fresh proxy connection for every probe.
        # Keep the small body in memory so HTTP 200 alone cannot pass the test.
        return subprocess.run(['curl', '--noproxy', '', '-fsS', '--max-time', '8',
                               '-x', f'http://127.0.0.1:{proxy_port}',
                               f'http://127.0.0.1:{payload_port}/small'],
                              capture_output=True, timeout=13)

    def expect_payload(label):
        result = transfer()
        assert result.returncode == 0 and result.stdout == expected_payload, label

    # SS2022 listens on both transports; an unused TCP port alone is insufficient.
    def target_port():
        for _ in range(20):
            candidate = port()
            if candidate == 10085:
                continue
            try:
                with socket.socket() as tcp, socket.socket(type=socket.SOCK_DGRAM) as udp:
                    tcp.bind(('127.0.0.1', candidate))
                    udp.bind(('127.0.0.1', candidate))
                    return candidate
            except OSError:
                pass
        raise AssertionError('no free loopback TCP/UDP port for relay B')

    def ready():
        assert target_process.poll() is None, 'isolated relay target exited'
        try:
            with socket.create_connection(('127.0.0.1', listen), timeout=0.2):
                return True
        except OSError:
            return False

    def revision():
        # Offline nodes can still render/check/persist a desired revision. B
        # deliberately has no Agent, so do not wait for its applied_revision.
        number = request('POST', target_api + '/core/apply')['revision']['revision']
        return request('GET', target_api + f'/core/revisions/{number}')['revision']

    try:
        expect_payload('direct A baseline failed before adding relay')
        target = request('POST', '/api/servers', {
            'name': 'isolated-relay-target', 'currency': 'USD', 'billing_cycle': 'month',
            'traffic_mode': 'max', 'public_host': '127.0.0.1'}, expected=201)['server']
        target_api = f'/api/servers/{target["id"]}'
        listen = target_port()
        inbound = request('POST', target_api + '/inbounds', {
            'protocol': 'shadowsocks', 'listen_port': listen}, expected=201)['inbound']
        before = state()['applied_revision']
        result = request('POST', relay_api, {
            'target_server_id': target['id'], 'target_inbound_id': inbound['id']})
        relay_active = True
        relay_id = result['relay_subscriber_id']
        relay_user = request('GET', f'/api/subscribers/{relay_id}')['subscriber']
        assert relay_id != user['id'] and relay_user['kind'] == 'relay'
        assert relay_user['ss_user_key'] != user['ss_user_key'], 'relay reused end-user credentials'
        assert relay_id not in {s['id'] for s in request('GET', '/api/subscribers')['subscribers']}
        extra = result['advanced']['extra_json']
        tag = extra['route']['final']
        outbound = next(o for o in extra['outbounds'] if o['tag'] == tag)
        assert outbound['type'] == 'shadowsocks'
        assert outbound['server'] == '127.0.0.1' and outbound['server_port'] == listen
        assert outbound['password'] == inbound['settings']['server_psk'] + ':' + relay_user['ss_user_key']

        target_revision = revision()
        config = isolated_config(target_revision, inbound['id'], relay_user)
        target_file.write_text(json.dumps(config))
        target_file.chmod(0o600)
        check = subprocess.run([core, 'check', '-c', str(target_file)], capture_output=True, timeout=20)
        assert check.returncode == 0, 'core rejected isolated B generated revision'
        target_process = launch([core, 'run', '-c', str(target_file)], 'relay-target.log')
        wait_for('relay B listener', ready, 15)
        applied = wait_applied('relay source A apply', before)
        source_revision = request('GET', source_api + f'/core/revisions/{applied["applied_revision"]}')['revision']
        assert source_revision['config_json']['route']['final'] == tag
        expect_payload('A -> B -> local payload transfer failed')

        # All processes share a host, so a successful local payload alone would
        # not prove two hops. Removing B must break this exact A client path.
        stop(target_process)
        assert transfer().returncode != 0, 'A bypassed stopped B despite relay being active'
        target_process = launch([core, 'run', '-c', str(target_file)], 'relay-target.log')
        wait_for('relay B restart', ready, 15)
        expect_payload('A -> B did not recover after B restart')
        evidence['checks']['relay_generated_credentials_two_hop_payload'] = True
        evidence['checks']['relay_stopped_b_denied_restart_restored'] = True

        before = state()['applied_revision']
        request('DELETE', relay_api)
        relay_active = False
        restored = wait_applied('relay removal restores A direct', before)
        restored_revision = request('GET', source_api + f'/core/revisions/{restored["applied_revision"]}')['revision']
        assert restored_revision['config_json']['route']['final'] == 'direct'
        assert tag not in {o['tag'] for o in restored_revision['config_json']['outbounds']}
        historical = request('GET', f'/api/subscribers/{relay_id}')['subscriber']
        assert historical['kind'] == 'relay' and not historical['assigned_inbounds']
        removed_target_revision = revision()
        assert removed_target_revision['revision'] > target_revision['revision']
        removed_inbound = removed_target_revision['config_json']['inbounds'][0]
        assert removed_inbound['users'] == [] and removed_inbound['managed'] is True
        stop(target_process)
        expect_payload('A direct did not work after removing relay with B stopped')
        evidence['checks']['relay_removal_unassigns_target_restores_direct'] = True
        evidence['relay'] = {
            'target_protocol': 'shadowsocks-2022', 'target_agent': False,
            'target_config_adjustments': ['remove experimental', 'bind inbound to 127.0.0.1'],
            'source_relay_revision': applied['applied_revision'],
            'source_direct_revision': restored['applied_revision'],
            'target_revision': target_revision['revision'],
            'target_unassigned_revision': removed_target_revision['revision'],
            'payload_bytes': len(expected_payload),
            'payload_sha256': hashlib.sha256(expected_payload).hexdigest(),
            'target_accounting_verified': False,
        }
        print('relay smoke: two-hop, stopped-B negative and direct restoration passed', flush=True)
    finally:
        # The outer harness also tracks every launched child. Limit this module's
        # cleanup to B and the temporary relay; never touch systemd or core paths.
        failing = sys.exc_info()[0] is not None
        try:
            if target_process is not None:
                stop(target_process)
            if relay_active:
                before = state()['applied_revision']
                request('DELETE', relay_api)
                wait_applied('failed relay smoke cleanup', before)
        except Exception:
            if not failing:
                raise
            print('relay smoke cleanup incomplete; outer harness will stop its temporary processes/services',
                  file=sys.stderr, flush=True)
