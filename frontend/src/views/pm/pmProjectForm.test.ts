import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validateProjectName } from './pmProjectForm';

// 新建项目：名称必填、去首尾空格、长度上限 255
test('validateProjectName', () => {
  assert.equal(validateProjectName('  '), '项目名称不能为空');
  assert.equal(validateProjectName('a'.repeat(256)), '项目名称不能超过 255 个字符');
  assert.equal(validateProjectName('  智能客服  '), '');
});
