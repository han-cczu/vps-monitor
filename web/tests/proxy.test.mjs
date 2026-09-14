import test from 'node:test';
import assert from 'node:assert/strict';
import { PROTOCOLS, suggestPort, inboundDefaults, inboundSchema, inboundPayload, coreStatus, revisionApplied, countAssignments, plainLog } from '../src/sections/proxy/helpers.ts';

test('default ports respect shared transports and disabled reservations', () => {
  for (const [protocol, spec] of Object.entries(PROTOCOLS)) {
    assert.equal(suggestPort(protocol, []), spec.port);
    assert.equal(suggestPort(protocol, [{ protocol, listen_port: spec.port, enabled: false }]), spec.port + 1);
  }
  assert.equal(suggestPort('vless', [{ protocol: 'hysteria2', listen_port: 443 }]), 443);
  assert.equal(suggestPort('vless', [{ protocol: 'shadowsocks', listen_port: 443 }]), 444);
  assert.equal(suggestPort('hysteria2', [{ protocol: 'shadowsocks', listen_port: 8443 }]), 8444);
});

test('port exhaustion and wraparound never suggest reserved statistics TCP port', () => {
  const full = Array.from({ length: 65535 }, (_, index) => ({ protocol: 'shadowsocks', listen_port: index + 1 }));
  assert.equal(suggestPort('vless', full), undefined);
  assert.equal(suggestPort('vless', full.filter((x) => x.listen_port !== 10085)), undefined);
  assert.equal(suggestPort('vless', full.filter((x) => x.listen_port !== 42)), 42);
});

test('four forms validate defaults and serialize only protocol writable settings', () => {
  for (const protocol of Object.keys(PROTOCOLS)) {
    const parsed = inboundSchema.parse(inboundDefaults(protocol, []));
    const payload = inboundPayload(parsed);
    assert.equal(payload.protocol, protocol);
    for (const key of ['private_key', 'public_key', 'server_psk', 'obfs_password']) assert.equal(key in payload.settings, false);
  }
  assert.deepEqual(inboundPayload(inboundSchema.parse(inboundDefaults('shadowsocks', []))).settings, {});
  assert.equal('short_ids' in inboundPayload(inboundSchema.parse(inboundDefaults('vless', []))).settings, false);
});

test('editing preserves port, short IDs and values while omitting generated credentials', () => {
  const current = { protocol: 'vless', listen_port: 54321, enabled: false, remark: 'note', settings: { handshake_server: 'example.org', handshake_port: 8443, short_ids: ['ab', '1234'], private_key: 'private', public_key: 'public' } };
  const payload = inboundPayload(inboundSchema.parse(inboundDefaults('vless', [], current)));
  assert.equal(payload.listen_port, 54321);
  assert.equal(payload.enabled, false);
  assert.deepEqual(payload.settings, { handshake_server: 'example.org', handshake_port: 8443, short_ids: ['ab', '1234'] });
});

test('Short IDs reject odd length, uppercase, duplicates and overlong lists', () => {
  const defaults = inboundDefaults('vless', []);
  for (const short_ids of ['abc', 'AB', 'ab,ab', 'a', '1234567890abcdef00', Array.from({length: 17}, (_, i) => i.toString(16).padStart(2, '0')).join(',')]) assert.equal(inboundSchema.safeParse({ ...defaults, short_ids }).success, false, short_ids);
  assert.deepEqual(inboundSchema.parse({ ...defaults, short_ids: 'ab, 1234\nabcdef' }).short_ids, ['ab', '1234', 'abcdef']);
});

test('invalid ports and protocol fields are rejected before mutation', () => {
  const vless = inboundDefaults('vless', []);
  for (const listen_port of ['', 0, -1, 65536, 443.5, 10085, 'NaN']) assert.equal(inboundSchema.safeParse({ ...vless, listen_port }).success, false);
  for (const handshake_port of [0, 65536, 1.5]) assert.equal(inboundSchema.safeParse({ ...vless, handshake_port }).success, false);
  assert.equal(inboundSchema.safeParse({ ...vless, handshake_server: 'https://example.com' }).success, false);
  assert.equal(inboundSchema.safeParse({ ...vless, remark: 'x'.repeat(65) }).success, false);
  for (const up_mbps of [-1, 0.5, 1_000_001]) assert.equal(inboundSchema.safeParse({ ...inboundDefaults('hysteria2', []), up_mbps }).success, false);
  assert.equal(inboundSchema.safeParse({ ...inboundDefaults('tuic', []), congestion_control: 'invalid' }).success, false);
  assert.equal(inboundSchema.safeParse({ ...inboundDefaults('tuic', []), listen_port: 10085 }).success, true);
});

test('four core labels prioritize installation and pending reconciliation', () => {
  assert.equal(coreStatus({ installed_version: null, pending: true, running: false }).label, '未安装');
  assert.equal(coreStatus({ installed_version: 'v1', pending: true, running: true }).label, '待应用');
  assert.equal(coreStatus({ installed_version: 'v1', pending: false, running: true }).label, '运行中');
  assert.equal(coreStatus({ installed_version: 'v1', pending: false, running: false }).label, '已停止');
});

test('stale pending=false cannot complete a newer apply; hash/version/offline also matter', () => {
  const target = { revision: 4, sha256: 'new', version: 'v2' };
  const applied = { online: true, running: true, pending: false, applied_revision: 4, config_sha256: 'new', installed_version: 'v2' };
  assert.equal(revisionApplied(applied, target), true);
  for (const patch of [{ applied_revision: 3 }, { config_sha256: 'old' }, { installed_version: 'v1' }, { online: false }, { running: false }, { pending: true }]) assert.equal(revisionApplied({ ...applied, ...patch }, target), false);
});

test('assigned users are deduplicated per node and count only matching inbound', () => {
  const subscribers = [{ id: 1, assigned_inbounds: [{ server_id: 1, inbound_id: 1 }, { server_id: 1, inbound_id: 2 }] }, { id: 2, assigned_inbounds: [{ server_id: 2, inbound_id: 3 }] }, { id: 3, assigned_inbounds: [{ server_id: 1, inbound_id: 1 }] }];
  assert.equal(countAssignments(subscribers, 1), 2);
  assert.equal(countAssignments(subscribers, 1, 2), 1);
  assert.equal(countAssignments(subscribers, 3), 0);
});

test('log display and clipboard text strip ANSI colors without changing content', () => {
  assert.equal(plainLog('\x1b[31mERROR\x1b[0m failed\n\x1b[1;33mWARN\x1b[m <tag>'), 'ERROR failed\nWARN <tag>');
});
