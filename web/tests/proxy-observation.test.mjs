import assert from 'node:assert/strict';
import test from 'node:test';
import {
  canEditManagedProxy,
  observedBytes,
  scanNotice,
  snapshotNotice,
  splitObservations,
  normalizeInstance,
  instanceStatus,
  instanceFromLatestScan,
} from '../src/sections/proxy/observation-helpers.ts';

test('unknown, external and offline instances never expose managed editing', () => {
  assert.equal(canEditManagedProxy(), false);
  for (const management of ['unknown', 'external'])
    assert.equal(canEditManagedProxy({ online: true, management }), false);
  assert.equal(canEditManagedProxy({ online: false, management: 'managed' }), false);
  for (const management of ['managed', 'none'])
    assert.equal(canEditManagedProxy({ online: true, management }), true);
});
test('missing external counters stay unavailable instead of zero', () => {
  for (const n of [null, undefined, -1, NaN]) assert.equal(observedBytes(n), '不可用');
  assert.equal(observedBytes(0), '0 B');
  assert.equal(observedBytes(500_000_000_000), '500 GB');
  assert.equal(observedBytes(1_000_000_000_000), '1 TB');
});

const instance = (id, extra = {}) => ({
  id,
  core: 'sing-box',
  running: true,
  stale: false,
  absent: false,
  issues: [],
  config_read_at: 1,
  received_at: 100,
  ...extra,
});

test('records without array fields render instead of crashing the page', () => {
  // 升级前留下的记录可能只有核心与版本：缺数组字段时整页不能白屏。
  const legacy = normalizeInstance({
    id: 'legacy',
    core: 'sing-box',
    version: null,
    config_paths: null,
    ports: null,
    inbounds: null,
    issues: null,
  });
  assert.deepEqual(legacy.config_paths, []);
  assert.deepEqual(legacy.ports, []);
  assert.deepEqual(legacy.inbounds, []);
  assert.deepEqual(legacy.issues, []);
  assert.equal(legacy.version, '');
  assert.equal(normalizeInstance({ id: 'a', version: 'v1.0.0', ports: [] }).version, 'v1.0.0');

  const { current, history } = splitObservations([{ id: 'gone', absent: true, inbounds: null }]);
  assert.deepEqual(current, []);
  assert.equal(history.length, 1);
  assert.deepEqual(history[0].inbounds, []);
});

test('only records the server marked absent leave the current list', () => {
  const { current, history } = splitObservations([
    instance('a'),
    instance('b', { absent: true }),
    instance('c', { running: false }),
    instance('d', { stale: true }),
    instance('e', { running: false, stale: true }),
  ]);
  assert.deepEqual(
    current.map((i) => i.id),
    ['a', 'c', 'd', 'e']
  );
  assert.deepEqual(
    history.map((i) => i.id),
    ['b']
  );
});

test('a stopped process reads as not running, never as uninstalled', () => {
  assert.deepEqual(instanceStatus(instance('a', { running: false })), {
    label: '未运行',
    color: 'default',
  });
  assert.deepEqual(instanceStatus(instance('a')), { label: '运行中', color: 'success' });
  assert.deepEqual(instanceStatus(instance('a', { stale: true })), {
    label: '待核验快照',
    color: 'warning',
  });
});

const scan = (extra = {}) => ({
  complete: true,
  collected_at: 1,
  received_at: 100,
  instances: 0,
  stale: false,
  ...extra,
});
const context = (extra = {}) => ({ online: true, scan: scan(extra) });

