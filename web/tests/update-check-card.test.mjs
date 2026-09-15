import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
import { createRequire } from 'node:module';

import ts from 'typescript';

const require = createRequire(import.meta.url);
const jsx = require('react/jsx-runtime');

function compile(path, loader) {
  const code = ts.transpileModule(fs.readFileSync(new URL(path, import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const module = { exports: {} };
  new Function('require', 'module', 'exports', code)(loader, module, module.exports);
  return module.exports;
}

function children(node) {
  return Array.isArray(node) ? node : node?.props?.children ? [node.props.children] : [];
}
function find(node, predicate) {
  if (predicate(node)) return node;
  for (const child of children(node)) {
    const found = find(child, predicate);
    if (found) return found;
  }
}
function text(node) {
  return typeof node === 'string' || typeof node === 'number' ? String(node) : children(node).map(text).join(' ');
}

// Run the real component's click handler and subsequent render with in-memory hooks
// and a controlled API boundary; no network or browser is required for this regression.
test('client request failure suppresses cached verdict until a successful retry', async () => {
  const states = [];
  let cursor = 0;
  let rejectRequest = true;
  let data = {
    state: 'ok', repository: 'owner/repo', checked_at: 1800000000,
    latest: { version: 'v1.0.0', url: 'https://github.com/owner/repo/releases/tag/v1.0.0' },
    panel_version: 'v1.0.0', agent_version: 'v1.0.0', panel_status: 'current', agent_status: 'current',
  };
  const status = compile('../src/components/update-check/status.ts', () => { throw new Error('Unexpected import'); });
  const { UpdateCheckCard } = compile('../src/components/update-check/update-check-card.tsx', (id) => {
    if (id === 'react/jsx-runtime') return jsx;
    if (id === 'react') return {
      useState(initial) {
        const index = cursor++;
        if (!(index in states)) states[index] = initial;
        return [states[index], (value) => { states[index] = value; }];
      },
    };
    if (id.startsWith('@mui/material/')) return { default: id.split('/').at(-1) };
    if (id === 'src/api/updates') return {
      useUpdates: () => ({ data, mutate: async (next) => { data = next; } }),
      checkUpdates: async () => {
        if (rejectRequest) throw new Error('Network unavailable');
        return { ...data, checked_at: data.checked_at + 600 };
      },
    };
    if (id === 'src/auth/utils') return { getErrorMessage: (err) => err.message };
    if (id === './status') return status;
    throw new Error(`Unexpected import: ${id}`);
  });
  const render = () => { cursor = 0; return UpdateCheckCard(); };
  let tree = render();
  assert.match(text(tree), /面板已是最新稳定版/);
  await find(tree, (node) => node?.type === 'Button').props.onClick();
  tree = render();
  assert.match(text(tree), /Network unavailable/);
  assert.match(text(tree), /最新稳定版（上次成功结果）/);
  assert.doesNotMatch(text(tree), /面板已是最新稳定版|探针已是最新稳定版/);

  rejectRequest = false;
  const retry = find(tree, (node) => node?.type === 'Button').props.onClick();
  assert.doesNotMatch(text(render()), /面板已是最新稳定版/);
  await retry;
  tree = render();
  assert.match(text(tree), /面板已是最新稳定版/);
  assert.doesNotMatch(text(tree), /Network unavailable|上次成功结果/);
});
