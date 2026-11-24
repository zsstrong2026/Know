import { test } from 'node:test';
import assert from 'node:assert/strict';
import { clampTipPosition } from './help-tip';

test('气泡视口钳位与上方不足翻转', () => {
  const vp = { width: 1280, height: 800 };
  const anchor = { top: 300, left: 600, width: 16, height: 16, bottom: 316, right: 616 };
  const p = clampTipPosition(anchor as any, { width: 280, height: 120 }, vp);
  assert.equal(p.flipped, false);
  assert.ok(p.top >= 0 && p.left >= 0 && p.left + 280 <= 1280);
  const nearTop = { ...anchor, top: 8, bottom: 24 };
  const p2 = clampTipPosition(nearTop as any, { width: 280, height: 120 }, vp);
  assert.equal(p2.flipped, true); // 翻转到下方
  assert.ok(p2.top >= 24); // 箭头仍指向图标
  const nearRight = { ...anchor, left: 1270, right: 1286 };
  const p3 = clampTipPosition(nearRight as any, { width: 280, height: 120 }, vp);
  assert.ok(p3.left + 280 <= 1280); // 横向钳位
});
