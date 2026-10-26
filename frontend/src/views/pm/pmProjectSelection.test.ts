import { test } from 'node:test';
import assert from 'node:assert/strict';
import { resolveInitialProjectId, resolveTabRoute } from './pmProjectSelection';

// 初始项目：上次项目仍存在则沿用，否则回退首个；列表为空返回空串
test('resolveInitialProjectId', () => {
  assert.equal(resolveInitialProjectId('b', ['a', 'b']), 'b');
  assert.equal(resolveInitialProjectId('gone', ['a', 'b']), 'a');
  assert.equal(resolveInitialProjectId('', ['a']), 'a');
  assert.equal(resolveInitialProjectId('a', []), '');
});

// Tab 归属：详情页归一，未知路由回退功能规划
test('resolveTabRoute', () => {
  assert.equal(resolveTabRoute('pmVersionDetail'), 'pmVersions');
  assert.equal(resolveTabRoute('pmMaterialPreview'), 'pmMaterials');
  assert.equal(resolveTabRoute('pmRequirements'), 'pmRequirements');
  assert.equal(resolveTabRoute('unknown'), 'pmRoadmap');
});
