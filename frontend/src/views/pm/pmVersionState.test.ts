import assert from 'node:assert/strict';
import { test } from 'node:test';

import { canDevelop, canFinalize } from './pmVersionState';

// 7 步流程的可用性判定：定稿前不可开发，未通过评审不可定稿
test('versionActions', () => {
  assert.equal(canFinalize({ status: 'reviewing', review_conclusion: 'reject' }), false);
  assert.equal(canFinalize({ status: 'reviewing', review_conclusion: 'conditional' }), true);
  assert.equal(canDevelop({ status: 'planning' }), false);
  assert.equal(canDevelop({ status: 'finalized' }), true);
});
