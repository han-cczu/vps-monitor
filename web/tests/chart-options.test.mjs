import test from 'node:test';
import assert from 'node:assert/strict';
import { mergeChartOptions } from '../src/components/chart/merge-options.ts';

test('multi-axis and tooltip arrays replace default objects', () => {
  const base = { yaxis: { tickAmount: 5 }, tooltip: { y: { title: { formatter: () => 'default' } } } };
  const overrides = { yaxis: [{ seriesName: ['avg', 'max'] }, { seriesName: 'loss', opposite: true, max: 100 }], tooltip: { y: [{ formatter: () => 'ms' }, { formatter: () => '%' }] } };
  const result = mergeChartOptions(base, overrides);
  assert.ok(Array.isArray(result.yaxis));
  assert.equal(result.yaxis.length, 2);
  assert.equal(result.yaxis[1].max, 100);
  assert.ok(Array.isArray(result.tooltip.y));
  assert.equal(result.tooltip.y[1].formatter(50), '%');
  assert.deepEqual(base.yaxis, { tickAmount: 5 });
  result.yaxis[0].seriesName.push('changed');
  assert.deepEqual(overrides.yaxis[0].seriesName, ['avg', 'max']);
});
test('repeated renders neither mutate defaults nor retain old array items', () => {
  const base = { stroke: { width: 2 }, colors: ['green', 'yellow', 'red'] };
  mergeChartOptions(base, { stroke: { width: [2, 1, 0] } });
  const result = mergeChartOptions(base, { colors: ['blue'] });
  assert.equal(base.stroke.width, 2);
  assert.deepEqual(result.colors, ['blue']);
  assert.equal(result.stroke.width, 2);
});
