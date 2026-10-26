import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildRequirementQuery } from './index';

// URL 与查询参数构造
test('需求列表查询参数只拼接非空筛选项', () => {
  assert.equal(
    buildRequirementQuery({ status: 'confirmed', priority: '', source: 'customer' }),
    '?status=confirmed&source=customer',
  );
});

test('需求列表无筛选项时不拼问号', () => {
  assert.equal(buildRequirementQuery(), '');
  assert.equal(buildRequirementQuery({ status: '  ', priority: '' }), '');
});
