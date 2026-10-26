import { test } from 'node:test';
import assert from 'node:assert/strict';
import { REQUIREMENT_STATUS_MAP, VERSION_STATUS_MAP, REQUIREMENT_BOARD_COLUMNS } from './pmStatus';

// 状态映射必须覆盖全部枚举值，防止新增状态漏翻译
test('需求状态映射覆盖七个状态', () => {
  const keys = ['pending_analysis', 'confirmed', 'scheduled', 'developing', 'launched', 'rejected', 'closed'];
  for (const k of keys) assert.ok(REQUIREMENT_STATUS_MAP[k], `missing ${k}`);
});

test('看板列不包含终止态', () => {
  const cols = REQUIREMENT_BOARD_COLUMNS.map((c) => c.status);
  assert.ok(!cols.includes('rejected'));
  assert.ok(!cols.includes('closed'));
});

test('版本状态映射覆盖六个状态', () => {
  const keys = ['planning', 'reviewing', 'finalized', 'developing', 'launched', 'shelved'];
  for (const k of keys) assert.ok(VERSION_STATUS_MAP[k], `missing ${k}`);
});
