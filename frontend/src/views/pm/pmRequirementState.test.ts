import assert from 'node:assert/strict';
import { test } from 'node:test';

import { canAnalyzeRequirement, groupByStatus, parseAnalysisSections } from './pmRequirementState';

// 看板分列：终止态不进入看板，由「已归档」筛选查看
test('groupByStatus', () => {
  const items = [
    { id: '1', status: 'pending_analysis' },
    { id: '2', status: 'rejected' },
  ];
  const cols = groupByStatus(items);
  assert.equal(cols.find((c) => c.status === 'pending_analysis')!.items.length, 1);
  assert.equal(cols.find((c) => c.status === 'rejected'), undefined);
});

// AI 分析入口：未填来源不可点击
test('canAnalyzeRequirement', () => {
  assert.equal(canAnalyzeRequirement({ title: '登录优化', source: '' }), false);
  assert.equal(canAnalyzeRequirement({ title: '登录优化', source: 'customer' }), true);
});

// 分析方案解析：四段固定标题，不再输出建议优先级
test('parseAnalysisSections', () => {
  const parsed = parseAnalysisSections([
    '## 1. 需求价值',
    '提升报表导出效率',
    '## 2. 关联功能影响',
    '导出中心需要同步改造',
    '## 3. 技术风险',
    'CSV 编码需兼容',
    '## 4. 实现方案',
    '复用现有导出管线',
  ].join('\n'));
  assert.equal(parsed.value, '提升报表导出效率');
  assert.equal(parsed.impact, '导出中心需要同步改造');
  assert.equal(parsed.risk, 'CSV 编码需兼容');
  assert.equal(parsed.designPlan, '复用现有导出管线');
  assert.deepEqual(Object.keys(parsed).sort(), ['designPlan', 'impact', 'risk', 'value']);
});
