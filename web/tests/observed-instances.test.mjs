import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
import { createRequire } from 'node:module';

import ts from 'typescript';

const require = createRequire(import.meta.url);
const jsx = require('react/jsx-runtime');

function transpile(path, options = {}) {
  return ts.transpileModule(fs.readFileSync(new URL(path, import.meta.url), 'utf8'), {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      jsx: ts.JsxEmit.ReactJSX,
      target: ts.ScriptTarget.ES2022,
      ...options,
    },
  }).outputText;
}
function load(code, loader) {
  const module = { exports: {} };
  new Function('require', 'module', 'exports', code)(loader, module, module.exports);
  return module.exports;
}
const helpers = load(transpile('../src/sections/proxy/observation-helpers.ts'), () => {
  throw new Error('Unexpected helper import');
});

function typeName(node) {
  return typeof node?.type === 'string' ? node.type : (node?.type?.name ?? '');
}
// 组件元素在测试里不会自动展开：内部组件按名字识别后手动渲染一层或多层，
// 这样断言看到的就是页面上真实显示的内容。
const inlined = new Set([
  'StatsNote',
  'InstanceCard',
  'HistoryCard',
  'InboundsTable',
  'PortsAccordion',
]);
function expand(node) {
  if (Array.isArray(node)) return node.flatMap(expand);
  if (node?.type && typeof node.type === 'function' && inlined.has(node.type.name)) {
    return expand(node.type(node.props));
  }
  return node;
}
function children(node) {
  const value = expand(node);
  if (Array.isArray(value)) return value;
  if (value?.props?.children == null) return [];
  return Array.isArray(value.props.children) ? value.props.children : [value.props.children];
}
function walk(node, visit) {
  if (node == null) return;
  const value = expand(node);
  if (value == null) return;
  visit(value);
  for (const child of children(value)) walk(child, visit);
}
function text(node) {
  const value = expand(node);
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  return children(value).map(text).join(' ');
}
function findAll(node, predicate) {
  const found = [];
  walk(node, (child) => {
    if (predicate(child)) found.push(child);
  });
  return found;
}
function buttons(tree) {
  return findAll(tree, (node) => typeName(node) === 'Button');
}
function buttonLabels(tree) {
  return buttons(tree).map((node) => text(node).trim());
}
function button(tree, label) {
  const hit = buttons(tree).find((node) => text(node).trim() === label);
  assert.ok(hit, `找不到按钮：${label}（现有：${buttonLabels(tree).join('、')}）`);
  return hit;
}
/** 确认对话框在真实组件里由 ActionDialog 渲染；这里取它的 props 直接跑确认流程。 */
function dialogOf(tree) {
  return findAll(tree, (node) => node?.props?.title === '删除观测记录' || node?.props?.title === '重置本节点观测')[0];
}

const instance = (id, extra = {}) => ({
  id,
  core: 'sing-box',
  source: 'generic',
  ownership: 'external',
  version: 'v1.0.0',
  running: true,
  pid: 100,
  service: '',
  binary: '/etc/s-box/sing-box',
  config_paths: [],
  ports: [],
  inbounds: [],
  issues: [],
  stats_status: 'not_configured',
  config_read_at: 1,
  last_success: 1,
  received_at: 1,
  absent_at: 0,
  stale: false,
  absent: false,
  truncated: false,
  ...extra,
});
const scan = (extra = {}) => ({
  complete: true,
  collected_at: 1,
  received_at: 1,
  instances: 0,
  stale: false,
  ...extra,
});
const baseData = (instances, scanState, extra = {}) => ({
  management: 'external',
  online: true,
  supported: true,
  scan: scanState,
  instances,
  ...extra,
});