test('scan state separates offline, waiting, partial, expired and a genuine empty result', () => {
  // 实际离线组合：Online=false 时能力协商也不会成功。
  const offline = scanNotice({
    scan: scan(),
    online: false,
    supported: false,
    hasCurrent: false,
    hasHistory: true,
  });
  assert.equal(offline.severity, 'info');
  assert.match(offline.text, /探针离线，无法确认当前实例/);
  assert.doesNotMatch(offline.text, /当前未发现代理实例|能力协商/);

  const capable = scanNotice({
    scan: scan(),
    online: false,
    supported: true,
    hasCurrent: false,
    hasHistory: false,
  });
  assert.match(capable.text, /探针离线，无法确认当前实例/);

  const uncapable = scanNotice({
    scan: null,
    online: true,
    supported: false,
    hasCurrent: false,
    hasHistory: false,
  });
  assert.equal(uncapable.severity, 'warning');
  assert.match(uncapable.text, /尚未完成观测能力协商/);
  assert.doesNotMatch(uncapable.text, /等待首个完整观测快照/);

  const waiting = scanNotice({
    scan: null,
    online: true,
    supported: true,
    hasCurrent: false,
    hasHistory: false,
  });
  assert.match(waiting.text, /等待首个完整观测快照/);
  assert.doesNotMatch(waiting.text, /当前未发现代理实例/);

  const partial = scanNotice({
    scan: scan({ complete: false, instances: 3 }),
    online: true,
    supported: true,
    hasCurrent: true,
    hasHistory: false,
  });
  assert.equal(partial.severity, 'warning');
  assert.match(partial.text, /采集不完整/);
  assert.match(partial.text, /不能算作本轮成功/);
  assert.doesNotMatch(partial.text, /当前未发现代理实例/);

  // 结果过期的空扫描：只能说上次没发现，不能说现在没发现。
  const expiredEmpty = scanNotice({
    scan: scan({ stale: true }),
    online: true,
    supported: true,
    hasCurrent: false,
    hasHistory: true,
  });
  assert.equal(expiredEmpty.severity, 'warning');
  assert.match(expiredEmpty.text, /超过 90 秒未更新/);
  assert.match(expiredEmpty.text, /上一次完整扫描没有发现代理实例/);
  assert.doesNotMatch(expiredEmpty.text, /当前未发现代理实例/);

  const expiredKept = scanNotice({
    scan: scan({ stale: true, instances: 2 }),
    online: true,
    supported: true,
    hasCurrent: true,
    hasHistory: false,
  });
  assert.match(expiredKept.text, /超过 90 秒未更新/);
  assert.match(expiredKept.text, /保留最后一次完整扫描的结果/);

  // 只有在线、有效、未过期的完整空扫描才能给出"当前未发现"。
  const empty = scanNotice({
    scan: scan(),
    online: true,
    supported: true,
    hasCurrent: false,
    hasHistory: true,
  });
  assert.equal(empty.severity, 'info');
  assert.match(empty.text, /当前未发现代理实例/);
  assert.match(empty.text, /历史观测记录/);

  const healthy = scanNotice({
    scan: scan({ instances: 1 }),
    online: true,
    supported: true,
    hasCurrent: true,
    hasHistory: false,
  });
  assert.equal(healthy, null);
});

test('a stale or offline card never presents last-known values as current', () => {
  // 整体过期：不能声称 PID、内存、端口是本次读取的。
  const expired = snapshotNotice(instance('a', { stale: true }), context({ stale: true }));
  assert.equal(expired.severity, 'warning');
  assert.match(expired.text, /不是节点现在的状态/);
  assert.match(expired.text, /PID、内存、端口绑定/);
  assert.doesNotMatch(expired.text, /本次读取/);

  const offline = snapshotNotice(instance('a'), { online: false, scan: scan() });
  assert.match(offline.text, /不是节点现在的状态/);
  assert.doesNotMatch(offline.text, /本次读取/);

  // 在线、完整、未过期、本轮读到配置：没有需要注意的地方。
  assert.equal(snapshotNotice(instance('a'), context()), null);

  // 配置读取失败：进程信息可能来自本轮，配置摘要一定来自上次成功读取；
  // 用量每轮单独采集，读不到会被清空，不能说成"沿用上次成功读取"。
  for (const issues of [
    ['config_invalid_jsonc'],
    ['config_path_unknown'],
    ['config_directory_unreadable'],
    ['source_read_failed'],
    ['config_files_limit'],
  ]) {
    const failed = snapshotNotice(instance('a', { stale: true, issues }), context());
    assert.match(failed.text, /PID、内存和端口绑定是本次读取的结果/);
    assert.match(failed.text, /配置摘要与入站仍是/);
    assert.match(failed.text, /用量按本次实际采集到的结果展示/);
    assert.doesNotMatch(failed.text, /用量仍是上一次成功读取的结果/);
  }

  // 参与读取的进程已经变了：连进程字段都只能算待核验。
  const changed = snapshotNotice(
    instance('a', { stale: true, issues: ['process_changed_during_read'] }),
    context()
  );
  assert.match(changed.text, /读取期间进程发生变化/);
  assert.match(changed.text, /待核验快照/);
  assert.doesNotMatch(changed.text, /是本次读取的结果/);

  // 没有进程可读（已停止）且配置没读到：不能把上次的绑定说成当前状态。
  const stopped = snapshotNotice(
    instance('a', { stale: true, running: false, issues: ['config_invalid_jsonc'] }),
    context()
  );
  assert.match(stopped.text, /进程没有在运行/);
  assert.match(stopped.text, /配置摘要与入站仍是/);

  // 有 stale 但没有已知原因：保守说明这一份只是最后读到的结果。
  const unknown = snapshotNotice(instance('a', { stale: true }), context());
  assert.match(unknown.text, /尚未完成核验/);
  assert.doesNotMatch(unknown.text, /本次读取/);
});

