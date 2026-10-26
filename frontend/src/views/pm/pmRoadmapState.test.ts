import assert from 'node:assert/strict';
import { test } from 'node:test';

import { canAdvanceStep, mergeDraft } from './pmRoadmapState';

// 步骤推进：当前步必填项未完成时不允许进入下一步
test('canAdvanceStep', () => {
  assert.equal(canAdvanceStep(1, { project_brief: '' }), false);
  assert.equal(canAdvanceStep(1, { project_brief: '面向 B 端的客服知识库' }), true);
  assert.equal(canAdvanceStep(5, { scope: '首期' }), false); // 第 5 步需已生成草稿
});

// 草稿合并：分步保存时只覆盖本步字段，不清空其他步
test('mergeDraft', () => {
  const base = { project_brief: 'A', scope: 'B', features: [] };
  assert.deepEqual(mergeDraft(base, { scope: 'B2' }), { project_brief: 'A', scope: 'B2', features: [] });
});

// 功能清单：每项必须补齐三级路径才可进入下一步
test('canAdvanceStep checks feature path', () => {
  const base = { module_name: '工单', feature_name: '工单流转', operation_name: '转派' };
  assert.equal(canAdvanceStep(2, { features: [base] }), true);
  assert.equal(canAdvanceStep(2, { features: [{ ...base, operation_name: ' ' }] }), false);
  assert.equal(canAdvanceStep(2, { features: [] }), false);
});

// 首期范围：至少勾选一个功能点
test('canAdvanceStep requires scope', () => {
  assert.equal(canAdvanceStep(4, { scope: ['工单/工单流转/转派'] }), true);
  assert.equal(canAdvanceStep(4, { scope: [] }), false);
});