// 用内存 hooks 和受控的 API 边界跑真实组件：不需要浏览器或网络。
function mount({ deletes = [], resets = [], refresh = [] } = {}) {
  const states = [];
  const toasts = [];
  const mutated = [];
  let cursor = 0;
  const { ObservedInstances } = load(
    transpile('../src/sections/proxy/detail/observed-instances.tsx'),
    (id) => {
      if (id === 'react/jsx-runtime') return jsx;
      if (id === 'react') {
        return {
          useState(initial) {
            const index = cursor++;
            if (!(index in states)) states[index] = initial;
            return [states[index], (value) => (states[index] = value)];
          },
        };
      }
      if (id === 'swr') {
        return { useSWRConfig: () => ({ mutate: async (filter) => mutated.push(filter) }) };
      }
      // MUI 组件用同名占位标签；ActionDialog 保留真实子节点，方便断言确认文案。
      if (id.startsWith('@mui/material/') || id === '@mui/x-data-grid') {
        return { default: id.split('/').at(-1), DataGrid: 'DataGrid' };
      }
      if (id === 'src/components/label') return { Label: 'Label' };
      if (id === 'src/components/snackbar') {
        return {
          toast: {
            success: (message) => toasts.push(['success', message]),
            error: (message) => toasts.push(['error', message]),
          },
        };
      }
      if (id === 'src/auth/utils') return { getErrorMessage: (error) => error.message };
      if (id === 'src/api/proxy-observation') {
        return {
          proxyObservationsKey: (server) => `/api/servers/${server}/proxy-observations`,
          refreshProxyObservations: async (server) => refresh.push(server),
          deleteProxyObservation: async (server, item) => deletes.push([server, item]),
          resetProxyObservations: async () => resets.shift(),
        };
      }
      if (id === '../shared') {
        return {
          ActionDialog: ({ title, children: body, onConfirm }) => jsx.jsx('ActionDialog', { title, onConfirm }, body),
        };
      }
      if (id === '../observation-helpers') return helpers;
      throw new Error(`Unexpected import: ${id}`);
    }
  );
  const render = (data) => {
    cursor = 0;
    return ObservedInstances({ serverId: 7, data });
  };
  return { render, toasts, mutated, deletes, refresh };
}

test('absent instances leave the current list and enter the collapsed history', () => {
  const { render } = mount();
  const data = baseData(
    [instance('a'), instance('b', { absent: true, absent_at: 500, running: false })],
    scan({ instances: 1, received_at: 500 })
  );
  const tree = render(data);
  assert.match(text(tree), /历史观测记录（\s*1\s*）/);
  assert.match(text(tree), /确认消失/);
  assert.match(text(tree), /最后一次观测到的状态：\s*未运行/);
  // 历史记录整体收在默认折叠的 Accordion 里，入站与端口各收进它内部的一层。
  const history = findAll(
    tree,
    (node) => typeName(node) === 'Accordion' && text(node).includes('历史观测记录')
  )[0];
  assert.ok(history, '历史记录必须收在折叠面板里');
  assert.equal(history.props.expanded, undefined, '历史记录不能默认展开');
  assert.equal(history.props.defaultExpanded, undefined, '历史记录不能默认展开');
  assert.equal(findAll(history, (node) => typeName(node) === 'Accordion').length, 3);
  assert.match(text(history), /最后一次快照的入站/);
  assert.match(text(history), /那次快照观测到的绑定端口/);
  assert.doesNotMatch(text(tree), /当前未发现代理实例/);
  assert.equal(buttonLabels(tree).includes('删除观测记录'), true);
});

test('history shows the last known port bindings with address, port and network', () => {
  const { render } = mount();
  const data = baseData(
    [
      instance('b', {
        absent: true,
        absent_at: 500,
        running: false,
        ports: [
          { address: '0.0.0.0', port: 443, network: 'tcp' },
          { address: '::', port: 8443, network: 'udp' },
        ],
      }),
    ],
    scan({ instances: 0, received_at: 500 })
  );
  const text_ = text(render(data));
  assert.match(text_, /0\.0\.0\.0:443\/tcp/);
  assert.match(text_, /::\:8443\/udp/);
  assert.match(text_, /不代表现在仍然绑定/);
  assert.doesNotMatch(text_, /已被手动删除/);
});

test('an expired scan keeps last-known values but never calls them current', () => {
  const { render } = mount();
  const data = baseData(
    [instance('a', { stale: true })],
    scan({ instances: 1, stale: true, received_at: 1 })
  );
  const tree = render(data);
  assert.match(text(tree), /超过 90 秒未更新/);
  assert.match(text(tree), /不是节点现在的状态/);
  assert.doesNotMatch(text(tree), /当前未发现代理实例/);
});

test('a node that keeps reporting still marks the single instance it no longer reports', () => {
  const { render } = mount();
  // 节点一直在上报（scan 没过期），但这一条的最后一次收到停在很早以前，
  // 并且带着 config_invalid_jsonc：它的进程/端口是历史读数，配置摘要也来自更早的成功读取。
  const data = baseData(
    [instance('a', { stale: true, received_at: 100, issues: ['config_invalid_jsonc'] })],
    scan({ instances: 1, received_at: 1000 })
  );
  const tree = render(data);
  const shown = text(tree);
  assert.doesNotMatch(shown, /超过 90 秒未更新/);
  assert.match(shown, /运行中 · 未确认/);
  assert.match(shown, /这一条不在节点最近一次采集里/);
  assert.match(shown, /不是节点最近一次采集的结果/);
  assert.doesNotMatch(shown, /PID、内存和端口绑定是本次读取的结果/);
  assert.match(shown, /用量按那次上报的结果展示/);

  // 同一轮里出现过的实例照旧是当前状态。
  const fresh = render(
    baseData([instance('b', { received_at: 1000 })], scan({ instances: 1, received_at: 1000 }))
  );
  assert.match(text(fresh), /运行中/);
  assert.doesNotMatch(text(fresh), /未确认|不是节点最近一次采集的结果/);
});