test('an instance missing from the latest scan is judged on its own timestamp', () => {
  // 节点一直在上报（scan 不过期），但这一条没出现在最近那一轮里：
  // 它的 received_at 停在 90 秒以前，节点级状态却是新的。
  const node = scan({ received_at: 1000, instances: 1 });
  const old = instance('a', {
    received_at: 890,
    stale: true,
    issues: ['config_invalid_jsonc'],
  });
  assert.equal(instanceFromLatestScan(old, node), false);
  assert.equal(instanceFromLatestScan(instance('b', { received_at: 1000 }), node), true);

  const notice = snapshotNotice(old, { online: true, scan: node });
  assert.equal(notice.severity, 'warning');
  assert.match(notice.text, /不是节点最近一次采集的结果/);
  assert.doesNotMatch(notice.text, /本次读取/);
  // 到期的是这一条自己：摘要来自它最后一次成功读取的时刻。
  assert.match(notice.text, /配置摘要与入站来自/);
  // 用量单独采集，不能一概断言沿用旧值。
  assert.match(notice.text, /用量按那次上报的结果展示/);

  // 同一轮里出现过的实例不受影响。
  assert.equal(
    snapshotNotice(instance('b', { received_at: 1000 }), { online: true, scan: node }),
    null
  );

  // 没有扫描状态时不能凭空说某条是新的。
  assert.equal(instanceFromLatestScan(instance('a'), null), false);
  assert.match(snapshotNotice(instance('a'), { online: true, scan: null }).text, /不是节点最近一次采集的结果/);
  // 离线时沿用整份快照过期的说法。
  assert.match(
    snapshotNotice(instance('a'), { online: false, scan: null }).text,
    /不是节点现在的状态/
  );
});

test('status labels say when a single instance is no longer confirmed', () => {
  const fresh = { online: true, fromLatest: true };
  assert.deepEqual(instanceStatus(instance('a'), fresh), { label: '运行中', color: 'success' });
  assert.deepEqual(instanceStatus(instance('a', { running: false }), fresh), {
    label: '未运行',
    color: 'default',
  });
  assert.deepEqual(instanceStatus(instance('a', { stale: true }), fresh), {
    label: '待核验快照',
    color: 'warning',
  });
  // 节点还在上报，但这一条已经掉出最近一轮：不能再说"运行中"。
  assert.deepEqual(instanceStatus(instance('a'), { online: true, fromLatest: false }), {
    label: '运行中 · 未确认',
    color: 'default',
  });
  assert.deepEqual(
    instanceStatus(instance('a', { running: false }), { online: true, fromLatest: false }),
    { label: '未运行 · 未确认', color: 'default' }
  );
  assert.deepEqual(
    instanceStatus(instance('a', { stale: true }), { online: true, fromLatest: false }),
    { label: '运行中 · 未确认', color: 'default' }
  );
  // 离线时同理。
  assert.deepEqual(instanceStatus(instance('a'), { online: false, fromLatest: true }), {
    label: '运行中 · 未确认',
    color: 'default',
  });
});
