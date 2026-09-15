import assert from 'node:assert/strict';
import test from 'node:test';

import { latestLabel, latestValue, updateSummary } from '../src/components/update-check/status.ts';

const base = {
  state: 'ok',
  latest: { version: 'v0.9.9', url: 'https://github.com/owner/repo/releases/tag/v0.9.9', published_at: '' },
  panel_version: 'v0.2.2',
  panel_status: 'available',
  agent_version: 'v0.2.0',
  agent_status: 'available',
};

test('every result state is distinguishable and only a successful check gives a verdict', () => {
  assert.equal(updateSummary({ ...base, state: 'unchecked' }), null);
  assert.equal(updateSummary({ ...base, state: 'error' }), null);
  // 尚无稳定发布：检查成功但没有可比较的版本。
  assert.equal(updateSummary({ ...base, latest: null, panel_status: 'unknown', agent_status: 'unknown' }), null);

  assert.match(updateSummary(base).panel, /面板有新版本 v0\.9\.9/);
  assert.match(updateSummary({ ...base, panel_status: 'current' }).panel, /已是最新稳定版/);
  assert.match(updateSummary({ ...base, panel_status: 'ahead' }).panel, /高于更新源的公开稳定版/);
  assert.match(
    updateSummary({ ...base, panel_version: 'dev', panel_status: 'unknown' }).panel,
    /无法与最新稳定版自动比较/
  );
});

test('a failed check never reads as up to date and keeps the last known version', () => {
  for (const state of ['error', 'unchecked']) {
    for (const latest of [base.latest, null]) {
      const summary = updateSummary({ ...base, state, latest });
      assert.equal(summary, null);
    }
  }
  assert.equal(latestValue('error', ''), '未获取到');
  assert.equal(latestValue('ok', ''), '尚未发布');
  assert.equal(latestValue('unchecked', ''), '待检查');
  assert.equal(latestLabel('error', true), '最新稳定版（上次成功结果）');
  assert.equal(latestLabel('error', false), '最新稳定版');
  assert.equal(latestLabel('ok', true), '最新稳定版');
});

test('a newer probe release that the panel does not carry tells the operator to update the panel first', () => {
  const summary = updateSummary({ ...base, agent_status: 'available' });
  assert.equal(summary.severity, 'warning');
  assert.match(summary.agent, /先部署携带新探针的面板安装包/);
  assert.match(summary.agent, /v0\.2\.0/);
  assert.doesNotMatch(summary.agent, /已是最新/);

  const carried = updateSummary({ ...base, agent_status: 'current' });
  assert.equal(carried.severity, 'warning'); // 面板本身仍有更新
  assert.match(carried.agent, /已是最新稳定版/);

  const noAgent = updateSummary({ ...base, agent_version: '', agent_status: 'available' });
  assert.match(noAgent.agent, /还没有携带任何探针安装包/);
  const unknownAgent = updateSummary({ ...base, agent_version: 'v0.2.0-observe21', agent_status: 'unknown' });
  assert.match(unknownAgent.agent, /v0\.2\.0-observe21 无法自动比较/);
});

test('severity is only a warning when something actually has an update', () => {
  const all = (panel, agent) => updateSummary({ ...base, panel_status: panel, agent_status: agent }).severity;
  assert.equal(all('available', 'current'), 'warning');
  assert.equal(all('current', 'available'), 'warning');
  assert.equal(all('current', 'current'), 'info');
  assert.equal(all('ahead', 'unknown'), 'info');
});