test('awaiting, offline and partial scans never claim instances disappeared', () => {
  const { render } = mount();
  assert.match(text(render(baseData([], null))), /等待首个完整观测快照/);
  assert.doesNotMatch(text(render(baseData([], null))), /当前未发现代理实例/);

  const offline = render(baseData([], null, { online: false }));
  assert.match(text(offline), /探针离线，无法确认当前实例/);
  assert.doesNotMatch(text(offline), /当前未发现代理实例/);

  const partial = render(baseData([instance('a')], scan({ complete: false, instances: 1 })));
  assert.match(text(partial), /采集不完整/);
  assert.doesNotMatch(text(partial), /当前未发现代理实例/);

  const empty = render(baseData([], scan()));
  assert.match(text(empty), /当前未发现代理实例/);
});

test('deleting a history record confirms, calls the API and refreshes the cache', async () => {
  const deletes = [];
  const { render, toasts, mutated } = mount({ deletes });
  const data = baseData(
    [instance('b', { absent: true, absent_at: 500 })],
    scan({ instances: 0, received_at: 500 })
  );
  button(render(data), '删除观测记录').props.onClick();
  const tree = render(data);
  const dialog = dialogOf(tree);
  assert.equal(dialog.props.title, '删除观测记录');
  assert.match(text(dialog), /只删除面板保存的这条观测记录/);
  assert.match(text(dialog), /不会停止或卸载节点上的代理/);
  assert.match(text(dialog), /重新发现/);
  await dialog.props.onConfirm();
  assert.deepEqual(deletes, [[7, 'b']]);
  assert.equal(mutated.length, 1, '删除后必须重新拉取观测缓存');
  assert.deepEqual(toasts.at(-1), ['success', '已删除该条观测记录，节点未受影响']);
});

test('reset distinguishes cleared, requested and offline waiting without faking a finished scan', async () => {
  const cases = [
    [{ cleared: true, requested: true, online: true }, /已请求重新采集/],
    [{ cleared: true, requested: false, online: true }, /未确认观测能力/],
    [{ cleared: true, requested: false, online: false }, /节点离线，等待探针上线/],
  ];
  for (const [result, expected] of cases) {
    const resets = [result];
    const { render, toasts, mutated } = mount({ resets });
    const data = baseData([instance('a')], scan({ instances: 1 }));
    button(render(data), '重置本节点观测').props.onClick();
    const tree = render(data);
    const dialog = dialogOf(tree);
    assert.equal(dialog.props.title, '重置本节点观测');
    assert.match(text(dialog), /不会删除代理、二进制、配置、systemd 服务或托管记录/);
    await dialog.props.onConfirm();
    assert.equal(mutated.length >= 1, true, '重置后必须更新 SWR 缓存');
    const [, message] = toasts.at(-1);
    assert.match(message, expected);
    assert.doesNotMatch(message, /采集已完成|扫描完成/);
  }
});

test('refresh reports only that a read-only collection was requested', async () => {
  const refresh = [];
  const { render, toasts } = mount({ refresh });
  button(render(baseData([instance('a')], scan({ instances: 1 }))), '刷新观测').props.onClick();
  // onClick 是异步的：等微任务队列清空后检查提示。
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(refresh, [7]);
  assert.match(toasts.at(-1)[1], /已请求只读采集/);
  assert.doesNotMatch(toasts.at(-1)[1], /扫描完成/);
});

test('reset is unavailable without saved records and refresh needs an online capable probe', () => {
  const { render } = mount();
  const noRecords = render(baseData([], scan()));
  assert.equal(button(noRecords, '重置本节点观测').props.disabled, true);
  const offline = render(baseData([instance('a')], scan({ instances: 1 }), { online: false }));
  assert.equal(button(offline, '刷新观测').props.disabled, true);
  assert.equal(button(offline, '重置本节点观测').props.disabled, false, '离线也应允许清理面板记录');
});
